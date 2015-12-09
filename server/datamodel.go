package main

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
 *  File        :  datamodel.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
 *
 *  Description :
 *
 * Messaging structures
 *
 *****************************************************************************/

import (
	"net/http"
	"strings"
	"time"
)

type JsonDuration time.Duration

func (jd *JsonDuration) UnmarshalJSON(data []byte) (err error) {
	d, err := time.ParseDuration(strings.Trim(string(data), "\""))
	*jd = JsonDuration(d)
	return err
}

type MsgBrowseOpts struct {
	// Load messages with seq id equal or greater than this
	Since int `json:"since,omitempty"`
	// Load messages with seq id lower than this
	Before int `json:"before,omitempty"`
	// Limit the number of messages loaded
	Limit uint `json:"limit,omitempty"`
}

// Client to Server (C2S) messages

// User creation message {acc}
type MsgClientAcc struct {
	Id   string          `json:"id,omitempty"` // Message Id
	User string          `json:"user"`         // "new" to create a new user or UserId to update a user; default: current user
	Auth []MsgAuthScheme `json:"auth"`
	// User initialization data when creating a new user, otherwise ignored
	Init *MsgSetInfo `json:"init,omitempty"`
}

type MsgAuthScheme struct {
	// Scheme name
	Scheme string `json:"scheme"`
	Secret string `json:"secret"`
}

// Login {login} message
type MsgClientLogin struct {
	// Message Id
	Id string `json:"id,omitempty"`
	// User agent
	UserAgent string `json:"ua,omitempty"`
	// Authentication scheme
	Scheme string `jdon:"scheme,omitempty"`
	// Shared secret
	Secret string `json:"secret"`
}

// Subscription request {sub} message
type MsgClientSub struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`

	// Topic initialization data, !new topic & new subscriptions only, mirrors {set info}
	Init *MsgSetInfo `json:"init,omitempty"`
	// Subscription parameters, mirrors {set sub}; sub.User must not be provided
	Sub *MsgSetSub `json:"sub,omitempty"`

	// mirrors get.what: "data", "sub", "info", default: get nothing
	// space separated list; unknown strings are ignored
	Get string `json:"get,omitempty"`
	// parameter for requesting data from topic, mirrors get.browse
	Browse *MsgBrowseOpts `json:"browse,omitempty"`
}

const (
	constMsgMetaInfo = 1 << iota
	constMsgMetaSub
	constMsgMetaData
	constMsgMetaDelTopic
	constMsgMetaDelMsg
)

func parseMsgClientMeta(params string) int {
	var bits int
	parts := strings.SplitN(params, " ", 8)
	for _, p := range parts {
		switch p {
		case "info":
			bits |= constMsgMetaInfo
		case "sub":
			bits |= constMsgMetaSub
		case "data":
			bits |= constMsgMetaData
		default:
			// ignore
		}
	}
	return bits
}

// MsgSetInfo: C2S in set.what == "info" and sub.init message
type MsgSetInfo struct {
	DefaultAcs *MsgDefaultAcsMode `json:"defacs,omitempty"` // Access mode
	Public     interface{}        `json:"public,omitempty"`
	Private    interface{}        `json:"private,omitempty"` // Per-subscription private data
}

// MsgSetSub: payload in set.sub request to update current subscription or invite another user, {sub.what} == "sub"
type MsgSetSub struct {
	// User affected by this request. Default (empty): current user
	User string `json:"user,omitempty"`

	// Access mode change, either Given or Want depending on context
	Mode string `json:"mode,omitempty"`
	// Free-form payload to pass to the invited user or to topic manager
	Info interface{} `json:"info,omitempty"`
}

// Topic default access mode
type MsgDefaultAcsMode struct {
	Auth string `json:"auth,omitempty"`
	Anon string `json:"anon,omitempty"`
}

// Unsubscribe {leave} request message
type MsgClientLeave struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	Unsub bool   `json:unsub,omitempty`
}

// MsgClientPub is client's request to publish data to topic subscribers {pub}
type MsgClientPub struct {
	Id      string      `json:"id,omitempty"`
	Topic   string      `json:"topic"`
	Content interface{} `json:"content"`
}

//func (msg *MsgClientPub) GetBoolParam(name string) bool {
//	return modelGetBoolParam(msg.Params, name)
//}

// Query topic state {get}
type MsgClientGet struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	// "data", "sub" or "info" space separated list; unknown strings are ignored
	What string `json:"what"`
	// Parameters for "data" request
	Browse *MsgBrowseOpts `json:"browse,omitempty"`
}

// Update topic state {set}
type MsgClientSet struct {
	Id    string      `json:"id,omitempty"`
	Topic string      `json:"topic"`
	What  string      `json:"what"`           // sub, info, space separated list; unknown strings are ignored
	Info  *MsgSetInfo `json:"info,omitempty"` // Payload for What == "info"
	Sub   *MsgSetSub  `json:"sub,omitempty"`  // Payload for What == "sub"
}

// MsgClientDel delete messages or topic
type MsgClientDel struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	// what to delete, either "msg" to delete messages (default) or "topic" to delete the topic
	What string `json:"what"`
	// Delete messages older than this time stamp (inclusive)
	Before time.Time `json:"before"`
	// Request to hard-delete messages for all users, if such option is available.
	Hard bool `json:"hard,omitempty"`
}

// MsgClientNote is a client-generated notification for topic subscribers
type MsgClientNote struct {
	// There is no Id -- server will not akn {ping} packets, they are "fire and forget"
	Topic string `json:"topic"`
	// what is being reported: "recv" - message received, "read" - message read, "kp" - typing notification
	What string `json:"what"`
	// Server-issued message ID being reported
	SeqId int `json:"seq,omitempty"`
}

type ClientComMessage struct {
	Acc   *MsgClientAcc   `json:"acc"`
	Login *MsgClientLogin `json:"login"`
	Sub   *MsgClientSub   `json:"sub"`
	Leave *MsgClientLeave `json:"leave"`
	Pub   *MsgClientPub   `json:"pub"`
	Get   *MsgClientGet   `json:"get"`
	Set   *MsgClientSet   `json:"set"`
	Del   *MsgClientDel   `json:"del"`
	Note  *MsgClientNote  `json:"note"`

	// from: userid as string
	from      string
	timestamp time.Time
}

/////////////////////////////////////////////////////////////
// Server to client messages

type MsgLastSeenInfo struct {
	When      *time.Time `json:"when,omitempty"` // when the user was last seen
	UserAgent string     `json:"ua,omitempty"`   // user agent of the device used to access the topic
}

// Topic info, S2C in Meta message
type MsgTopicInfo struct {
	CreatedAt  *time.Time         `json:"created,omitempty"`
	UpdatedAt  *time.Time         `json:"updated,omitempty"`
	Name       string             `json:"name,omitempty"`
	DefaultAcs *MsgDefaultAcsMode `json:"defacs,omitempty"`
	Acs        *MsgAccessMode     `json:"acs,omitempty"` // Actual access mode
	SeqId      int                `json:"seq,omitempty"`
	ReadSeqId  int                `json:"read,omitempty"`
	RecvSeqId  int                `json:"recv,omitempty"`
	Public     interface{}        `json:"public,omitempty"`
	Private    interface{}        `json:"private,omitempty"` // Per-subscription private data
}

type MsgAccessMode struct {
	Want  string `json:"want,omitempty"`
	Given string `json:"given,omitempty"`
}

// MsgTopicSub: topic subscription details, sent in Meta message
type MsgTopicSub struct {
	Topic     string     `json:"topic,omitempty"`
	User      string     `json:"user,omitempty"`
	UpdatedAt *time.Time `json:"updated,omitempty"`
	Online    string     `json:"online,omitempty"`

	// cumulative access mode (mode.Want & mode.Given)
	AcsMode string `json:"mode"`
	// ID of the message reported by the client as read
	ReadSeqId int `json:"read,omitempty"`
	// ID of the message reported by the client as received
	RecvSeqId int `json:"recv,omitempty"`
	// Topic's public data
	Public  interface{} `json:"public,omitempty"`
	Private interface{} `json:"private,omitempty"`

	// All following makes sence only in context of getting user's subscriptions

	// ID of the last {data} message in a topic
	SeqId int `json:"seq,omitempty"`
	// P2P topics only
	// ID of the other user
	With string `json:"with,omitempty"`
	// Other user's last online timestamp & user agent
	LastSeen *MsgLastSeenInfo `json:"seen,omitempty"`
}

type MsgServerCtrl struct {
	Id     string      `json:"id,omitempty"`
	Topic  string      `json:"topic,omitempty"`
	Params interface{} `json:"params,omitempty"`

	Code      int       `json:"code"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"ts"`
}

// Invitation to a topic, sent as MsgServerData.Content
type MsgInvitation struct {
	// Topic that user wants to subscribe to or is invited to
	Topic string `json:"topic"`
	// User being subscribed
	User string `json:"user"`
	// Type of this invite - InvJoin, InvAppr
	Action string `json:"act"`
	// Current state of the access mode
	Acs MsgAccessMode `json:"acs,omitempty"`
	// Free-form payload
	Info interface{} `json:"info,omitempty"`
}

type MsgServerData struct {
	Topic string `json:"topic"`
	// ID of the user who originated the message as {pub}, could be empty if sent by the system
	From      string      `json:"from,omitempty"`
	Timestamp time.Time   `json:"ts"`
	SeqId     int         `json:"seq"`
	Content   interface{} `json:"content"`
}

type MsgServerPres struct {
	Topic     string `json:"topic"`
	Src       string `json:"src"`
	What      string `json:"what"`
	UserAgent string `json:"ua,omitempty"`
	SeqId     int    `json:"seq,omitempty"`
	// unroutable, to break the reply loop
	wantReply bool
}

type MsgServerMeta struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`

	Timestamp *time.Time `json:"ts,omitempty"`

	Info *MsgTopicInfo `json:"info,omitempty"` // Topic description
	Sub  []MsgTopicSub `json:"sub,omitempty"`  // Subscriptions as an array of objects
}

// MsgServerInfo is the server-side copy of MsgClientNote with From added
type MsgServerInfo struct {
	Topic string `json:"topic"`
	// ID of the user who originated the message
	From string `json:"from"`
	// what is being reported: "rcpt" - message received, "read" - message read, "kp" - typing notification
	What string `json:"what"`
	// Server-issued message ID being reported
	SeqId int `json:"seq,omitempty"`
}

type ServerComMessage struct {
	Ctrl *MsgServerCtrl `json:"ctrl,omitempty"`
	Data *MsgServerData `json:"data,omitempty"`
	Meta *MsgServerMeta `json:"meta,omitempty"`
	Pres *MsgServerPres `json:"pres,omitempty"`
	Info *MsgServerInfo `json:"info,omitempty"`

	// to: topic
	rcptto string
	// originating session, copy of Session.send
	akn chan<- []byte
	// origin-specific id to use in {ctrl} aknowledgements
	id string
	// timestamp for consistency of timestamps in {ctrl} messages
	timestamp time.Time
	// Should the packet be sent to the original sessions?
	skipSession *Session
}

// Combined message
type ComMessage struct {
	*ClientComMessage
	*ServerComMessage
}

/*
func modelGetBoolParam(params map[string]interface{}, name string) bool {
	var val bool
	if params != nil {
		if param, ok := params[name]; ok {
			switch param.(type) {
			case bool:
				val = param.(bool)
			case float64:
				val = (param.(float64) != 0.0)
			}
		}
	}

	return val
}

func modelGetInt64Param(params map[string]interface{}, name string) int64 {
	var val int64
	if params != nil {
		if param, ok := params[name]; ok {
			switch param.(type) {
			case int8, int16, int32, int64, int:
				val = reflect.ValueOf(param).Int()
			case float32, float64:
				val = int64(reflect.ValueOf(param).Float())
			}
		}
	}

	return val
}
*/

// Generators of error messages

func NoErr(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusOK, // 200
		Text:      "ok",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func NoErrCreated(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusCreated, // 201
		Text:      "created",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func NoErrAccepted(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusAccepted, // 202
		Text:      "message accepted for delivery",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func NoErrEvicted(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusResetContent, // 205
		Text:      "evicted from topic",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func NoErrShutdown(ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Code:      http.StatusResetContent, // 205
		Text:      "server shutdown",
		Timestamp: ts}}
	return msg
}

// 3xx
func InfoAlreadySubscribed(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "already subscribed",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func InfoNotJoined(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "not joined",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

// 4xx Errors
func ErrMalformed(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusBadRequest, // 400
		Text:      "malformed message",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrAuthRequired(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "authentication required",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrAuthFailed(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "authentication failed",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrAuthUnknownScheme(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "unknown or missing authentication scheme",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrPermissionDenied(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusForbidden, // 403
		Text:      "access denied",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrTopicNotFound(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotFound,
		Text:      "topic not found", // 404
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrUserNotFound(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotFound, // 404
		Text:      "user not found or offline",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrAlreadyAuthenticated(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "already authenticated",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrDuplicateCredential(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "duplicate credential",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrAttachFirst(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "must attach to unsubscribe",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrGone(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusGone, // 410
		Text:      "gone",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrUnknown(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusInternalServerError, // 500
		Text:      "internal error",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}

func ErrNotImplemented(id, topic string, ts time.Time) *ServerComMessage {
	msg := &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotImplemented, // 501
		Text:      "not implemented",
		Topic:     topic,
		Timestamp: ts}}
	return msg
}
