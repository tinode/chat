package types

import (
	"encoding/base64"
	"encoding/binary"

	sf "github.com/tinode/snowflake"
	"golang.org/x/crypto/xtea"
)

// UidGenerator holds snowflake and encryption paramenets.
// RethinkDB generates UUIDs as primary keys. Using snowflake-generated uint64 instead.
type UidGenerator struct {
	seq    *sf.SnowFlake
	cipher *xtea.Cipher
}

// Init initialises the Uid generator
func (ug *UidGenerator) Init(workerID uint, key []byte) error {
	var err error

	if ug.seq == nil {
		ug.seq, err = sf.NewSnowFlake(uint32(workerID))
	}
	if ug.cipher == nil {
		ug.cipher, err = xtea.NewCipher(key)
	}

	return err
}

// Get generates a unique weakly encryped id it so ids are random-looking.
func (ug *UidGenerator) Get() Uid {
	buf, err := getIDBuffer(ug)
	if err != nil {
		return ZeroUid
	}
	return Uid(binary.LittleEndian.Uint64(buf))
}

// GetStr generates a unique id then returns it as base64-encrypted string.
func (ug *UidGenerator) GetStr() string {
	buf, err := getIDBuffer(ug)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(buf)[:uid_BASE64_UNPADDED]
}

// getIdBuffer returns a byte array holding the Uid bytes
func getIDBuffer(ug *UidGenerator) ([]byte, error) {
	var id uint64
	var err error
	if id, err = ug.seq.Next(); err != nil {
		return nil, err
	}

	var src = make([]byte, 8)
	var dst = make([]byte, 8)
	binary.LittleEndian.PutUint64(src, id)
	ug.cipher.Encrypt(dst, src)

	return dst, nil
}
