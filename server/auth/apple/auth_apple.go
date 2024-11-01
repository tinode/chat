package apple

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

type authenticator struct {
	name string
	// default is https://appleid.apple.com/auth
	appleServerURL string
	clientID       string
	teamID         string
	keyID          string
	privateKey     interface{}
}

type appleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

// Init initializes the handler taking config string and logical name as parameters.
func (a *authenticator) Init(jsonconf json.RawMessage, name string) error {
	if name == "" {
		return errors.New("auth_apple: authenticator name cannot be blank")
	}
	if a.name != "" {
		return errors.New("auth_apple: already initialized as " + a.name + "; " + name)
	}

	type configType struct {
		AppleServerURL string `json:"apple_server_url"`
		ClientID       string `json:"client_id"`
		TeamID         string `json:"team_id"`
		KeyID          string `json:"key_id"`
		PrivateKeyPath string `json:"private_key_path"`
	}

	var config configType
	err := json.Unmarshal(jsonconf, &config)
	if err != nil {
		return errors.New("auth_apple: failed to parse config: " + err.Error() + "(" + string(jsonconf) + ")")
	}

	a.name = name
	a.appleServerURL = config.AppleServerURL
	a.clientID = config.ClientID
	a.teamID = config.TeamID
	a.keyID = config.KeyID
	err = a.loadCertificate(config.PrivateKeyPath)
	if err != nil {
		return err
	}

	return nil
}

func (a *authenticator) loadCertificate(privatePath string) error {
	pKeyByte, err := os.ReadFile(privatePath)
	if err != nil {
		return errors.New("failed to read private key: " + err.Error())
	}

	block, _ := pem.Decode(pKeyByte)
	pKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return errors.New("failed to parse private key: " + err.Error())
	}
	a.privateKey = pKey

	return nil
}

// IsInitialized returns true if the handler is initialized.
func (a *authenticator) IsInitialized() bool {
	return a.name != ""
}

// AddRecord adds persistent authentication record to the database.
// Returns: updated auth record, error
func (a *authenticator) AddRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	return nil, types.ErrUnsupported
}

// UpdateRecord updates existing record with new credentials.
// Returns updated auth record, error.
func (a *authenticator) UpdateRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	tokenResponse, err := a.validateRefreshToken(string(secret))
	if err != nil {
		return nil, errors.New("auth_apple: failed to exchange refresh token: " + err.Error())
	}

	return a.buildRecFromToken(tokenResponse)
}

func (a *authenticator) Authenticate(secret []byte, remoteAddr string) (*auth.Rec, []byte, error) {
	tokenResponse, err := a.validateCode(string(secret))
	if err != nil {
		return nil, nil, errors.New("auth_apple: failed to exchange code for token: " + err.Error())
	}

	rec, err := a.buildRecFromToken(tokenResponse)
	if err != nil {
		return nil, nil, err
	}

	return rec, nil, nil
}

func (a *authenticator) buildRecFromToken(tokenResponse *appleTokenResponse) (*auth.Rec, error) {
	idToken, err := jwt.Parse(tokenResponse.IDToken, func(token *jwt.Token) (interface{}, error) {
		return a.privateKey, nil
	})

	if err != nil {
		return nil, errors.New("failed to parse ID token: " + err.Error())
	}
	if !idToken.Valid {
		return nil, errors.New("invalid ID token")
	}

	claims, ok := idToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unable to parse token claims")
	}

	userID, ok := claims["sub"].(string)
	if !ok {
		return nil, errors.New("missing user ID in token claims")
	}

	email, _ := claims["email"].(string)
	exp, _ := claims["exp"].(float64)

	var uid types.Uid
	err = uid.UnmarshalText([]byte(userID))
	if err != nil {
		return nil, errors.New("invalid user ID")
	}

	rec := &auth.Rec{
		Uid:        uid,
		AuthLevel:  auth.LevelAuth,
		Lifetime:   auth.Duration(time.Duration(exp) * time.Second),
		Credential: email,
		Public:     map[string]interface{}{"email": email},
	}

	return rec, nil
}

// generateClientSecret generates a JWT client secret to be used in Apple authentication.
func (a *authenticator) generateClientSecret() (string, error) {
	claims := jwt.MapClaims{
		"iss": a.teamID,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour * 24).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": a.clientID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = a.keyID
	token.Header["alg"] = "ES256"

	clientSecret, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", errors.New("failed to sign client secret: " + err.Error())
	}
	return clientSecret, nil
}

// validateCode exchanges the authorization code for an access token from Apple.
func (a *authenticator) validateCode(code string) (*appleTokenResponse, error) {
	clientSecret, err := a.generateClientSecret()
	if err != nil {
		return nil, errors.New("failed to generate client secret for validation code: " + err.Error())
	}

	data := url.Values{}
	data.Set("client_id", a.clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")

	resp, err := a.sendRequest(a.appleServerURL+"/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.New("failed to send request: " + err.Error())
	}

	return resp, nil
}

func (a *authenticator) validateRefreshToken(refreshToken string) (*appleTokenResponse, error) {
	clientSecret, err := a.generateClientSecret()
	if err != nil {
		return nil, errors.New("failed to generate client secret for validation refresh token: " + err.Error())
	}

	data := url.Values{}
	data.Set("client_id", a.clientID)
	data.Set("client_secret", clientSecret)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	resp, err := a.sendRequest(a.appleServerURL+"/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errors.New("failed to send request: " + err.Error())
	}

	return resp, nil
}

func (a *authenticator) sendRequest(url string, request io.Reader) (*appleTokenResponse, error) {
	req, err := http.NewRequest("POST", url, request)
	if err != nil {
		return nil, errors.New("failed to create request: " + err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("failed to send request: " + err.Error())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("failed to read response body: " + err.Error())
	}

	var tokenResponse appleTokenResponse
	err = json.Unmarshal(body, &tokenResponse)
	if err != nil {
		return nil, errors.New("failed to parse response body [" + string(body) + "]: " + err.Error())
	}

	return &tokenResponse, nil
}

// GenSecret generates a new secret, if appropriate.
func (a *authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	return nil, time.Time{}, types.ErrUnsupported
}

// AsTag is not supported, will produce an empty string.
func (a *authenticator) AsTag(token string) string {
	return ""
}

// IsUnique is not supported, will produce an error.
func (a *authenticator) IsUnique(token []byte, remoteAddr string) (bool, error) {
	return false, types.ErrUnsupported
}

// DelRecords adds disabled user ID to a stop list.
func (a *authenticator) DelRecords(uid types.Uid) error {
	return types.ErrUnsupported
}

// RestrictedTags returns tag namespaces restricted by this authenticator (none for token).
func (a *authenticator) RestrictedTags() ([]string, error) {
	return []string{}, nil
}

// GetResetParams returns authenticator parameters passed to password reset handler
// (none for token).
func (a *authenticator) GetResetParams(uid types.Uid) (map[string]interface{}, error) {
	return nil, nil
}

const realName = "apple"

// GetRealName returns the hardcoded name of the authenticator.
func (a *authenticator) GetRealName() string {
	return realName
}

func init() {
	store.RegisterAuthScheme(realName, &authenticator{})
}
