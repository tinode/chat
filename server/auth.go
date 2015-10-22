/******************************************************************************
 *
 *  Copyright (C) 2014 Tinode, All Rights Reserved
 *
 *  This program is free software; you can redistribute it and/or modify it
 *  under the terms of the GNU Affero General Public License as published by
 *  the Free Software Foundation; either version 3 of the License, or (at your
 *  option) any later version.
 *
 *  This program is distributed in the hope that it will be useful, but
 *  WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY
 *  or FITNESS FOR A PARTICULAR PURPOSE.
 *  See the GNU Affero General Public License for more details.
 *
 *  You should have received a copy of the GNU Affero General Public License
 *  along with this program; if not, see <http://www.gnu.org/licenses>.
 *
 *  This code is available under licenses for commercial use.
 *
 *  File        :  auth.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
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
	"crypto/rand"
	//	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
	"log"
	"time"
)

// 32 random bytes to be used for signing auth tokens
// FIXME(gene): move it to the database (make it unique per-application)
var hmac_salt = []byte{
	0x4f, 0xbd, 0x77, 0xfe, 0xb6, 0x18, 0x81, 0x6e,
	0xe0, 0xe2, 0x6d, 0xef, 0x1b, 0xac, 0xc6, 0x46,
	0x1e, 0xfe, 0x14, 0xcd, 0x6d, 0xd1, 0x3f, 0x23,
	0xd7, 0x79, 0x28, 0x5d, 0x27, 0x0e, 0x02, 0x3e}

// TODO(gene):change to use snowflake
// getRandomString generates 72 bits of randomness, returns 12 char-long random-looking string
func getRandomString() string {
	buf := make([]byte, 9)
	_, err := rand.Read(buf)
	if err != nil {
		panic("getRandomString: failed to generate a random string: " + err.Error())
	}
	//return base32.StdEncoding.EncodeToString(buf)
	return base64.URLEncoding.EncodeToString(buf)
}

func genTopicName() string {
	return "grp" + getRandomString()
}

// Generate HMAC hash from password
func passHash(password string) []byte {
	hasher := hmac.New(md5.New, hmac_salt)
	hasher.Write([]byte(password))
	return hasher.Sum(nil)
}

func isValidPass(password string, validMac []byte) bool {
	return hmac.Equal(validMac, passHash(password))
}

// Singned AppID. Composition:
//   [1:algorithm version][4:appid][2:key sequence][1:isRoot][16:signature] = 24 bytes
// convertible to base64 without padding
// All integers are little-endian

const (
	APIKEY_VERSION   = 1
	APIKEY_APPID     = 4
	APIKEY_SEQUENCE  = 2
	APIKEY_WHO       = 1
	APIKEY_SIGNATURE = 16
	APIKEY_LENGTH    = APIKEY_VERSION + APIKEY_APPID + APIKEY_SEQUENCE + APIKEY_WHO + APIKEY_SIGNATURE
)

// Client signature validation
//   key: client's secret key
// Returns application id, key type
func checkApiKey(apikey string) (appid uint32, isRoot bool) {

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

	hasher := hmac.New(md5.New, hmac_salt)
	hasher.Write(data[:APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO])
	check := hasher.Sum(nil)
	if !bytes.Equal(data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE+APIKEY_WHO:], check) {
		log.Println("invalid apikey signature")
		return
	}

	appid = binary.LittleEndian.Uint32(data[APIKEY_VERSION : APIKEY_VERSION+APIKEY_APPID])
	isRoot = (data[APIKEY_VERSION+APIKEY_APPID+APIKEY_SEQUENCE] == 1)
	return
}

// TODO(gene): make UID variable length
// [2:type][8:UID][4:expires][16:signature] == 30 bytes
const (
	SEC_TOKEN_TYPE      = 2 //always zero for now
	SEC_TOKEN_UID       = 8
	SEC_TOKEN_EXPIRES   = 4
	SEC_TOKEN_SIGNATURE = 16
	SEC_TOKEN_LENGTH    = SEC_TOKEN_TYPE + SEC_TOKEN_UID + SEC_TOKEN_EXPIRES + SEC_TOKEN_SIGNATURE
)

// Make a temporary token to be used instead of login/password
func makeSecurityToken(uid types.Uid, expires time.Time) string {

	var buf = make([]byte, SEC_TOKEN_LENGTH)
	b, _ := uid.MarshalBinary()
	buf = append(buf[SEC_TOKEN_TYPE:], b...)
	binary.LittleEndian.PutUint32(buf[SEC_TOKEN_TYPE+SEC_TOKEN_UID:], uint32(expires.Unix()))

	hasher := hmac.New(md5.New, hmac_salt)
	hasher.Write(buf[:SEC_TOKEN_TYPE+SEC_TOKEN_UID+SEC_TOKEN_EXPIRES])

	return base64.URLEncoding.EncodeToString(hasher.Sum(buf[SEC_TOKEN_TYPE+SEC_TOKEN_UID+SEC_TOKEN_EXPIRES:]))
}

func checkSecurityToken(token string) (uid types.Uid, expires time.Time) {

	if declen := base64.URLEncoding.DecodedLen(len(token)); declen != SEC_TOKEN_LENGTH {
		return
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		log.Println("failed to decode.base64 sectoken ", err)
		return
	}
	hasher := hmac.New(md5.New, hmac_salt)
	hasher.Write(data[:SEC_TOKEN_TYPE+SEC_TOKEN_UID+SEC_TOKEN_EXPIRES])
	check := hasher.Sum(nil)
	if !hmac.Equal(data[SEC_TOKEN_TYPE+SEC_TOKEN_UID+SEC_TOKEN_EXPIRES:], check) {
		log.Println("invalid sectoken signature")
		return
	}

	expires = time.Unix(int64(binary.LittleEndian.Uint32(
		data[SEC_TOKEN_TYPE+SEC_TOKEN_UID:(SEC_TOKEN_TYPE+SEC_TOKEN_UID+SEC_TOKEN_EXPIRES)])), 0).UTC()
	if expires.Before(time.Now()) {
		log.Println("expired sectoken")
		return
	}

	return store.UidFromBytes(data[SEC_TOKEN_TYPE:SEC_TOKEN_UID]), expires
}

// Check API key for origin, and revocation. The key must be valid
//   origin: browser-provided Origin URL
//   dbcheck: if true, validate key & origin by calling the database (should do one on the first connection)
func authClient(appid int32, apikey, origin string) error {
	// TODO(gene): validate key with database
	// data, err := base64.URLEncoding.DecodeString(apikey)
	// var clientid = binary.LittleEndian.Uint16(data[5:7])
	// var version = binary.LittleEndian.Uint16(data[7:9])

	return nil
}
