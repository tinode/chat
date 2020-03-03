// Package token implements authentication by HMAC-signed security token.
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
	name         string
	hmacSalt     []byte
	lifetime     time.Duration
	serialNumber int
}

// tokenLayout defines positioning of various bytes in token.
// [8:UID][4:expires][2:authLevel][2:serial-number][2:feature-bits][32:signature] = 50 bytes
type tokenLayout struct {
	// User ID.
	Uid uint64
	// Token expiration time.
	Expires uint32
	// User's authentication level.
	AuthLevel uint16
	// Serial number - to invalidate all tokens if needed.
	SerialNumber uint16
	// Bitmap with feature bits.
	Features uint16
}

// Init initializes the authenticator: parses the config and sets salt, serial number and lifetime.
func (ta *authenticator) Init(jsonconf json.RawMessage, name string) error {
	if ta.name != "" {
		return errors.New("auth_token: already initialized as " + ta.name + "; " + name)
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
	if err := json.Unmarshal(jsonconf, &config); err != nil {
		return errors.New("auth_token: failed to parse config: " + err.Error() + "(" + string(jsonconf) + ")")
	}

	if len(config.Key) < sha256.Size {
		return errors.New("auth_token: the key is missing or too short")
	}
	if config.ExpireIn <= 0 {
		return errors.New("auth_token: invalid expiration value")
	}

	ta.name = name
	ta.hmacSalt = config.Key
	ta.lifetime = time.Duration(config.ExpireIn) * time.Second
	ta.serialNumber = config.SerialNum

	return nil
}

// AddRecord is not supprted, will produce an error.
func (authenticator) AddRecord(rec *auth.Rec, secret []byte) (*auth.Rec, error) {
	return nil, types.ErrUnsupported
}

// UpdateRecord is not supported, will produce an error.
func (authenticator) UpdateRecord(rec *auth.Rec, secret []byte) (*auth.Rec, error) {
	return nil, types.ErrUnsupported
}

// Authenticate checks validity of provided token.
func (ta *authenticator) Authenticate(token []byte) (*auth.Rec, []byte, error) {
	var tl tokenLayout
	dataSize := binary.Size(&tl)
	if len(token) < dataSize+sha256.Size {
		// Token is too short
		return nil, nil, types.ErrMalformed
	}

	buf := bytes.NewBuffer(token)
	err := binary.Read(buf, binary.LittleEndian, &tl)
	if err != nil {
		return nil, nil, types.ErrMalformed
	}

	hbuf := new(bytes.Buffer)
	binary.Write(hbuf, binary.LittleEndian, &tl)

	// Check signature.
	hasher := hmac.New(sha256.New, ta.hmacSalt)
	hasher.Write(hbuf.Bytes())
	if !hmac.Equal(token[dataSize:dataSize+sha256.Size], hasher.Sum(nil)) {
		return nil, nil, types.ErrFailed
	}

	// Check authentication level for validity.
	if auth.Level(tl.AuthLevel) > auth.LevelRoot {
		return nil, nil, types.ErrMalformed
	}

	// Check serial number.
	if int(tl.SerialNumber) != ta.serialNumber {
		return nil, nil, types.ErrFailed
	}

	// Check token expiration time.
	expires := time.Unix(int64(tl.Expires), 0).UTC()
	if expires.Before(time.Now().Add(1 * time.Second)) {
		return nil, nil, types.ErrExpired
	}

	return &auth.Rec{
		Uid:       types.Uid(tl.Uid),
		AuthLevel: auth.Level(tl.AuthLevel),
		Lifetime:  time.Until(expires),
		Features:  auth.Feature(tl.Features),
		State:     types.StateUndefined}, nil, nil
}

// GenSecret generates a new token.
func (ta *authenticator) GenSecret(rec *auth.Rec) ([]byte, time.Time, error) {

	if rec.Lifetime == 0 {
		rec.Lifetime = ta.lifetime
	} else if rec.Lifetime < 0 {
		return nil, time.Time{}, types.ErrExpired
	}
	expires := time.Now().Add(rec.Lifetime).UTC().Round(time.Millisecond)

	tl := tokenLayout{
		Uid:          uint64(rec.Uid),
		Expires:      uint32(expires.Unix()),
		AuthLevel:    uint16(rec.AuthLevel),
		SerialNumber: uint16(ta.serialNumber),
		Features:     uint16(rec.Features),
	}
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, &tl)
	hasher := hmac.New(sha256.New, ta.hmacSalt)
	hasher.Write(buf.Bytes())
	binary.Write(buf, binary.LittleEndian, hasher.Sum(nil))

	return buf.Bytes(), expires, nil
}

// IsUnique is not supported, will produce an error.
func (authenticator) IsUnique(token []byte) (bool, error) {
	return false, types.ErrUnsupported
}

// DelRecords adds disabled user ID to a stop list.
func (authenticator) DelRecords(uid types.Uid) error {
	return nil
}

// RestrictedTags returns tag namespaces restricted by this authenticator (none for token).
func (authenticator) RestrictedTags() ([]string, error) {
	return nil, nil
}

// GetResetParams returns authenticator parameters passed to password reset handler
// (none for token).
func (authenticator) GetResetParams(uid types.Uid) (map[string]interface{}, error) {
	return nil, nil
}

func init() {
	store.RegisterAuthScheme("token", &authenticator{})
}
