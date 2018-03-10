package auth

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

// AuthErr is a structure for reporting an error condition.
type AuthErr string

func (e AuthErr) Error() string {
	return string(e)
}

const (
	// ErrInternal means DB or other internal failure
	ErrInternal = AuthErr("internal")
	// ErrMalformed means the secret cannot be parsed or otherwise wrong
	ErrMalformed = AuthErr("malformed")
	// ErrFailed means authentication failed (wrong login or password, etc)
	ErrFailed = AuthErr("failed")
	// ErrDuplicate means duplicate credential, i.e. non-unique login
	ErrDuplicate = AuthErr("duplicate")
	// ErrUnsupported means an operation is not supported
	ErrUnsupported = AuthErr("unsupported")
	// ErrExpired means the secret has expired
	ErrExpired = AuthErr("expired")
	// ErrPolicy means policy violation, e.g. password too weak.
	ErrPolicy = AuthErr("policy")
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

// AuthLevelName gets human-readable name for a numeric authentication level.
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

// AuthHandler is the interface which auth providers must implement.
type AuthHandler interface {
	// Init initialize the handler.
	Init(jsonconf string) error

	// AddRecord adds persistent record to database. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.AddAuthRecord("scheme", "unique", "secret")
	// Returns: auth level, error
	AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, error)

	// UpdateRecord updates existing record with new credentials. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.UpdateAuthRecord("scheme", "unique", "secret")
	UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) error

	// Authenticate: given a user-provided authentication secret (such as "login:password"
	// return user ID, time when the secret expires (zero, if never) or an error code.
	// store.Users.GetAuthRecord("scheme", "unique")
	// Returns: user ID, user auth level, token expiration time, AuthErr.
	Authenticate(secret []byte) (types.Uid, int, time.Time, error)

	// IsUnique verifies if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique.
	// store.GetAuthRecord(scheme, unique)
	IsUnique(secret []byte) (bool, error)

	// GenSecret generates a new secret, if appropriate.
	GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, error)

	// DelRecords deletes all authentication records for the given user.
	DelRecords(uid types.Uid) error
}
