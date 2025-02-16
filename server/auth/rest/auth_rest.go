// Package rest provides authentication by calling a separate process over REST API (technically JSON RPC, not REST).
package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// authenticator is the type to map authentication methods to.
type authenticator struct {
	// Logical name of this authenticator
	name string
	// URL of the server
	serverUrl string
	// Authenticator may add new accounts to local database.
	allowNewAccounts bool
	// Use separate endpoints, i.e. add request name to serverUrl path when making requests.
	useSeparateEndpoints bool
	// Cache of restricted tag prefixes (namespaces).
	rTagNS []string
	// Optional regex pattern for checking tokens.
	reToken *regexp.Regexp
}

// Request to the server.
type request struct {
	Endpoint   string    `json:"endpoint"`
	Name       string    `json:"name"`
	Record     *auth.Rec `json:"rec,omitempty"`
	Secret     []byte    `json:"secret,omitempty"`
	RemoteAddr string    `json:"addr,omitempty"`
}

// User initialization data when creating a new user.
type newAccount struct {
	// Default access mode
	Auth string `json:"auth,omitempty"`
	Anon string `json:"anon,omitempty"`
	// User's Public data
	Public interface{} `json:"public,omitempty"`
	// User's Trusted data
	Trusted interface{} `json:"trusted,omitempty"`
	// Per-subscription private data
	Private interface{} `json:"private,omitempty"`
}

// Response from the server.
type response struct {
	// Error message in case of an error.
	Err string `json:"err,omitempty"`
	// Optional auth record
	Record *auth.Rec `json:"rec,omitempty"`
	// Optional byte slice
	ByteVal []byte `json:"byteval,omitempty"`
	// Optional time value
	TimeVal time.Time `json:"ts,omitempty"`
	// Boolean value
	BoolVal bool `json:"boolval,omitempty"`
	// String slice value
	StrSliceVal []string `json:"strarr,omitempty"`
	// Account creation data
	NewAcc *newAccount `json:"newacc,omitempty"`
}

// Init initializes the handler.
func (a *authenticator) Init(jsonconf json.RawMessage, name string) error {
	if name == "" {
		return errors.New("auth_rest: authenticator name cannot be blank")
	}

	if a.name != "" {
		return errors.New("auth_rest: already initialized as " + a.name + "; " + name)
	}

	type configType struct {
		// ServerUrl is the URL of the server to call.
		ServerUrl string `json:"server_url"`
		// Server may create new accounts.
		AllowNewAccounts bool `json:"allow_new_accounts"`
		// Use separate endpoints, i.e. add request name to serverUrl path when making requests.
		UseSeparateEndpoints bool `json:"use_separate_endpoints"`
	}

	var config configType
	err := json.Unmarshal(jsonconf, &config)
	if err != nil {
		return errors.New("auth_rest: failed to parse config: " + err.Error() + "(" + string(jsonconf) + ")")
	}

	serverUrl, err := url.Parse(config.ServerUrl)
	if err != nil || !serverUrl.IsAbs() {
		return errors.New("auth_rest: invalid server_url '" + string(jsonconf) + "'")
	}

	if !strings.HasSuffix(serverUrl.Path, "/") {
		serverUrl.Path += "/"
	}

	a.name = name
	a.serverUrl = serverUrl.String()
	a.allowNewAccounts = config.AllowNewAccounts
	a.useSeparateEndpoints = config.UseSeparateEndpoints

	return nil
}

// IsInitialized returns true if the handler is initialized.
func (a *authenticator) IsInitialized() bool {
	return a.name != ""
}

// Execute HTTP POST to the server at the specified endpoint and with the provided payload.
func (a *authenticator) callEndpoint(endpoint string, rec *auth.Rec, secret []byte, remoteAddr string) (*response, error) {
	// Convert payload to json.
	req := &request{Endpoint: endpoint, Name: a.name, Record: rec, Secret: secret, RemoteAddr: remoteAddr}
	content, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	urlToCall := a.serverUrl
	if a.useSeparateEndpoints {
		epUrl, _ := url.Parse(a.serverUrl)
		epUrl.Path += endpoint
		urlToCall = epUrl.String()
	}

	// Send payload to server using default HTTP client.
	post, err := http.Post(urlToCall, "application/json", bytes.NewBuffer(content))
	if err != nil {
		return nil, err
	}
	defer post.Body.Close()

	// Check HTTP status response. Must be 2xx.
	if post.StatusCode < http.StatusOK || post.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.New("unexpected HTTP response " + post.Status)
	}

	// Read response.
	body, err := ioutil.ReadAll(post.Body)
	if err != nil {
		return nil, err
	}

	// Parse response.
	var resp response
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Err != "" {
		return nil, types.StoreError(resp.Err)
	}

	return &resp, nil
}

// AddRecord adds persistent authentication record to the database.
// Returns: updated auth record, error
func (a *authenticator) AddRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	resp, err := a.callEndpoint("add", rec, secret, remoteAddr)
	if err != nil {
		return nil, err
	}

	return resp.Record, nil
}

// UpdateRecord updates existing record with new credentials.
func (a *authenticator) UpdateRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	_, err := a.callEndpoint("upd", rec, secret, remoteAddr)
	return rec, err
}

// Authenticate: get user record by provided secret
func (a *authenticator) Authenticate(secret []byte, remoteAddr string) (*auth.Rec, []byte, error) {
	resp, err := a.callEndpoint("auth", nil, secret, remoteAddr)
	if err != nil {
		return nil, nil, err
	}

	// Auth record not found.
	if resp.Record == nil {
		logs.Warn.Println("rest_auth: invalid response: missing Record")
		return nil, nil, types.ErrInternal
	}

	// Check if server provided a user ID. If not, create a new account in the local database.
	if resp.Record.Uid.IsZero() && a.allowNewAccounts {
		if resp.NewAcc == nil {
			return nil, nil, types.ErrNotFound
		}

		// Create account, get UID, report UID back to the server.

		user := types.User{
			State:   resp.Record.State,
			Public:  resp.NewAcc.Public,
			Trusted: resp.NewAcc.Trusted,
			Tags:    resp.Record.Tags,
		}
		user.Access.Auth.UnmarshalText([]byte(resp.NewAcc.Auth))
		user.Access.Anon.UnmarshalText([]byte(resp.NewAcc.Anon))
		_, err = store.Users.Create(&user, resp.NewAcc.Private)
		if err != nil {
			return nil, nil, err
		}

		// Report the new UID to the server.
		resp.Record.Uid = user.Uid()
		_, err = a.callEndpoint("link", resp.Record, secret, "")
		if err != nil {
			store.Users.Delete(resp.Record.Uid, true)
			return nil, nil, err
		}
	}

	return resp.Record, resp.ByteVal, nil
}

// AsTag converts search token into prefixed tag or an empty string if it
// cannot be represented as a prefixed tag.
func (a *authenticator) AsTag(token string) string {
	if len(a.rTagNS) > 0 {
		if a.reToken != nil && !a.reToken.MatchString(token) {
			return ""
		}
		// No validation or passed validation.
		return a.rTagNS[0] + ":" + token
	}
	return ""
}

// IsUnique verifies if the provided secret can be considered unique by the auth
// scheme as well as policy compliance. E.g. if login is unique and not too short/long.
func (a *authenticator) IsUnique(secret []byte, remoteAddr string) (bool, error) {
	resp, err := a.callEndpoint("checkunique", nil, secret, remoteAddr)
	if err != nil {
		return false, err
	}

	return resp.BoolVal, err
}

// GenSecret generates a new secret, if appropriate.
func (a *authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	resp, err := a.callEndpoint("gen", rec, nil, "")
	if err != nil {
		return nil, time.Time{}, err
	}

	return resp.ByteVal, resp.TimeVal, err
}

// DelRecords deletes all authentication records for the given user.
func (a *authenticator) DelRecords(uid types.Uid) error {
	logs.Info.Println("DelRecords, initialized=", a.name != "")
	_, err := a.callEndpoint("del", &auth.Rec{Uid: uid}, nil, "")
	return err
}

// RestrictedTags returns tag namespaces (prefixes, such as prefix:login) restricted by the server.
func (a *authenticator) RestrictedTags() ([]string, error) {
	if a.rTagNS != nil {
		// Using cached prefixes.
		ns := make([]string, len(a.rTagNS))
		// Returning a copy to prevent accidental modification of server-provided tags.
		copy(ns, a.rTagNS)
		return ns, nil
	}

	// First time use, fetch prefixes from the server.
	resp, err := a.callEndpoint("rtagns", nil, nil, "")
	if err != nil {
		return nil, err
	}

	// Save valid result to cache.
	a.rTagNS = resp.StrSliceVal
	if len(resp.ByteVal) > 0 {
		a.reToken, err = regexp.Compile(string(resp.ByteVal))
		if err != nil {
			logs.Warn.Println("rest_auth: invalid token regexp", string(resp.ByteVal))
		}
	}
	return resp.StrSliceVal, nil
}

// GetResetParams returns authenticator parameters passed to password reset handler
// (none for rest).
func (authenticator) GetResetParams(uid types.Uid) (map[string]interface{}, error) {
	// TODO: route request to the server.
	return nil, nil
}

const realName = "rest"

// GetRealName returns the hardcoded name of the authenticator.
func (authenticator) GetRealName() string {
	return realName
}

func init() {
	store.RegisterAuthScheme(realName, &authenticator{})
}
