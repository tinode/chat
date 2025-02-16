// Package auth provides interfaces and types required for implementing an authenticaor.
package auth

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/tinode/chat/server/store/types"
)

// Level is the type for authentication levels.
type Level int

// Authentication levels
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
	s, err := a.MarshalText()
	if err != nil {
		return "unkn"
	}
	return string(s)
}

// ParseAuthLevel parses authentication level from a string.
func ParseAuthLevel(name string) Level {
	switch name {
	case "anon", "ANON":
		return LevelAnon
	case "auth", "AUTH":
		return LevelAuth
	case "root", "ROOT":
		return LevelRoot
	default:
		return LevelNone
	}
}

// MarshalText converts Level to a slice of bytes with the name of the level.
func (a Level) MarshalText() ([]byte, error) {
	switch a {
	case LevelNone:
		return []byte(""), nil
	case LevelAnon:
		return []byte("anon"), nil
	case LevelAuth:
		return []byte("auth"), nil
	case LevelRoot:
		return []byte("root"), nil
	default:
		return nil, errors.New("auth.Level: invalid level value")
	}
}

// UnmarshalText parses authentication level from a string.
func (a *Level) UnmarshalText(b []byte) error {
	switch string(b) {
	case "":
		*a = LevelNone
		return nil
	case "anon", "ANON":
		*a = LevelAnon
		return nil
	case "auth", "AUTH":
		*a = LevelAuth
		return nil
	case "root", "ROOT":
		*a = LevelRoot
		return nil
	default:
		return errors.New("auth.Level: unrecognized")
	}
}

// MarshalJSON converts Level to a quoted string.
func (a Level) MarshalJSON() ([]byte, error) {
	res, err := a.MarshalText()
	if err != nil {
		return nil, err
	}

	return append(append([]byte{'"'}, res...), '"'), nil
}

// UnmarshalJSON reads Level from a quoted string.
func (a *Level) UnmarshalJSON(b []byte) error {
	if b[0] != '"' || b[len(b)-1] != '"' {
		return errors.New("syntax error")
	}

	return a.UnmarshalText(b[1 : len(b)-1])
}

// Feature is a bitmap of authenticated features, such as validated/not validated.
type Feature uint16

const (
	// FeatureValidated bit is set if user's credentials are already validated (V).
	FeatureValidated Feature = 1 << iota
	// FeatureNoLogin is set if the token should not be used to permanently authenticate a session (L).
	FeatureNoLogin
)

// MarshalText converts Feature to ASCII byte slice.
func (f Feature) MarshalText() ([]byte, error) {
	res := []byte{}
	for i, chr := range []byte{'V', 'L'} {
		if (f & (1 << uint(i))) != 0 {
			res = append(res, chr)
		}
	}
	return res, nil
}

// UnmarshalText parses Feature string as byte slice.
func (f *Feature) UnmarshalText(b []byte) error {
	var f0 int
	var err error
	if len(b) > 0 {
		if b[0] >= '0' && b[0] <= '9' {
			f0, err = strconv.Atoi(string(b))
		} else {
		Loop:
			for i := 0; i < len(b); i++ {
				switch b[i] {
				case 'V', 'v':
					f0 |= int(FeatureValidated)
				case 'L', 'l':
					f0 |= int(FeatureNoLogin)
				default:
					err = errors.New("Feature: invalid character '" + string(b[i]) + "'")
					break Loop
				}
			}
		}
	}

	*f = Feature(f0)

	return err
}

// String Featureto a string representation.
func (f Feature) String() string {
	res, err := f.MarshalText()
	if err != nil {
		return ""
	}
	return string(res)
}

// MarshalJSON converts Feature to a quoted string.
func (f Feature) MarshalJSON() ([]byte, error) {
	res, err := f.MarshalText()
	if err != nil {
		return nil, err
	}

	return append(append([]byte{'"'}, res...), '"'), nil
}

// UnmarshalJSON reads Feature from a quoted string or an integer.
func (f *Feature) UnmarshalJSON(b []byte) error {
	if b[0] == '"' && b[len(b)-1] == '"' {
		return f.UnmarshalText(b[1 : len(b)-1])
	}
	return f.UnmarshalText(b)
}

// Duration is identical to time.Duration except it can be sanely unmarshallend from JSON.
type Duration time.Duration

// UnmarshalJSON handles the cases where duration is specified in JSON as a "5000s" string or just plain seconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value) * time.Second)
		return nil
	case string:
		d0, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(d0)
		return nil
	default:
		return errors.New("invalid duration")
	}
}

// Rec is an authentication record.
type Rec struct {
	// User ID.
	Uid types.Uid `json:"uid,omitempty"`
	// Authentication level.
	AuthLevel Level `json:"authlvl,omitempty"`
	// Lifetime of this record.
	Lifetime Duration `json:"lifetime,omitempty"`
	// Bitmap of features. Currently 'validated'/'not validated' only.
	Features Feature `json:"features,omitempty"`
	// Tags generated by this authentication record.
	Tags []string `json:"tags,omitempty"`
	// User account state received or read by the authenticator.
	State types.ObjState
	// Credential 'method:value' associated with this record.
	Credential string `json:"cred,omitempty"`

	// Authenticator may request the server to create a new account.
	// These are the account parameters which can be used for creating the account.
	DefAcs  *types.DefaultAccess `json:"defacs,omitempty"`
	Public  interface{}          `json:"public,omitempty"`
	Private interface{}          `json:"private,omitempty"`
}

// AuthHandler is the interface which auth providers must implement.
type AuthHandler interface {
	// Init initializes the handler taking config string and logical name as parameters.
	Init(jsonconf json.RawMessage, name string) error

	// IsInitialized returns true if the handler is initialized.
	IsInitialized() bool

	// AddRecord adds persistent authentication record to the database.
	// Returns: updated auth record, error
	AddRecord(rec *Rec, secret []byte, remoteAddr string) (*Rec, error)

	// UpdateRecord updates existing record with new credentials.
	// Returns updated auth record, error.
	UpdateRecord(rec *Rec, secret []byte, remoteAddr string) (*Rec, error)

	// Authenticate: given a user-provided authentication secret (such as "login:password"), either
	// return user's record (ID, time when the secret expires, etc), or issue a challenge to
	// continue the authentication process to the next step, or return an error code.
	// The remoteAddr (i.e. the IP address of the client) can be used by custom authenticators for
	// additional validation. The stock authenticators don't use it.
	// store.Users.GetAuthRecord("scheme", "unique")
	// Returns: user auth record, challenge, error.
	Authenticate(secret []byte, remoteAddr string) (*Rec, []byte, error)

	// AsTag converts search token into prefixed tag or an empty string if it
	// cannot be represented as a prefixed tag.
	AsTag(token string) string

	// IsUnique verifies if the provided secret can be considered unique by the auth scheme
	// E.g. if login is unique. It also may check for policy compliance, i.e. not too short, etc.
	IsUnique(secret []byte, remoteAddr string) (bool, error)

	// GenSecret generates a new secret, if appropriate.
	GenSecret(rec *Rec) ([]byte, time.Time, error)

	// DelRecords deletes (or disables) all authentication records for the given user.
	DelRecords(uid types.Uid) error

	// RestrictedTags returns the tag namespaces (prefixes) which are restricted by this authenticator.
	RestrictedTags() ([]string, error)

	// GetResetParams returns authenticator parameters passed to password reset handler
	// for the provided user id.
	// Returns: map of params.
	GetResetParams(uid types.Uid) (map[string]interface{}, error)

	// GetRealName returns the hardcoded name of the authenticator.
	GetRealName() string
}
