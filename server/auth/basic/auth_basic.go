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
		err = types.ErrMalformed
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
func (BasicAuth) AddRecord(rec *auth.Rec, secret []byte) (auth.Level, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return auth.LevelNone, fail
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.LevelNone, err
	}
	var expires time.Time
	if rec.Lifetime > 0 {
		expires = time.Now().Add(rec.Lifetime).UTC().Round(time.Millisecond)
	}
	dup, err := store.Users.AddAuthRecord(rec.Uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if dup {
		return auth.LevelNone, types.ErrDuplicate
	} else if err != nil {
		return auth.LevelNone, err
	}
	return auth.LevelAuth, nil
}

// UpdateRecord updates password for basic authentication.
func (BasicAuth) UpdateRecord(rec *auth.Rec, secret []byte) error {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return fail
	}

	storedUID, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return err
	}
	if storedUID != rec.Uid {
		return types.ErrFailed
	}
	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return types.ErrInternal
	}
	var expires time.Time
	if rec.Lifetime > 0 {
		expires = time.Now().Add(rec.Lifetime)
	}
	_, err = store.Users.UpdateAuthRecord(rec.Uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if err != nil {
		return err
	}
	return nil
}

// Authenticate checks login and password.
func (BasicAuth) Authenticate(secret []byte) (*auth.Rec, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != nil {
		return nil, fail
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return nil, err
	} else if uid.IsZero() {
		// Invalid login.
		return nil, types.ErrFailed
	} else if !expires.IsZero() && expires.Before(time.Now()) {
		// The record has expired
		return nil, types.ErrExpired
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		// Invalid password
		return nil, types.ErrFailed
	}

	var lifetime time.Duration
	if !expires.IsZero() {
		lifetime = time.Until(expires)
	}
	return &auth.Rec{
		Uid:       uid,
		AuthLevel: authLvl,
		Lifetime:  lifetime,
		Features:  0}, nil
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
	return false, types.ErrDuplicate
}

// GenSecret is not supported, generates an error.
func (BasicAuth) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	return nil, time.Time{}, types.ErrUnsupported
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
