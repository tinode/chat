package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

type TokenAuth struct{}

const (
	token_len_decoded = 44
	min_key_length    = 32
)

var hmac_salt []byte
var token_timeout time.Duration

func (TokenAuth) Init(jsonconf string) error {
	if hmac_salt != nil {
		return errors.New("auth_token: already initialized")
	}

	type configType struct {
		Key     []byte `json:"key"`
		Timeout int    `json:"timeout"`
	}
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("auth_token: failed to parse config: " + err.Error() + "(" + jsonconf + ")")
	}

	if config.Key == nil || len(config.Key) < min_key_length {
		return errors.New("auth_token: the key is missing or too short")
	}
	if config.Timeout <= 0 {
		return errors.New("auth_token: invalid timeout")
	}

	hmac_salt = config.Key
	token_timeout = time.Duration(config.Timeout) * time.Second

	return nil
}

func (TokenAuth) AddRecord(uid types.Uid, secret string, expires time.Time) (int, error) {
	return auth.ErrUnsupported, nil
}

func (TokenAuth) Authenticate(token string) (types.Uid, time.Time, int) {
	var zeroTime time.Time
	// [8:UID][4:expires][32:signature] == 44 bytes

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return types.ZeroUid, zeroTime, auth.ErrMalformed
	}

	if len(data) != token_len_decoded {
		return types.ZeroUid, zeroTime, auth.ErrMalformed
	}

	var uid types.Uid
	if err := uid.UnmarshalBinary(data[0:8]); err != nil {
		return types.ZeroUid, zeroTime, auth.ErrMalformed
	}

	hasher := hmac.New(sha256.New, hmac_salt)
	hasher.Write(data[:12])
	if !hmac.Equal(data[12:], hasher.Sum(nil)) {
		return types.ZeroUid, zeroTime, auth.ErrFailed
	}

	expires := time.Unix(int64(binary.LittleEndian.Uint32(data[8:12])), 0).UTC()
	if expires.Before(time.Now()) {
		return types.ZeroUid, zeroTime, auth.ErrExpired
	}

	return uid, expires, auth.NoErr
}

func (TokenAuth) GenSecret(uid types.Uid, expires time.Time) (string, error) {
	// [8:UID][4:expires][32:signature] == 44 bytes

	buf := new(bytes.Buffer)
	uidbits, _ := uid.MarshalBinary()
	binary.Write(buf, binary.LittleEndian, uidbits)
	binary.Write(buf, binary.LittleEndian, uint32(expires.Unix()))
	hasher := hmac.New(sha256.New, hmac_salt)
	hasher.Write(buf.Bytes())
	binary.Write(buf, binary.LittleEndian, hasher.Sum(nil))

	return base64.URLEncoding.EncodeToString(buf.Bytes()), nil
}

func (TokenAuth) IsUnique(secret string) (bool, error) {
	return false, errors.New("auth_token: unsupported")
}

func init() {
	var auth TokenAuth
	store.RegisterAuthScheme("token", auth)
}
