package auth_basic

import (
	"errors"
	"log"
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

func (BasicAuth) AddRecord(uid types.Uid, secret string) (int, error) {
	uname, password, fail := parseSecret(secret)
	if fail != auth.NoErr {
		return fail, errors.New("basic auth handler: malformed secret")
	}

	passhash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return auth.ErrInternal, err
	}
	err, dup := store.Users.AddAuthRecord(uid, "basic", uname, passhash)
	if dup {
		return auth.ErrDuplicate, err
	} else if err != nil {
		return auth.ErrInternal, err
	}
	return auth.NoErr, nil
}

func (BasicAuth) Authenticate(secret string) (types.Uid, int, error) {
	uname, password, fail := parseSecret(secret)
	if fail != auth.NoErr {
		return types.ZeroUid, fail, nil
	}

	uid, passhash, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		log.Println(err)
		return types.ZeroUid, auth.ErrInternal, err
	} else if uid.IsZero() {
		// Invalid login.
		return types.ZeroUid, auth.ErrFailed, nil
	}

	err = bcrypt.CompareHashAndPassword([]byte(passhash), []byte(password))
	if err != nil {
		log.Println(err)
		// Invalid password
		return types.ZeroUid, auth.ErrFailed, nil
	}

	return uid, auth.NoErr, nil

}

func (BasicAuth) IsUnique(secret string) (bool, error) {
	uname, _, fail := parseSecret(secret)
	if fail != auth.NoErr {
		return false, errors.New("auth_basic.IsUnique: malformed secret")
	}

	uid, _, err := store.Users.GetAuthRecord("basic", uname)
	if err != nil {
		return false, err
	}

	return !uid.IsZero(), nil
}

func (BasicAuth) GenSecret(uid types.Uid, expires time.Time) (string, error) {
	return "", errors.New("auth_basic.GenSecret: not supported")
}

func init() {
	var auth BasicAuth
	store.RegisterAuthScheme("basic", auth)
}
