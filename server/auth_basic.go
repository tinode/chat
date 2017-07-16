package main

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

func (BasicAuth) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, int, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return auth.LevelNone, fail, errors.New("basic auth handler: malformed secret")
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.LevelNone, auth.ErrInternal, err
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime)
	}
	err, dup := store.Users.AddAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if dup {
		return auth.LevelNone, auth.ErrDuplicate, err
	} else if err != nil {
		return auth.LevelNone, auth.ErrInternal, err
	}
	return auth.LevelAuth, auth.NoErr, nil
}

func (BasicAuth) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) (int, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return fail, errors.New("basic auth handler: malformed secret")
	}

	storedUid, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return auth.ErrInternal, err
	}
	if storedUid != uid {
		return auth.ErrFailed, nil
	}
	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.ErrInternal, errors.New("basic auth handler: login cannot be changed")
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime)
	}
	_, err = store.Users.UpdateAuthRecord(uid, auth.LevelAuth, "basic", uname, passhash, expires)
	if err != nil {
		return auth.ErrInternal, err
	}
	return auth.NoErr, nil
}

func (BasicAuth) Authenticate(secret []byte) (types.Uid, int, time.Time, int) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return types.ZeroUid, auth.LevelNone, time.Time{}, fail
	}

	uid, authLvl, passhash, expires, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return types.ZeroUid, auth.LevelNone, time.Time{}, auth.ErrInternal
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
	return uid, authLvl, expires, auth.NoErr
}

func (BasicAuth) IsUnique(secret []byte) (bool, error) {
	uname, _, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return false, errors.New("auth_basic.IsUnique: malformed secret")
	}

	uid, _, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return false, err
	}

	return uid.IsZero(), nil
}

func (BasicAuth) GenSecret(uid types.Uid, authLvl int, lifetime time.Duration) ([]byte, time.Time, int) {
	return nil, time.Time{}, auth.ErrUnsupported
}

func init() {
	var auth BasicAuth
	store.RegisterAuthScheme("basic", auth)
}
