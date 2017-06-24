package auth_basic

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

func (BasicAuth) AddRecord(uid types.Uid, secret []byte, expires time.Time) (int, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return fail, errors.New("basic auth handler: malformed secret")
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.ErrInternal, err
	}
	err, dup := store.Users.AddAuthRecord(uid, "basic", uname, passhash, expires)
	if dup {
		return auth.ErrDuplicate, err
	} else if err != nil {
		return auth.ErrInternal, err
	}
	return auth.NoErr, nil
}

func (BasicAuth) UpdateRecord(uid types.Uid, secret []byte, expires time.Time) (int, error) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return fail, errors.New("basic auth handler: malformed secret")
	}

	storedUid, _, _, err := store.Users.GetAuthRecord("basic", uname)
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
	_, err = store.Users.UpdateAuthRecord(uid, "basic", uname, passhash, expires)
	if err != nil {
		return auth.ErrInternal, err
	}
	return auth.NoErr, nil
}

func (BasicAuth) Authenticate(secret []byte) (types.Uid, time.Time, int) {
	uname, password, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return types.ZeroUid, time.Time{}, fail
	}

	uid, passhash, expires, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return types.ZeroUid, time.Time{}, auth.ErrInternal
	} else if uid.IsZero() {
		// Invalid login.
		return types.ZeroUid, time.Time{}, auth.ErrFailed
	} else if !expires.IsZero() && expires.Before(time.Now()) {
		// The record has expired
		return types.ZeroUid, time.Time{}, auth.ErrExpired
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		// Invalid password
		return types.ZeroUid, time.Time{}, auth.ErrFailed
	}
	return uid, expires, auth.NoErr
}

func (BasicAuth) IsUnique(secret []byte) (bool, error) {
	uname, _, fail := parseSecret(string(secret))
	if fail != auth.NoErr {
		return false, errors.New("auth_basic.IsUnique: malformed secret")
	}

	uid, _, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return false, err
	}

	return uid.IsZero(), nil
}

func (BasicAuth) GenSecret(uid types.Uid, expires time.Time) ([]byte, error) {
	return nil, errors.New("auth_basic.GenSecret: not supported")
}

func init() {
	var auth BasicAuth
	store.RegisterAuthScheme("basic", auth)
}
