package auth

import (
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
)

// Interface which auth providers must implement
type AuthHandler interface {
	// Add persistent record to database. Returns an error and a bool
	// to indicate if the error is due to a duplicate (true) or some other error.
	// store.AddAuthRecord("scheme", "unique", "secret")
	AddRecord(uid types.Uid, secret string) (int, error)

	// Given a user-provided authentication secret (such as "login:password"
	// return user ID or error
	// store.Users.GetAuthRecord("scheme", "unique")
	Authenticate(secret string) (types.Uid, int, error)

	// Verify if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique.
	// store.GetAuthRecord(scheme, unique)
	IsUnique(secret string) (bool, error)
}
