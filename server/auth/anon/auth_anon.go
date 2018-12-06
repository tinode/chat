package anon

// Authentication without credentials. Most useful for customer support.
// Anonymous authentication is used only at account creation time.

import (
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// authenticator is the singleton instance of the anonymous authorizer.
type authenticator struct{}

// Init is a noop, always returns success.
func (authenticator) Init(unused string) error {
	return nil
}

// AddRecord checks authLevel and assigns default LevelAnon. Otherwise it
// just reports success.
func (authenticator) AddRecord(rec *auth.Rec, secret []byte) (*auth.Rec, error) {
	if rec.AuthLevel == auth.LevelNone {
		rec.AuthLevel = auth.LevelAnon
	}
	return rec, nil
}

// UpdateRecord is a noop. Just report success.
func (authenticator) UpdateRecord(rec *auth.Rec, secret []byte) error {
	return nil
}

// Authenticate is not supported. It's used only at account creation time.
func (authenticator) Authenticate(secret []byte) (*auth.Rec, []byte, error) {
	return nil, nil, types.ErrUnsupported
}

// IsUnique for a noop. Anonymous login does not use secret, any secret is fine.
func (authenticator) IsUnique(secret []byte) (bool, error) {
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

func init() {
	store.RegisterAuthScheme("anonymous", &authenticator{})
}
