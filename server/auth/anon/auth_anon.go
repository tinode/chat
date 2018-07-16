package anon

// Anonymous authentication is used only at account creation time.

import (
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// AnonAuth is the singleton instance of the anonymous authorizer.
type AnonAuth struct{}

// Init is a noop, always returns success.
func (AnonAuth) Init(unused string) error {
	return nil
}

// AddRecord is a noop. Just report success.
func (AnonAuth) AddRecord(rec *auth.Rec, secret []byte) (*auth.Rec, error) {
	return rec, nil
}

// UpdateRecord is a noop. Just report success.
func (AnonAuth) UpdateRecord(rec *auth.Rec, secret []byte) error {
	return nil
}

// Authenticate is not supported. It's used only at account creation time.
func (AnonAuth) Authenticate(secret []byte) (*auth.Rec, []byte, error) {
	return nil, nil, types.ErrUnsupported
}

// IsUnique for a noop. Anonymous login does not use secret, any secret is fine.
func (AnonAuth) IsUnique(secret []byte) (bool, error) {
	return true, nil
}

// GenSecret always fails.
func (AnonAuth) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	return nil, time.Time{}, types.ErrUnsupported
}

// DelRecords is a noop which always succeeds.
func (AnonAuth) DelRecords(uid types.Uid) error {
	return nil
}

func init() {
	store.RegisterAuthScheme("anonymous", &AnonAuth{})
}
