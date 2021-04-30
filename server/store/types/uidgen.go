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

// Get generates a unique weakly-encryped random-looking ID.
// The Uid is a unit64 with the highest bit possibly set which makes it
// incompatible with go's pre-1.9 sql package.
func (ug *UidGenerator) Get() Uid {
	buf, err := getIDBuffer(ug)
	if err != nil {
		return ZeroUid
	}
	return Uid(binary.LittleEndian.Uint64(buf))
}

// GetStr generates the same unique ID as Get then returns it as
// base64-encoded string. Slightly more efficient than calling Get()
// then base64-encoding the result.
func (ug *UidGenerator) GetStr() string {
	buf, err := getIDBuffer(ug)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(buf)
}

// getIdBuffer returns a byte array holding the Uid bytes
func getIDBuffer(ug *UidGenerator) ([]byte, error) {
	var id uint64
	var err error
	if id, err = ug.seq.Next(); err != nil {
		return nil, err
	}

	src := make([]byte, 8)
	dst := make([]byte, 8)
	binary.LittleEndian.PutUint64(src, id)
	ug.cipher.Encrypt(dst, src)

	return dst, nil
}

// DecodeUid takes an encrypted Uid and decrypts it into a non-negative int64.
// This is needed for go/sql compatibility where uint64 with high bit
// set is unsupported and possibly for other uses such as MySQL's recommendation
// for sequential primary keys.
func (ug *UidGenerator) DecodeUid(uid Uid) int64 {
	src := make([]byte, 8)
	dst := make([]byte, 8)
	binary.LittleEndian.PutUint64(src, uint64(uid))
	ug.cipher.Decrypt(dst, src)
	return int64(binary.LittleEndian.Uint64(dst))
}

// EncodeInt64 takes a positive int64 and encrypts it into a Uid.
// This is needed for go/sql compatibility where uint64 with high bit
// set is unsupported  and possibly for other uses such as MySQL's recommendation
// for sequential primary keys.
func (ug *UidGenerator) EncodeInt64(val int64) Uid {
	src := make([]byte, 8)
	dst := make([]byte, 8)
	binary.LittleEndian.PutUint64(src, uint64(val))
	ug.cipher.Encrypt(dst, src)
	return Uid(binary.LittleEndian.Uint64(dst))
}
