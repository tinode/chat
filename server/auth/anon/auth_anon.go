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
func (AnonAuth) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, error) {
	return auth.LevelAnon, nil
}

// UpdateRecord is a noop. Just report success.
func (AnonAuth) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) error {
	return nil
}

// Authenticate is not supported. It's used only at account creation time.
func (AnonAuth) Authenticate(secret []byte) (types.Uid, int, time.Time, error) {
	return types.ZeroUid, auth.LevelNone, time.Time{}, auth.ErrUnsupported
}

// IsUnique for a noop. Anonymous login does not use secret, any secret is fine.
func (AnonAuth) IsUnique(secret []byte) (bool, error) {
	return true, nil
}

// GenSecret always fails.
func (AnonAuth) GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, error) {
	return nil, time.Time{}, auth.ErrUnsupported
}

// DelRecords is a noop which always succeeds.
func (AnonAuth) DelRecords(uid types.Uid) error {
	return nil
}

func init() {
	store.RegisterAuthScheme("anonymous", &AnonAuth{})
}
