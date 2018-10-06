package basic

// This handler must be kept in a separate package because it's referenced by
// tinode-db

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/crypto/bcrypt"
)

const (
	// Define constraints on login
	minLoginLength = 1
	maxLoginLength = 32
)

// BasicAuth is the type to map authentication methods to.
type authenticator struct {
	addToTags bool
}

func parseSecret(bsecret []byte) (uname, password string, err error) {
	secret := string(bsecret)

	splitAt := strings.Index(secret, ":")
	if splitAt < 0 {
		err = types.ErrMalformed
		return
	}

	uname = strings.ToLower(secret[:splitAt])
	password = secret[splitAt+1:]
	return
}

// Init initializes the basic authenticator.
func (a *authenticator) Init(jsonconf string) error {
	type configType struct {
		//
		AddToTags bool `json:"add_to_tags"`
	}
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("auth_basic: failed to parse config: " + err.Error() + "(" + jsonconf + ")")
	}
	a.addToTags = config.AddToTags
	return nil
}

// AddRecord adds a basic authentication record to DB.
func (a *authenticator) AddRecord(rec *auth.Rec, secret []byte) (*auth.Rec, error) {
	uname, password, err := parseSecret(secret)
	if err != nil {
		return nil, err
	}

	if len([]rune(uname)) < minLoginLength || len([]rune(uname)) > maxLoginLength {
		return nil, types.ErrPolicy
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	var expires time.Time
	if rec.Lifetime > 0 {
		expires = time.Now().Add(rec.Lifetime).UTC().Round(time.Millisecond)
	}

	authLevel := rec.AuthLevel
	if authLevel == auth.LevelNone {
		authLevel = auth.LevelAuth
	}

	dup, err := store.Users.AddAuthRecord(rec.Uid, authLevel, "basic", uname, passhash, expires)
	if dup {
		return nil, types.ErrDuplicate
	} else if err != nil {
		return nil, err
	}

	rec.AuthLevel = authLevel
	if a.addToTags {
		rec.Tags = []string{"basic:" + uname}
	}
	return rec, nil
}

// UpdateRecord updates password for basic authentication.
func (authenticator) UpdateRecord(rec *auth.Rec, secret []byte) error {
	uname, password, err := parseSecret(secret)
	if err != nil {
		return err
	}

	login, _, _, _, err := store.Users.GetAuthRecord(rec.Uid, "basic")
	if err != nil {
		return err
	}
	// User does not have a record.
	if login == "" {
		return types.ErrNotFound
	}
	if uname == "" {
		// User is changing just the password.
		uname = login
	}
	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return types.ErrInternal
	}
	var expires time.Time
	if rec.Lifetime > 0 {
		expires = types.TimeNow().Add(rec.Lifetime)
	}
	_, err = store.Users.UpdateAuthRecord(rec.Uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if err != nil {
		return err
	}
	return nil
}

// Authenticate checks login and password.
func (authenticator) Authenticate(secret []byte) (*auth.Rec, []byte, error) {
	uname, password, err := parseSecret(secret)
	if err != nil {
		return nil, nil, err
	}

	if len([]rune(uname)) < minLoginLength || len([]rune(uname)) > maxLoginLength {
		return nil, nil, types.ErrFailed
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthUniqueRecord("basic", uname)
	if err != nil {
		return nil, nil, err
	}
	if uid.IsZero() {
		// Invalid login.
		return nil, nil, types.ErrFailed
	}
	if !expires.IsZero() && expires.Before(time.Now()) {
		// The record has expired
		return nil, nil, types.ErrExpired
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		// Invalid password
		return nil, nil, types.ErrFailed
	}

	var lifetime time.Duration
	if !expires.IsZero() {
		lifetime = time.Until(expires)
	}
	return &auth.Rec{
		Uid:       uid,
		AuthLevel: authLvl,
		Lifetime:  lifetime,
		Features:  0}, nil, nil
}

// IsUnique checks login uniqueness.
func (authenticator) IsUnique(secret []byte) (bool, error) {
	uname, _, err := parseSecret(secret)
	if err != nil {
		return false, err
	}

	if len([]rune(uname)) < minLoginLength || len([]rune(uname)) > maxLoginLength {
		return false, types.ErrPolicy
	}

	uid, _, _, _, err := store.Users.GetAuthUniqueRecord("basic", uname)
	if err != nil {
		return false, err
	}

	if uid.IsZero() {
		return true, nil
	}
	return false, types.ErrDuplicate
}

// GenSecret is not supported, generates an error.
func (authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {
	return nil, time.Time{}, types.ErrUnsupported
}

func (authenticator) DelRecords(uid types.Uid) error {
	return store.Users.DelAuthRecords(uid, "basic")
}

func init() {
	store.RegisterAuthScheme("basic", &authenticator{})
}
