package auth

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

const (
	// NoErr means successful completion
	NoErr = iota
	// InfoNotModified means no changes were made
	InfoNotModified
	// ErrInternal means DB or other internal failure
	ErrInternal
	// ErrMalformed means the secret cannot be parsed or otherwise wrong
	ErrMalformed
	// ErrFailed means authentication failed (wrong login or password, etc)
	ErrFailed
	// ErrDuplicate means duplicate credential, i.e. non-unique login
	ErrDuplicate
	// ErrUnsupported means an operation is not supported
	ErrUnsupported
	// ErrExpired means the secret has expired
	ErrExpired
	// ErrPolicy means policy violation, e.g. password too weak.
	ErrPolicy
)

// Authentication levels.
const (
	// LevelNone is undefined/not authenticated
	LevelNone = iota * 10
	// LevelAnon is anonymous user/light authentication
	LevelAnon
	// LevelAuth is fully authenticated user
	LevelAuth
	// LevelRoot is a superuser (currently unused)
	LevelRoot
)

// AuthErr is a structure for reporting an error condition.
type AuthErr struct {
	Code int
	Err  error
}

// NewErr creates a new AuthErr instance from components.
func NewErr(code int, err error) AuthErr {
	return AuthErr{Code: code, Err: err}
}

// IsError checks if the venue represents an actual error.
func (a AuthErr) IsError() bool {
	return a.Code <= InfoNotModified
}

// AuthHandler is the interface which auth providers must implement.
type AuthHandler interface {
	// Initialize the handler
	Init(jsonconf string) error

	// Add persistent record to database. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.AddAuthRecord("scheme", "unique", "secret")
	// Returns: auth level, error
	AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, AuthErr)

	// Update existing record with new credentials. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.UpdateAuthRecord("scheme", "unique", "secret")
	UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) AuthErr

	// Given a user-provided authentication secret (such as "login:password"
	// return user ID, time when the secret expires (zero, if never) or an error code.
	// store.Users.GetAuthRecord("scheme", "unique")
	// Returns: user ID, user auth level, token expiration time, AuthErr.
	Authenticate(secret []byte) (types.Uid, int, time.Time, AuthErr)

	// Verify if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique.
	// store.GetAuthRecord(scheme, unique)
	IsUnique(secret []byte) (bool, AuthErr)

	// Generate a new secret, if appropriate.
	GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, AuthErr)
}

// AuthLevelName gets human-readable name for a numeric uauthentication level.
func AuthLevelName(authLvl int) string {
	switch authLvl {
	case LevelNone:
		return ""
	case LevelAnon:
		return "anon"
	case LevelAuth:
		return "auth"
	case LevelRoot:
		return "root"
	default:
		return "unkn"
	}
}
