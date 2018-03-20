package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// authenticator is a singleton instance of the authenticator.
type authenticator struct {
	hmacSalt     []byte
	timeout      time.Duration
	serialNumber int
}

// Token composition: [8:UID][4:expires][2:authLevel][2:serial-number][32:signature] == 48 bytes
// Token markers
const (
	tokenUIDStart = 0
	tokenUIDEnd   = 8

	tokenExpiresStart = 8
	tokenExpiresEnd   = 12

	tokenAuthLvlStart = 12
	tokenAuthLvlEnd   = 14

	tokenSerialStart = 14
	tokenSerialEnd   = 16

	tokenSignatureStart = 16

	tokenLengthDecoded = 48

	tokenMinHmacLength = 32
)

// Init initializes the authenticator: parses the config and sets salt, serial number and lifetime.
func (ta *authenticator) Init(jsonconf string) error {
	if ta.hmacSalt != nil {
		return errors.New("auth_token: already initialized")
	}

	type configType struct {
		// Key for signing tokens
		Key []byte `json:"key"`
		// Datatabase or other serial number, to invalidate all issued tokens at once.
		SerialNum int `json:"serial_num"`
		// Token expiration time
		ExpireIn int `json:"expire_in"`
	}
	var config configType
	if err := json.Unmarshal([]byte(jsonconf), &config); err != nil {
		return errors.New("auth_token: failed to parse config: " + err.Error() + "(" + jsonconf + ")")
	}

	if config.Key == nil || len(config.Key) < tokenMinHmacLength {
		return errors.New("auth_token: the key is missing or too short")
	}
	if config.ExpireIn <= 0 {
		return errors.New("auth_token: invalid expiration value")
	}

	ta.hmacSalt = config.Key
	ta.timeout = time.Duration(config.ExpireIn) * time.Second

	ta.serialNumber = config.SerialNum

	return nil
}

// AddRecord is not supprted, will produce an error.
func (authenticator) AddRecord(uid types.Uid, secret []byte, lifetime time.Duration) (auth.Level, error) {
	return auth.LevelNone, types.ErrUnsupported
}

// UpdateRecord is not supported, will produce an error.
func (authenticator) UpdateRecord(uid types.Uid, secret []byte, lifetime time.Duration) error {
	return types.ErrUnsupported
}

// Authenticate checks validity of provided token.
func (ta *authenticator) Authenticate(token []byte) (types.Uid, auth.Level, time.Time, error) {
	// [8:UID][4:expires][2:authLevel][2:serial-number][32:signature] == 48 bytes

	if len(token) < tokenLengthDecoded {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrMalformed
	}

	var uid types.Uid
	if err := uid.UnmarshalBinary(token[tokenUIDStart:tokenUIDEnd]); err != nil {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrMalformed
	}
	var authLvl auth.Level
	if authLvl = auth.Level(binary.LittleEndian.Uint16(token[tokenAuthLvlStart:tokenAuthLvlEnd])); authLvl < 0 || authLvl > auth.LevelRoot {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrMalformed
	}

	if snum := int(binary.LittleEndian.Uint16(token[tokenSerialStart:tokenSerialEnd])); snum != ta.serialNumber {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrMalformed
	}

	hasher := hmac.New(sha256.New, ta.hmacSalt)
	hasher.Write(token[:tokenSignatureStart])
	if !hmac.Equal(token[tokenSignatureStart:], hasher.Sum(nil)) {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrFailed
	}

	expires := time.Unix(int64(binary.LittleEndian.Uint32(token[tokenExpiresStart:tokenExpiresEnd])), 0).UTC()
	if expires.Before(time.Now().Add(1 * time.Second)) {
		return types.ZeroUid, auth.LevelNone, time.Time{}, types.ErrExpired
	}

	return uid, authLvl, expires, nil
}

// GenSecret generates a new token.
func (ta *authenticator) GenSecret(uid types.Uid, authLvl auth.Level, lifetime time.Duration) ([]byte, time.Time, error) {
	// [8:UID][4:expires][2:authLevel][2:serial-number][32:signature] == 48 bytes

	buf := new(bytes.Buffer)
	uidbits, _ := uid.MarshalBinary()
	binary.Write(buf, binary.LittleEndian, uidbits)
	if lifetime == 0 {
		lifetime = ta.timeout
	} else if lifetime < 0 {
		return nil, time.Time{}, types.ErrExpired
	}
	expires := time.Now().Add(lifetime).UTC().Round(time.Millisecond)
	binary.Write(buf, binary.LittleEndian, uint32(expires.Unix()))
	binary.Write(buf, binary.LittleEndian, uint16(authLvl))
	binary.Write(buf, binary.LittleEndian, uint16(ta.serialNumber))

	hasher := hmac.New(sha256.New, ta.hmacSalt)
	hasher.Write(buf.Bytes())
	binary.Write(buf, binary.LittleEndian, hasher.Sum(nil))

	return buf.Bytes(), expires, nil
}

// IsUnique is not supported, will produce an error.
func (authenticator) IsUnique(token []byte) (bool, error) {
	return false, types.ErrUnsupported
}

// DelRecords is a noop which always succeeds.
func (authenticator) DelRecords(uid types.Uid) error {
	return nil
}

func init() {
	store.RegisterAuthScheme("token", &authenticator{})
}
