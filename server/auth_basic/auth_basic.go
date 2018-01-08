package auth_basic

// This handler must be kept in a separate package because it's referenced by
// tinode-db

import (
	"errors"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/crypto/bcrypt"
)

type BasicAuth struct{}

func parseSecret(secret string) (uname, password string, err int) {
	splitAt := strings.Index(secret, ":")
	if splitAt < 1 {
		err = auth.ErrMalformed
		return
	}

	err = auth.NoErr
	uname = strings.ToLower(secret[:splitAt])
	password = secret[splitAt+1:]

	return
}

func (BasicAuth) Init(unused string) error {
	return nil
}

func (BasicAuth) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, auth.AuthErr) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return auth.LevelNone, auth.NewErr(fail, errors.New("basic auth: malformed secret"))
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.LevelNone, auth.NewErr(auth.ErrInternal, err)
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime).UTC().Round(time.Millisecond)
	}
	err, dup := store.Users.AddAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if dup {
		return auth.LevelNone, auth.NewErr(auth.ErrDuplicate, err)
	} else if err != nil {
		return auth.LevelNone, auth.NewErr(auth.ErrInternal, err)
	}
	return auth.LevelAuth, auth.NewErr(auth.NoErr, nil)
}

func (BasicAuth) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) auth.AuthErr {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return auth.NewErr(fail, errors.New("basic auth: malformed secret"))
	}

	storedUid, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return auth.NewErr(auth.ErrInternal, err)
	}
	if storedUid != uid {
		return auth.NewErr(auth.ErrFailed,
			errors.New("basic auth: updating some else's credentials is not supported"))
	}
	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.NewErr(auth.ErrInternal, errors.New("basic auth: login cannot be changed"))
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime)
	}
	_, err = store.Users.UpdateAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if err != nil {
		return auth.NewErr(auth.ErrInternal, err)
	}
	return auth.NewErr(auth.NoErr, nil)
}

func (BasicAuth) Authenticate(secret []byte) (types.Uid, int, time.Time, auth.AuthErr) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return types.ZeroUid, auth.LevelNone, time.Time{},
			auth.NewErr(fail, errors.New("basic auth: malformed secret"))
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return types.ZeroUid, auth.LevelNone, time.Time{}, auth.NewErr(auth.ErrInternal, err)
	} else if uid.IsZero() {
		// Invalid login.
		return types.ZeroUid, auth.LevelNone, time.Time{},
			auth.NewErr(auth.ErrFailed, errors.New("basic auth: invalid login"))
	} else if !expires.IsZero() && expires.Before(time.Now()) {
		// The record has expired
		return types.ZeroUid, auth.LevelNone, time.Time{},
			auth.NewErr(auth.ErrExpired, errors.New("basic auth: expired record"))
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		// Invalid password
		return types.ZeroUid, auth.LevelNone, time.Time{},
			auth.NewErr(auth.ErrFailed, errors.New("basic auth: invalid password"))
	}
	return uid, authLvl, expires, auth.NewErr(auth.NoErr, nil)
}

func (BasicAuth) IsUnique(secret []byte) (bool, auth.AuthErr) {
	uname, _, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return false, auth.NewErr(fail, errors.New("basic auth: malformed secret"))
	}

	uid, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return false, auth.NewErr(auth.ErrInternal, err)
	}

	if uid.IsZero() {
		return true, auth.NewErr(auth.NoErr, nil)
	}
	return false, auth.NewErr(auth.ErrDuplicate, errors.New("basic auth: duplicate credentials"))
}

func (BasicAuth) GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, auth.AuthErr) {
	return nil, time.Time{}, auth.NewErr(auth.ErrUnsupported, errors.New("basic auth: GenSecret is not supported"))
}

func init() {
	var auth BasicAuth
	store.RegisterAuthScheme("basic", auth)
}
