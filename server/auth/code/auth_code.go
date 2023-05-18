// Package code implements temporary no-login authentication by short numeric code.
package code

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// authenticator is a singleton instance of the authenticator.
type authenticator struct {
	name         string
	codeLength   int
	maxCodeValue *big.Int
	lifetime     time.Duration
	maxRetries   int
}

// Init initializes the authenticator: parses the config and sets internal state.
func (ca *authenticator) Init(jsonconf json.RawMessage, name string) error {
	if name == "" {
		return errors.New("auth_code: authenticator name cannot be blank")
	}

	if ca.name != "" {
		return errors.New("auth_code: already initialized as " + ca.name + "; " + name)
	}

	type configType struct {
		// Length of the security code.
		CodeLength int `json:"code_length"`
		// Code expiration time in seconds.
		ExpireIn int `json:"expire_in"`
		// Maximum number of verification attempts per code.
		MaxRetries int `json:"max_retries"`
	}
	var config configType
	if err := json.Unmarshal(jsonconf, &config); err != nil {
		return errors.New("auth_code: failed to parse config: " + err.Error() + "(" + string(jsonconf) + ")")
	}

	if config.ExpireIn <= 0 {
		return errors.New("auth_code: invalid expiration period")
	}

	if config.CodeLength < 4 {
		return errors.New("auth_code: invalid code length")
	}

	if config.MaxRetries < 1 {
		return errors.New("auth_code: invalid reties count")
	}

	ca.name = name
	ca.codeLength = config.CodeLength
	ca.maxCodeValue = big.NewInt(0).Exp(big.NewInt(10), big.NewInt(int64(ca.codeLength)), nil)
	ca.lifetime = time.Duration(config.ExpireIn) * time.Second
	ca.maxRetries = config.MaxRetries

	return nil
}

// IsInitialized returns true if the handler is initialized.
func (ca *authenticator) IsInitialized() bool {
	return ca.name != ""
}

// AddRecord is not supported, will produce an error.
func (authenticator) AddRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	return nil, types.ErrUnsupported
}

// UpdateRecord is not supported, will produce an error.
func (authenticator) UpdateRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	return nil, types.ErrUnsupported
}

// Authenticate checks validity of provided short code.
// The secret is structured as <code>:<cred_method>:<cred_value>, "123456:email:alice@example.com".
func (ca *authenticator) Authenticate(secret []byte, remoteAddr string) (*auth.Rec, []byte, error) {
	parts := strings.SplitN(string(secret), ":", 2)
	if len(parts) != 2 {
		return nil, nil, types.ErrMalformed
	}

	code, cred := parts[0], parts[1]
	key := sanitizeKey(realName + "_" + cred)

	value, err := store.PCache.Get(key)
	if err != nil {
		if err == types.ErrNotFound {
			err = types.ErrFailed
		}
		return nil, nil, err
	}

	// code:count:uid
	parts = strings.Split(value, ":")
	if len(parts) != 3 {
		return nil, nil, types.ErrInternal
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, nil, types.ErrInternal
	}

	if count >= ca.maxRetries {
		return nil, nil, types.ErrFailed
	}

	if parts[0] != code {
		// Update count of attempts. If the update fails, the error is ignored.
		store.PCache.Upsert(key, parts[0]+":"+strconv.Itoa(count+1)+":"+parts[2], false)
		return nil, nil, types.ErrFailed
	}

	// Success. Remove no longer needed entry. The error is ignored here.
	if err = store.PCache.Delete(key); err != nil {
		logs.Warn.Println("code_auth: error deleting key", key, err)
	}

	return &auth.Rec{
		Uid:        types.ParseUid(parts[2]),
		AuthLevel:  auth.LevelNone,
		Lifetime:   auth.Duration(ca.lifetime),
		Features:   auth.FeatureNoLogin,
		State:      types.StateUndefined,
		Credential: cred}, nil, nil
}

// GenSecret generates a new code.
func (ca *authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	// Run garbage collection.
	store.PCache.Expire(realName+"_", time.Now().UTC().Add(-ca.lifetime))

	// Generate random code.
	code, err := rand.Int(rand.Reader, ca.maxCodeValue)
	if err != nil {
		return nil, time.Time{}, types.ErrInternal
	}

	// Convert the code to fixed length string.
	resp := strconv.FormatInt(code.Int64(), 10)
	resp = strings.Repeat("0", ca.codeLength-len(resp)) + resp

	if rec.Lifetime == 0 {
		rec.Lifetime = auth.Duration(ca.lifetime)
	} else if rec.Lifetime < 0 {
		return nil, time.Time{}, types.ErrExpired
	}

	// Save "code:counter:uid" to the database. The key is code_<credential>.
	if err = store.PCache.Upsert(sanitizeKey(realName+"_"+rec.Credential), resp+":0:"+rec.Uid.String(), true); err != nil {
		return nil, time.Time{}, err
	}

	expires := time.Now().Add(time.Duration(rec.Lifetime)).UTC().Round(time.Millisecond)

	return []byte(resp), expires, nil
}

// AsTag is not supported, will produce an empty string.
func (authenticator) AsTag(token string) string {
	return ""
}

// IsUnique is not supported, will produce an error.
func (authenticator) IsUnique(secret []byte, remoteAddr string) (bool, error) {
	return false, types.ErrUnsupported
}

// DelRecords adds disabled user ID to a stop list.
func (authenticator) DelRecords(uid types.Uid) error {
	return nil
}

// RestrictedTags returns tag namespaces restricted by this authenticator (none for short code).
func (authenticator) RestrictedTags() ([]string, error) {
	return nil, nil
}

// GetResetParams returns authenticator parameters passed to password reset handler
// (none for short code).
func (authenticator) GetResetParams(uid types.Uid) (map[string]interface{}, error) {
	return nil, nil
}

// Replace all occurences of % with / to ensure SQL LIKE query works correctly.
func sanitizeKey(key string) string {
	return strings.ReplaceAll(key, "%", "/")
}

const realName = "code"

// GetRealName returns the hardcoded name of the authenticator.
func (authenticator) GetRealName() string {
	return realName
}

func init() {
	store.RegisterAuthScheme(realName, &authenticator{})
}
