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

	"github.com/tinode/chat/server/logs"
)

// Singned AppID. Composition:
//
//	[1:algorithm version][4:appid][2:key sequence][1:isRoot][16:signature] = 24 bytes
//
// convertible to base64 without padding. All integers are little-endian.
// Definitions for byte lengths of key's parts.
const (
	// apikeyVersion is the version of this API scheme.
	apikeyVersion = 1
	// apikeyAppID is deprecated and will be removed in the future.
	apikeyAppID = 4
	// apikeySequence is the serial number of the key.
	apikeySequence = 2
	// apikeyWho indicates if the key grants root privileges.
	apikeyWho = 1
	// apikeySignature is key's cryptographic (HMAC) signature.
	apikeySignature = 16
	// apikeyLength is the length of the key in bytes.
	apikeyLength = apikeyVersion + apikeyAppID + apikeySequence + apikeyWho + apikeySignature
)

// Client signature validation
//
//	key: client's secret key
//
// Returns application id, key type.
func checkAPIKey(apikey string) (isValid, isRoot bool) {
	if declen := base64.URLEncoding.DecodedLen(len(apikey)); declen != apikeyLength {
		return
	}

	data, err := base64.URLEncoding.DecodeString(apikey)
	if err != nil {
		logs.Warn.Println("failed to decode.base64 appid ", err)
		return
	}
	if data[0] != 1 {
		logs.Warn.Println("unknown appid signature algorithm ", data[0])
		return
	}

	hasher := hmac.New(md5.New, globals.apiKeySalt)
	hasher.Write(data[:apikeyVersion+apikeyAppID+apikeySequence+apikeyWho])
	check := hasher.Sum(nil)
	if !bytes.Equal(data[apikeyVersion+apikeyAppID+apikeySequence+apikeyWho:], check) {
		logs.Warn.Println("invalid apikey signature")
		return
	}

	isRoot = (data[apikeyVersion+apikeyAppID+apikeySequence] == 1)

	isValid = true

	return
}
