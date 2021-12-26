// Package anon provides authentication without credentials. Most useful for customer support.
// Anonymous authentication is used only at the account creation time.
package anon

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// authenticator is the singleton instance of the anonymous authorizer.
type authenticator struct {
	name string
}

// Init is a noop, always returns success.
func (a *authenticator) Init(_ json.RawMessage, name string) error {
	if name == "" {
		return errors.New("auth_anonymous: authenticator name cannot be blank")
	}

	if a.name != "" {
		return errors.New("auth_anonymous: already initialized as " + a.name + "; " + name)
	}

	a.name = name
	return nil
}

// IsInitialized returns true if the handler is initialized.
func (a *authenticator) IsInitialized() bool {
	return a.name != ""
}

// AddRecord checks authLevel and assigns default LevelAnon. Otherwise it
// just reports success.
func (authenticator) AddRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	if rec.AuthLevel == auth.LevelNone {
		rec.AuthLevel = auth.LevelAnon
	}
	rec.State = types.StateOK
	return rec, nil
}

// UpdateRecord is a noop. Just report success.
func (authenticator) UpdateRecord(rec *auth.Rec, secret []byte, remoteAddr string) (*auth.Rec, error) {
	return rec, nil
}

// Authenticate is not supported. This authenticator is used only at account creation time.
func (authenticator) Authenticate(secret []byte, remoteAddr string) (*auth.Rec, []byte, error) {
	return nil, nil, types.ErrUnsupported
}

// AsTag is not supported, will produce an empty string.
func (authenticator) AsTag(token string) string {
	return ""
}

// IsUnique for a noop. Anonymous login does not use secret, any secret is fine.
func (authenticator) IsUnique(secret []byte, remoteAddr string) (bool, error) {
	return true, nil
}

// GenSecret always fails.
func (authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	return nil, time.Time{}, types.ErrUnsupported
}

// DelRecords is a noop which always succeeds.
func (authenticator) DelRecords(uid types.Uid) error {
	return nil
}

// RestrictedTags returns tag namespaces restricted by this authenticator (none for anonymous).
func (authenticator) RestrictedTags() ([]string, error) {
	return nil, nil
}

// GetResetParams returns authenticator parameters passed to password reset handler
// (none for anonymous).
func (authenticator) GetResetParams(uid types.Uid) (map[string]interface{}, error) {
	return nil, nil
}

const realName = "anonymous"

// GetRealName returns the hardcoded name of the authenticator.
func (authenticator) GetRealName() string {
	return realName
}

func init() {
	store.RegisterAuthScheme(realName, &authenticator{})
}
