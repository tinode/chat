package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
)

var hmacSalt = []byte{
	0x4f, 0xbd, 0x77, 0xfe, 0xb6, 0x18, 0x81, 0x6e,
	0xe0, 0xe2, 0x6d, 0xef, 0x1b, 0xac, 0xc6, 0x46,
	0x1e, 0xfe, 0x14, 0xcd, 0x6d, 0xd1, 0x3f, 0x23,
	0xd7, 0x79, 0x28, 0x5d, 0x27, 0x0e, 0x02, 0x3e}

// Generate API key
// Composition:
//  [1:algorithm version][4:deprecated (used to be expiration time)][2:key sequence][1:isRoot][16:signature] = 24 bytes
// convertible to base64 without padding
// All integers are little-endian
func main() {
	var version = flag.Int("sequence", 1, "Sequential number of the API key")
	var isRoot = flag.Int("isroot", 0, "Is this a root API key?")
	var apikey = flag.String("validate", "", "API key to validate")

	flag.Parse()

	if *apikey != "" {
		validate(*apikey)
	} else {
		generate(*version, *isRoot)
	}
}

const (
	// APIKEY_VERSION is algorithm version
	APIKEY_VERSION = 1
	// APIKEY_APPID is deprecated
	APIKEY_APPID = 4
	// APIKEY_SEQUENCE key serial number
	APIKEY_SEQUENCE = 2
	// APIKEY_WHO is a Root user designator
	APIKEY_WHO = 1
	// APIKEY_SIGNATURE is cryptographic signature
	APIKEY_SIGNATURE = 16
	// APIKEY_LENGTH is total length of the key
	APIKEY_LENGTH = APIKEY_VERSION + APIKEY_APPID + APIKEY_SEQUENCE + APIKEY_WHO + APIKEY_SIGNATURE
)

func generate(sequence, isRoot int) {

	var data [APIKEY_LENGTH]byte

	// [1:algorithm version][4:appid][2:key sequence][1:isRoot]
	data[0] = 1 // default algorithm
	// deprecated
	binary.LittleEndian.PutUint32(data[APIKEY_VERSION:], uint32(0))
	binary.LittleEndian.PutUint16(data[APIKEY_VERSION+APIKEY_APPID:], uint16(sequence))
	data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE] = uint8(isRoot)

	hasher := hmac.New(md5.New, hmacSalt)
	hasher.Write(data[:APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO])
	signature := hasher.Sum(nil)

	copy(data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO:], signature)

	var strIsRoot string
	if isRoot == 1 {
		strIsRoot = "ROOT"
	} else {
		strIsRoot = "ordinary"
	}

	fmt.Printf("API key v%d seq%d [%s]: %s\n", 1, sequence, strIsRoot,
		base64.URLEncoding.EncodeToString(data[:]))
}

func validate(apikey string) {
	var version uint8
	var deprecated uint32
	var sequence uint16
	var isRoot uint8

	var strIsRoot string

	if declen := base64.URLEncoding.DecodedLen(len(apikey)); declen != APIKEY_LENGTH {
		fmt.Printf("invalid key length %d, expecting %d", declen, APIKEY_LENGTH)
		return
	}

	data, err := base64.URLEncoding.DecodeString(apikey)
	if err != nil {
		fmt.Println("failed to decode key as base64", err)
		return
	}

	buf := bytes.NewReader(data)
	binary.Read(buf, binary.LittleEndian, &version)

	if version != 1 {
		fmt.Println("unknown signature algorithm ", data[0])
		return
	}

	hasher := hmac.New(md5.New, hmacSalt)
	hasher.Write(data[:APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO])
	signature := hasher.Sum(nil)

	if !bytes.Equal(data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO:], signature) {
		fmt.Println("invalid signature ", data, signature)
		return
	}
	// [1:algorithm version][4:deprecated][2:key sequence][1:isRoot]
	binary.Read(buf, binary.LittleEndian, &deprecated)
	binary.Read(buf, binary.LittleEndian, &sequence)
	binary.Read(buf, binary.LittleEndian, &isRoot)

	if isRoot == 1 {
		strIsRoot = "ROOT"
	} else {
		strIsRoot = "ordinary"
	}

	fmt.Printf("Valid v%d seq%d, [%s]\n", version, sequence, strIsRoot)
}
