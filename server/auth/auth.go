package auth

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

type Level int

// Authentication levels.
const (
	// LevelNone is undefined/not authenticated
	LevelNone Level = iota * 10
	// LevelAnon is anonymous user/light authentication
	LevelAnon
	// LevelAuth is fully authenticated user
	LevelAuth
	// LevelRoot is a superuser (currently unused)
	LevelRoot
)

// String implements Stringer interface: gets human-readable name for a numeric authentication level.
func (a Level) String() string {
	switch a {
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

func ParseAuthLevel(name string) Level {
	switch name {
	case "anon":
		return LevelAnon
	case "auth":
		return LevelAuth
	case "root":
		return LevelRoot
	default:
		return LevelNone
	}
}

type Feature uint16

const (
	Validated Feature = 1 << iota
)

type Rec struct {
	Uid       types.Uid
	AuthLevel Level
	Lifetime  time.Duration
	Features  Feature
}

// AuthHandler is the interface which auth providers must implement.
type AuthHandler interface {
	// Init initialize the handler.
	Init(jsonconf string) error

	// AddRecord adds persistent record to database.
	// Calls store.Users.AddAuthRecord("user", "auth.Level", "scheme", "unique", "passhash", "expiration")
	// Returns: auth level, error
	AddRecord(rec *Rec, secret []byte) (Level, error)

	// UpdateRecord updates existing record with new credentials. Returns a numeric error code to indicate
	// if the error is due to a duplicate or some other error.
	// store.UpdateAuthRecord("scheme", "unique", "secret")
	UpdateRecord(rec *Rec, secret []byte) error

	// Authenticate: given a user-provided authentication secret (such as "login:password"
	// return user ID, time when the secret expires (zero, if never) or an error code.
	// store.Users.GetAuthRecord("scheme", "unique")
	// Returns: user ID, user auth level, login expiration time, error.
	Authenticate(secret []byte) (*Rec, error)

	// IsUnique verifies if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique.
	// store.GetAuthRecord(scheme, unique)
	IsUnique(secret []byte) (bool, error)

	// GenSecret generates a new secret, if appropriate.
	GenSecret(rec *Rec) ([]byte, time.Time, error)

	// DelRecords deletes all authentication records for the given user.
	DelRecords(uid types.Uid) error
}
