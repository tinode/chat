/******************************************************************************
 *
 *  Description :
 *
 *  Authentication
 *
 *****************************************************************************/

package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"log"
)

// Singned AppID. Composition:
//   [1:algorithm version][4:appid][2:key sequence][1:isRoot][16:signature] = 24 bytes
// convertible to base64 without padding. All integers are little-endian.
// Definitions for byte lengths of key's parts.
const (
	// APIKEY_VERSION is the version of this API scheme.
	APIKEY_VERSION = 1
	// APIKEY_APPID is deprecated and will be removed in the future.
	APIKEY_APPID = 4
	// APIKEY_SEQUENCE is the serial number of the key.
	APIKEY_SEQUENCE = 2
	// APIKEY_WHO indicates if the key grants root privileges
	APIKEY_WHO = 1
	// APIKEY_SIGNATURE is key's cryptographic (HMAC) signature
	APIKEY_SIGNATURE = 16
	// APIKEY_LENGTH is the length of the key in bytes
	APIKEY_LENGTH = APIKEY_VERSION + APIKEY_APPID + APIKEY_SEQUENCE + APIKEY_WHO + APIKEY_SIGNATURE
)

// Client signature validation
//   key: client's secret key
// Returns application id, key type
func checkAPIKey(apikey string) (isValid, isRoot bool) {

	if declen := base64.URLEncoding.DecodedLen(len(apikey)); declen != APIKEY_LENGTH {
		return
	}

	data, err := base64.URLEncoding.DecodeString(apikey)
	if err != nil {
		log.Println("failed to decode.base64 appid ", err)
		return
	}
	if data[0] != 1 {
		log.Println("unknown appid signature algorithm ", data[0])
		return
	}

	hasher := hmac.New(md5.New, globals.apiKeySalt)
	hasher.Write(data[:APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO])
	check := hasher.Sum(nil)
	if !bytes.Equal(data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO:], check) {
		log.Println("invalid apikey signature")
		return
	}

	isRoot = (data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE] == 1)

	isValid = true

	return
}
