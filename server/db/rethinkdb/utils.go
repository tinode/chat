package rethinkdb

import (
	"encoding/base64"
	"encoding/binary"
	t "github.com/tinode/chat/server/store/types"
	sf "github.com/tinode/snowflake"
	"golang.org/x/crypto/xtea"
)

// RethinkDB generates UUIDs as primary keys. Using snowflake-generated uint64 instead.
type uidGenerator struct {
	seq    *sf.SnowFlake
	cipher *xtea.Cipher
}

// Init initialises the Uid generator
func (uid *uidGenerator) Init(workerId uint, key []byte) error {
	var err error

	if uid.seq == nil {
		uid.seq, err = sf.NewSnowFlake(uint32(workerId))
	}
	if uid.cipher == nil {
		uid.cipher, err = xtea.NewCipher(key)
	}

	return err
}

// Get generates a unique weakly encryped id it so ids are random-looking.
func (uid *uidGenerator) Get() t.Uid {
	buf, err := getIdBuffer(uid)
	if err != nil {
		return t.ZeroUid
	}
	return t.Uid(binary.LittleEndian.Uint64(buf))
}

// 8 bytes of data are always encoded as 11 bytes of base64 + 1 byte of padding.
const (
	BASE64_PADDED   = 12
	BASE64_UNPADDED = 11
)

// GetStr generates a unique id then returns it as base64-encrypted string.
func (uid *uidGenerator) GetStr() string {
	buf, err := getIdBuffer(uid)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(buf)[:BASE64_UNPADDED]
}

// getIdBuffer returns a byte array holding the Uid bytes
func getIdBuffer(uid *uidGenerator) ([]byte, error) {
	var id uint64
	var err error
	if id, err = uid.seq.Next(); err != nil {
		return nil, err
	}

	var src = make([]byte, 8)
	var dst = make([]byte, 8)
	binary.LittleEndian.PutUint64(src, id)
	uid.cipher.Encrypt(dst, src)

	return dst, nil
}
