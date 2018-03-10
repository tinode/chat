package basic

// This handler must be kept in a separate package because it's referenced by
// tinode-db

import (
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/crypto/bcrypt"
)

// BasicAuth is the type to map authentication methods to.
type BasicAuth struct{}

func parseSecret(secret string) (uname, password string, err error) {
	splitAt := strings.Index(secret, ":")
	if splitAt < 1 {
		err = auth.ErrMalformed
		return
	}

	uname = strings.ToLower(secret[:splitAt])
	password = secret[splitAt+1:]

	return
}

// Init initializes the basic authenticator.
func (BasicAuth) Init(unused string) error {
	return nil
}

// AddRecord adds a basic authentication record to DB.
func (BasicAuth) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return auth.LevelNone, fail
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.LevelNone, err
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime).UTC().Round(time.Millisecond)
	}
	dup, err := store.Users.AddAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if dup {
		return auth.LevelNone, auth.ErrDuplicate
	} else if err != nil {
		return auth.LevelNone, err
	}
	return auth.LevelAuth, nil
}

// UpdateRecord updates password for basic authentication.
func (BasicAuth) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) error {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return fail
	}

	storedUID, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return err
	}
	if storedUID != uid {
		return auth.ErrFailed
	}
	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.ErrInternal
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime)
	}
	_, err = store.Users.UpdateAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if err != nil {
		return err
	}
	return nil
}

// Authenticate checks login and password.
func (BasicAuth) Authenticate(secret []byte) (types.Uid, int, time.Time, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return types.ZeroUid, auth.LevelNone, time.Time{}, fail
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return types.ZeroUid, auth.LevelNone, time.Time{}, err
	} else if uid.IsZero() {
		// Invalid login.
		return types.ZeroUid, auth.LevelNone, time.Time{}, auth.ErrFailed
	} else if !expires.IsZero() && expires.Before(time.Now()) {
		// The record has expired
		return types.ZeroUid, auth.LevelNone, time.Time{}, auth.ErrExpired
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		// Invalid password
		return types.ZeroUid, auth.LevelNone, time.Time{}, auth.ErrFailed
	}
	return uid, authLvl, expires, nil
}

// IsUnique checks login uniqueness.
func (BasicAuth) IsUnique(secret []byte) (bool, error) {
	uname, _, fail := parseSecret(string(secret))
	if fail != nil {
		return false, fail
	}

	uid, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return false, err
	}

	if uid.IsZero() {
		return true, nil
	}
	return false, auth.ErrDuplicate
}

// GenSecret is not supported, generates an error.
func (BasicAuth) GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, error) {
	return nil, time.Time{}, auth.ErrUnsupported
}

func (BasicAuth) DelRecords(uid types.Uid) error {
	if err := store.Users.DelAuthRecords(uid, "basic"); err != nil {
		return err
	}
	return nil
}

func init() {
	store.RegisterAuthScheme("basic", &BasicAuth{})
}
