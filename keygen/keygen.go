package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
)

// Generate API key
// Composition:
//  [1:algorithm version][4:deprecated (used to be application ID)][2:key sequence][1:isRoot][16:signature] = 24 bytes
// convertible to base64 without padding.
// All integers are little-endian.
func main() {
	version := flag.Int("sequence", 1, "Sequential number of the API key")
	isRoot := flag.Int("isroot", 0, "Is this a root API key?")
	apikey := flag.String("validate", "", "API key to validate")
	hmacSalt := flag.String("salt", "auto", "HMAC salt, 32 random bytes base64 encoded or 'auto' to generate salt")

	flag.Parse()

	if *apikey != "" {
		if *hmacSalt == "auto" {
			log.Println("Error: Cannot validate the key with 'auto' HMAC salt")
			os.Exit(1)
		}
		os.Exit(validate(*apikey, *hmacSalt))
	} else {
		os.Exit(generate(*version, *isRoot, *hmacSalt))
	}
}

const (
	// APIKEY_VERSION is algorithm version.
	APIKEY_VERSION = 1
	// APIKEY_APPID is deprecated.
	APIKEY_APPID = 4
	// APIKEY_SEQUENCE key serial number.
	APIKEY_SEQUENCE = 2
	// APIKEY_WHO is a Root user designator.
	APIKEY_WHO = 1
	// APIKEY_SIGNATURE is cryptographic signature.
	APIKEY_SIGNATURE = 16
	// APIKEY_LENGTH is total length of the key.
	APIKEY_LENGTH = APIKEY_VERSION + APIKEY_APPID + APIKEY_SEQUENCE + APIKEY_WHO + APIKEY_SIGNATURE
)

func generate(sequence, isRoot int, hmacSaltB64 string) int {
	var data [APIKEY_LENGTH]byte
	var hmacSalt []byte

	if hmacSaltB64 == "auto" {
		hmacSalt = make([]byte, 32)
		_, err := rand.Read(hmacSalt)
		if err != nil {
			log.Println("Error: Failed to generate HMAC salt", err)

			return 1
		}
	} else {
		var err error
		hmacSalt, err = base64.URLEncoding.DecodeString(hmacSaltB64)
		if err != nil {
			// Try standard base64 decoding
			hmacSalt, err = base64.StdEncoding.DecodeString(hmacSaltB64)
		}
		if err != nil {
			log.Println("Error: Failed to decode HMAC salt", err)

			return 1
		}
	}
	// Make sure the salt is base64std encoded: tinode.conf requires std encoding.
	hmacSaltB64 = base64.StdEncoding.EncodeToString(hmacSalt)

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

	fmt.Printf("API key v%d seq%d [%s]: %s\nUsed HMAC salt: %s\n", 1, sequence, strIsRoot,
		base64.URLEncoding.EncodeToString(data[:]), hmacSaltB64)

	return 0
}

func validate(apikey string, hmacSaltB64 string) int {
	var version uint8
	var deprecated uint32
	var sequence uint16
	var isRoot uint8

	var strIsRoot string

	hmacSalt, err := base64.URLEncoding.DecodeString(hmacSaltB64)
	if err != nil {
		// Try standard base64 decoding.
		hmacSalt, err = base64.StdEncoding.DecodeString(hmacSaltB64)
	}
	if err != nil {
		log.Println("Error: Failed to decode HMAC salt", err)

		return 1
	}

	if declen := base64.URLEncoding.DecodedLen(len(apikey)); declen != APIKEY_LENGTH {
		log.Printf("Error: Invalid key length %d, expecting %d", declen, APIKEY_LENGTH)

		return 1
	}

	data, err := base64.URLEncoding.DecodeString(apikey)
	if err != nil {
		log.Println("Error: Failed to decode key as base64-URL-encoded", err)

		return 1
	}

	buf := bytes.NewReader(data)
	binary.Read(buf, binary.LittleEndian, &version)

	if version != 1 {
		log.Println("Error: Unknown signature algorithm ", version)

		return 1
	}

	hasher := hmac.New(md5.New, hmacSalt)
	hasher.Write(data[:APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO])

	if signature := hasher.Sum(nil); !bytes.Equal(data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO:], signature) {
		log.Println("Error: Invalid signature ", data, signature)

		return 1
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

	return 0
}
