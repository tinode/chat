package auth

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

const (
	// No error
	NoErr = iota
	// DB or other internal failure
	ErrInternal
	// The secret cannot be parsed or otherwise wrong
	ErrMalformed
	// Authentication failed (wrong password)
	ErrFailed
	// Duplicate credential
	ErrDuplicate
	// The operation is unsupported
	ErrUnsupported
	// Secret has expired
	ErrExpired
)

// Interface which auth providers must implement
type AuthHandler interface {
	// Initialize the handler
	Init(jsonconf string) error

	// Add persistent record to database. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.AddAuthRecord("scheme", "unique", "secret")
	AddRecord(uid types.Uid, secret []byte, expires time.Time) (int, error)

	// Update existing record with new credentials. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.UpdateAuthRecord("scheme", "unique", "secret")
	UpdateRecord(uid types.Uid, secret []byte, expires time.Time) (int, error)

	// Given a user-provided authentication secret (such as "login:password"
	// return user ID, time when the secret expires (zero, if never) or an error code.
	// store.Users.GetAuthRecord("scheme", "unique")
	Authenticate(secret []byte) (types.Uid, time.Time, int)

	// Verify if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique.
	// store.GetAuthRecord(scheme, unique)
	IsUnique(secret []byte) (bool, error)

	// Generate a new secret, if appropriate.
	GenSecret(uid types.Uid, expires time.Time) ([]byte, error)
}
