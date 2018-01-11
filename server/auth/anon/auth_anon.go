package anon

// Anonymous authentication is used only at account creation time.

import (
	"errors"
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
func (AnonAuth) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, auth.AuthErr) {
	return auth.LevelAnon, auth.NewErr(auth.NoErr, nil)
}

// UpdateRecord is a noop. Just report success.
func (AnonAuth) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) auth.AuthErr {
	return auth.NewErr(auth.NoErr, nil)
}

// Authenticate is not supported. It's used only at account creation time.
func (AnonAuth) Authenticate(secret []byte) (types.Uid, int, time.Time, auth.AuthErr) {
	return types.ZeroUid, auth.LevelNone, time.Time{},
		auth.NewErr(auth.ErrUnsupported, errors.New("anon auth: Authenticate is not supported"))
}

// IsUnique for a noop. Anonymous login does not use secret, any secret is fine.
func (AnonAuth) IsUnique(secret []byte) (bool, auth.AuthErr) {
	return true, auth.NewErr(auth.NoErr, nil)
}

// GenSecret always fails.
func (AnonAuth) GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, auth.AuthErr) {
	return nil, time.Time{}, auth.NewErr(auth.ErrUnsupported, errors.New("anon auth: GenSecret is not supported"))
}

func init() {
	store.RegisterAuthScheme("anonymous", AnonAuth{})
}
