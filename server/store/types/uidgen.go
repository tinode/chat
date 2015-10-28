package types

import (
	"encoding/base64"
	"encoding/binary"
	sf "github.com/tinode/snowflake"
	"golang.org/x/crypto/xtea"
)

// RethinkDB generates UUIDs as primary keys. Using snowflake-generated uint64 instead.
type UidGenerator struct {
	seq    *sf.SnowFlake
	cipher *xtea.Cipher
}

// Init initialises the Uid generator
func (uid *UidGenerator) Init(workerId uint, key []byte) error {
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
func (uid *UidGenerator) Get() Uid {
	buf, err := getIdBuffer(uid)
	if err != nil {
		return ZeroUid
	}
	return Uid(binary.LittleEndian.Uint64(buf))
}

// GetStr generates a unique id then returns it as base64-encrypted string.
func (uid *UidGenerator) GetStr() string {
	buf, err := getIdBuffer(uid)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(buf)[:uid_BASE64_UNPADDED]
}

// getIdBuffer returns a byte array holding the Uid bytes
func getIdBuffer(uid *UidGenerator) ([]byte, error) {
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
