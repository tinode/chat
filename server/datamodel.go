package main

/******************************************************************************
 *
 *  Description :
 *
 *    Wire protocol structures
 *
 *****************************************************************************/

import (
	"net/http"
	"strings"
	"time"
)

// MsgGetOpts defines Get query parameters.
type MsgGetOpts struct {
	// Optional User ID to return result(s) for one user.
	User string `json:"user,omitempty"`
	// Optional topic name to return result(s) for one topic.
	Topic string `json:"topic,omitempty"`
	// Return results modified dince this timespamp.
	IfModifiedSince *time.Time `json:"ims,omitempty"`
	// Load messages/ranges with IDs equal or greater than this (inclusive or closed)
	SinceId int `json:"since,omitempty"`
	// Load messages/ranges with IDs lower than this (exclusive or open)
	BeforeId int `json:"before,omitempty"`
	// Limit the number of messages loaded
	Limit int `json:"limit,omitempty"`
}

// MsgGetQuery is a topic metadata or data query.
type MsgGetQuery struct {
	What string `json:"what"`

	// Parameters of "desc" request: IfModifiedSince
	Desc *MsgGetOpts `json:"desc,omitempty"`
	// Parameters of "sub" request: User, Topic, IfModifiedSince, Limit.
	Sub *MsgGetOpts `json:"sub,omitempty"`
	// Parameters of "data" request: Since, Before, Limit.
	Data *MsgGetOpts `json:"data,omitempty"`
	// Parameters of "del" request: Since, Before, Limit.
	Del *MsgGetOpts `json:"del,omitempty"`
}

// MsgSetSub is a payload in set.sub request to update current subscription or invite another user, {sub.what} == "sub"
type MsgSetSub struct {
	// User affected by this request. Default (empty): current user
	User string `json:"user,omitempty"`

	// Access mode change, either Given or Want depending on context
	Mode string `json:"mode,omitempty"`
}

// MsgSetDesc is a C2S in set.what == "desc", acc, sub message
type MsgSetDesc struct {
	DefaultAcs *MsgDefaultAcsMode `json:"defacs,omitempty"` // default access mode
	Public     interface{}        `json:"public,omitempty"`
	Private    interface{}        `json:"private,omitempty"` // Per-subscription private data
}

// MsgCredClient is an account credential such as email or phone number.
type MsgCredClient struct {
	// Credential type, i.e. `email` or `tel`.
	Method string `json:"meth,omitempty"`
	// Value to verify, i.e. `user@example.com` or `+18003287448`
	Value string `json:"val,omitempty"`
	// Verification response
	Response string `json:"resp,omitempty"`
	// Request parameters, such as preferences. Passed to valiator without interpretation.
	Params interface{} `json:"params,omitempty"`
}

// MsgSetQuery is an update to topic metadata: Desc, subscriptions, or tags.
type MsgSetQuery struct {
	// Topic metadata, new topic & new subscriptions only
	Desc *MsgSetDesc `json:"desc,omitempty"`
	// Subscription parameters
	Sub *MsgSetSub `json:"sub,omitempty"`
	// Indexable tags for user discovery
	Tags []string `json:"tags,omitempty"`
	// Update to account credentials.
	Cred *MsgCredClient `json:"cred,omitempty"`
}

// MsgDelRange is either an individual ID (HiId=0) or a randge of deleted IDs, low end inclusive (closed),
// high-end exclusive (open): [LowId .. HiId), e.g. 1..5 -> 1, 2, 3, 4
type MsgDelRange struct {
	LowId int `json:"low,omitempty"`
	HiId  int `json:"hi,omitempty"`
}

// Client to Server (C2S) messages

// MsgClientHi is a handshake {hi} message.
type MsgClientHi struct {
	// Message Id
	Id string `json:"id,omitempty"`
	// User agent
	UserAgent string `json:"ua,omitempty"`
	// Protocol version, i.e. "0.13"
	Version string `json:"ver,omitempty"`
	// Client's unique device ID
	DeviceID string `json:"dev,omitempty"`
	// ISO 639-1 human language of the connected device
	Lang string `json:"lang,omitempty"`
	// Platform code: ios, android, web.
	Platform string `json:"platf,omitempty"`
}

// MsgClientAcc is an {acc} message for creating or updating a user account.
type MsgClientAcc struct {
	// Message Id
	Id string `json:"id,omitempty"`
	// "newXYZ" to create a new user or UserId to update a user; default: current user.
	User string `json:"user,omitempty"`
	// Account state: normal, suspended.
	State string `json:"status,omitempty"`
	// Authentication level of the user when UserID is set and not equal to the current user.
	// Either "", "auth" or "anon". Default: ""
	AuthLevel string
	// Authentication token for resetting the password and maybe other one-time actions.
	Token []byte `json:"token,omitempty"`
	// The initial authentication scheme the account can use
	Scheme string `json:"scheme,omitempty"`
	// Shared secret
	Secret []byte `json:"secret,omitempty"`
	// Authenticate session with the newly created account
	Login bool `json:"login,omitempty"`
	// Indexable tags for user discovery
	Tags []string `json:"tags,omitempty"`
	// User initialization data when creating a new user, otherwise ignored
	Desc *MsgSetDesc `json:"desc,omitempty"`
	// Credentials to verify (email or phone or captcha)
	Cred []MsgCredClient `json:"cred,omitempty"`
}

// MsgClientLogin is a login {login} message.
type MsgClientLogin struct {
	// Message Id
	Id string `json:"id,omitempty"`
	// Authentication scheme
	Scheme string `json:"scheme,omitempty"`
	// Shared secret
	Secret []byte `json:"secret"`
	// Credntials being verified (email or phone or captcha etc.)
	Cred []MsgCredClient `json:"cred,omitempty"`
}

// MsgClientSub is a subscription request {sub} message.
type MsgClientSub struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`

	// The subscription request is non-interactive, i.e. issued by a service on behalf of a user.
	// This affects presence notifications.
	Background bool `json:"bkg,omitempty"`

	// Mirrors {set}.
	Set *MsgSetQuery `json:"set,omitempty"`

	// Mirrors {get}.
	Get *MsgGetQuery `json:"get,omitempty"`
}

const (
	constMsgMetaDesc = 1 << iota
	constMsgMetaSub
	constMsgMetaData
	constMsgMetaTags
	constMsgMetaDel
	constMsgMetaCred
)

const (
	constMsgDelTopic = iota + 1
	constMsgDelMsg
	constMsgDelSub
	constMsgDelUser
	constMsgDelCred
)

func parseMsgClientMeta(params string) int {
	var bits int
	parts := strings.SplitN(params, " ", 8)
	for _, p := range parts {
		switch p {
		case "desc":
			bits |= constMsgMetaDesc
		case "sub":
			bits |= constMsgMetaSub
		case "data":
			bits |= constMsgMetaData
		case "tags":
			bits |= constMsgMetaTags
		case "del":
			bits |= constMsgMetaDel
		case "cred":
			bits |= constMsgMetaCred
		default:
			// ignore unknown
		}
	}
	return bits
}

func parseMsgClientDel(params string) int {
	switch params {
	case "", "msg":
		return constMsgDelMsg
	case "topic":
		return constMsgDelTopic
	case "sub":
		return constMsgDelSub
	case "user":
		return constMsgDelUser
	case "cred":
		return constMsgDelCred
	default:
		// ignore
	}
	return 0
}

// MsgDefaultAcsMode is a topic default access mode.
type MsgDefaultAcsMode struct {
	Auth string `json:"auth,omitempty"`
	Anon string `json:"anon,omitempty"`
}

// MsgClientLeave is an unsubscribe {leave} request message.
type MsgClientLeave struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	Unsub bool   `json:"unsub,omitempty"`
}

// MsgClientPub is client's request to publish data to topic subscribers {pub}
type MsgClientPub struct {
	Id      string                 `json:"id,omitempty"`
	Topic   string                 `json:"topic"`
	NoEcho  bool                   `json:"noecho,omitempty"`
	Head    map[string]interface{} `json:"head,omitempty"`
	Content interface{}            `json:"content"`
}

// MsgClientGet is a query of topic state {get}.
type MsgClientGet struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	MsgGetQuery
}

// MsgClientSet is an update of topic state {set}
type MsgClientSet struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`
	MsgSetQuery
}

// MsgClientDel delete messages or topic {del}.
type MsgClientDel struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic,omitempty"`
	// What to delete:
	// * "msg" to delete messages (default)
	// * "topic" to delete the topic
	// * "sub" to delete a subscription to topic.
	// * "user" to delete or disable user.
	// * "cred" to delete credential (email or phone)
	What string `json:"what"`
	// Delete messages with these IDs (either one by one or a set of ranges)
	DelSeq []MsgDelRange `json:"delseq,omitempty"`
	// User ID of the user or subscription to delete
	User string `json:"user,omitempty"`
	// Credential to delete
	Cred *MsgCredClient `json:"cred,omitempty"`
	// Request to hard-delete objects (i.e. delete messages for all users), if such option is available.
	Hard bool `json:"hard,omitempty"`
}

// MsgClientNote is a client-generated notification for topic subscribers {note}.
type MsgClientNote struct {
	// There is no Id -- server will not akn {ping} packets, they are "fire and forget"
	Topic string `json:"topic"`
	// what is being reported: "recv" - message received, "read" - message read, "kp" - typing notification
	What string `json:"what"`
	// Server-issued message ID being reported
	SeqId int `json:"seq,omitempty"`
	// Client's count of unread messages to report back to the server. Used in push notifications on iOS.
	Unread int `json:"unread,omitempty"`
}

// ClientComMessage is a wrapper for client messages.
type ClientComMessage struct {
	Hi    *MsgClientHi    `json:"hi"`
	Acc   *MsgClientAcc   `json:"acc"`
	Login *MsgClientLogin `json:"login"`
	Sub   *MsgClientSub   `json:"sub"`
	Leave *MsgClientLeave `json:"leave"`
	Pub   *MsgClientPub   `json:"pub"`
	Get   *MsgClientGet   `json:"get"`
	Set   *MsgClientSet   `json:"set"`
	Del   *MsgClientDel   `json:"del"`
	Note  *MsgClientNote  `json:"note"`

	// Message ID denormalized
	id string
	// Un-routable (original) topic name denormalized from XXX.Topic.
	topic string
	// Sender's UserId as string
	from string
	// Sender's authentication level
	authLvl int
	// Timestamp when this message was received by the server
	timestamp time.Time
}

/////////////////////////////////////////////////////////////
// Server to client messages

// MsgLastSeenInfo contains info on user's appearance online - when & user agent
type MsgLastSeenInfo struct {
	// Timestamp of user's last appearance online.
	When *time.Time `json:"when,omitempty"`
	// User agent of the device when the user was last online.
	UserAgent string `json:"ua,omitempty"`
}

// MsgCredServer is an account credential such as email or phone number.
type MsgCredServer struct {
	// Credential type, i.e. `email` or `tel`.
	Method string `json:"meth,omitempty"`
	// Credential value, i.e. `user@example.com` or `+18003287448`
	Value string `json:"val,omitempty"`
	// Indicates that the credential is validated.
	Done bool `json:"done,omitempty"`
}

// MsgAccessMode is a definition of access mode.
type MsgAccessMode struct {
	// Access mode requested by the user
	Want string `json:"want,omitempty"`
	// Access mode granted to the user by the admin
	Given string `json:"given,omitempty"`
	// Cumulative access mode want & given
	Mode string `json:"mode,omitempty"`
}

// MsgTopicDesc is a topic description, S2C in Meta message.
type MsgTopicDesc struct {
	CreatedAt *time.Time `json:"created,omitempty"`
	UpdatedAt *time.Time `json:"updated,omitempty"`
	// Timestamp of the last message
	TouchedAt *time.Time `json:"touched,omitempty"`

	// Account state, 'me' topic only.
	State string `json:"state,omitempty"`

	// If the group topic is online.
	Online bool `json:"online,omitempty"`

	DefaultAcs *MsgDefaultAcsMode `json:"defacs,omitempty"`
	// Actual access mode
	Acs *MsgAccessMode `json:"acs,omitempty"`
	// Max message ID
	SeqId     int `json:"seq,omitempty"`
	ReadSeqId int `json:"read,omitempty"`
	RecvSeqId int `json:"recv,omitempty"`
	// Id of the last delete operation as seen by the requesting user
	DelId  int         `json:"clear,omitempty"`
	Public interface{} `json:"public,omitempty"`
	// Per-subscription private data
	Private interface{} `json:"private,omitempty"`
}

// MsgTopicSub is topic subscription details, sent in Meta message.
type MsgTopicSub struct {
	// Fields common to all subscriptions

	// Timestamp when the subscription was last updated
	UpdatedAt *time.Time `json:"updated,omitempty"`
	// Timestamp when the subscription was deleted
	DeletedAt *time.Time `json:"deleted,omitempty"`

	// If the subscriber/topic is online
	Online bool `json:"online,omitempty"`

	// Access mode. Topic admins receive the full info, non-admins receive just the cumulative mode
	// Acs.Mode = want & given. The field is not a pointer because at least one value is always assigned.
	Acs MsgAccessMode `json:"acs,omitempty"`
	// ID of the message reported by the given user as read
	ReadSeqId int `json:"read,omitempty"`
	// ID of the message reported by the given user as received
	RecvSeqId int `json:"recv,omitempty"`
	// Topic's public data
	Public interface{} `json:"public,omitempty"`
	// User's own private data per topic
	Private interface{} `json:"private,omitempty"`

	// Response to non-'me' topic

	// Uid of the subscribed user
	User string `json:"user,omitempty"`

	// The following sections makes sense only in context of getting
	// user's own subscriptions ('me' topic response)

	// Topic name of this subscription
	Topic string `json:"topic,omitempty"`
	// Timestamp of the last message in the topic.
	TouchedAt *time.Time `json:"touched,omitempty"`
	// ID of the last {data} message in a topic
	SeqId int `json:"seq,omitempty"`
	// Id of the latest Delete operation
	DelId int `json:"clear,omitempty"`

	// P2P topics only:

	// Other user's last online timestamp & user agent
	LastSeen *MsgLastSeenInfo `json:"seen,omitempty"`
}

// MsgDelValues describes request to delete messages.
type MsgDelValues struct {
	DelId  int           `json:"clear,omitempty"`
	DelSeq []MsgDelRange `json:"delseq,omitempty"`
}

// MsgServerCtrl is a server control message {ctrl}.
type MsgServerCtrl struct {
	Id     string      `json:"id,omitempty"`
	Topic  string      `json:"topic,omitempty"`
	Params interface{} `json:"params,omitempty"`

	Code      int       `json:"code"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"ts"`
}

// MsgServerData is a server {data} message.
type MsgServerData struct {
	Topic string `json:"topic"`
	// ID of the user who originated the message as {pub}, could be empty if sent by the system
	From      string                 `json:"from,omitempty"`
	Timestamp time.Time              `json:"ts"`
	DeletedAt *time.Time             `json:"deleted,omitempty"`
	SeqId     int                    `json:"seq"`
	Head      map[string]interface{} `json:"head,omitempty"`
	Content   interface{}            `json:"content"`
}

// MsgServerPres is presence notification {pres} (authoritative update).
type MsgServerPres struct {
	Topic     string        `json:"topic"`
	Src       string        `json:"src,omitempty"`
	What      string        `json:"what"`
	UserAgent string        `json:"ua,omitempty"`
	SeqId     int           `json:"seq,omitempty"`
	DelId     int           `json:"clear,omitempty"`
	DelSeq    []MsgDelRange `json:"delseq,omitempty"`
	AcsTarget string        `json:"tgt,omitempty"`
	AcsActor  string        `json:"act,omitempty"`
	// Acs or a delta Acs. Need to marshal it to json under a name different than 'acs'
	// to allow different handling on the client
	Acs *MsgAccessMode `json:"dacs,omitempty"`

	// UNroutable params. All marked with `json:"-"` to exclude from json marshalling.
	// They are still serialized for intra-cluster communication.

	// Flag to break the reply loop
	WantReply bool `json:"-"`

	// Additional access mode filters when senting to topic's online members. Both filter conditions must be true.
	// send only to those who have this access mode.
	FilterIn int `json:"-"`
	// skip those who have this access mode.
	FilterOut int `json:"-"`

	// When sending to 'me', skip sessions subscribed to this topic
	SkipTopic string `json:"-"`

	// Send to sessions of a single user only
	SingleUser string `json:"-"`

	// Exclude sessions of a single user
	ExcludeUser string `json:"-"`
}

// MsgServerMeta is a topic metadata {meta} update.
type MsgServerMeta struct {
	Id    string `json:"id,omitempty"`
	Topic string `json:"topic"`

	Timestamp *time.Time `json:"ts,omitempty"`

	// Topic description
	Desc *MsgTopicDesc `json:"desc,omitempty"`
	// Subscriptions as an array of objects
	Sub []MsgTopicSub `json:"sub,omitempty"`
	// Delete ID and the ranges of IDs of deleted messages
	Del *MsgDelValues `json:"del,omitempty"`
	// User discovery tags
	Tags []string `json:"tags,omitempty"`
	// Account credentials, 'me' only.
	Cred []*MsgCredServer `json:"cred,omitempty"`
}

// MsgServerInfo is the server-side copy of MsgClientNote with From added (non-authoritative).
type MsgServerInfo struct {
	Topic string `json:"topic"`
	// ID of the user who originated the message
	From string `json:"from"`
	// what is being reported: "rcpt" - message received, "read" - message read, "kp" - typing notification
	What string `json:"what"`
	// Server-issued message ID being reported
	SeqId int `json:"seq,omitempty"`
}

// ServerComMessage is a wrapper for server-side messages.
type ServerComMessage struct {
	Ctrl *MsgServerCtrl `json:"ctrl,omitempty"`
	Data *MsgServerData `json:"data,omitempty"`
	Meta *MsgServerMeta `json:"meta,omitempty"`
	Pres *MsgServerPres `json:"pres,omitempty"`
	Info *MsgServerInfo `json:"info,omitempty"`

	// MsgServerData has no Id field, copying it here for use in {ctrl} aknowledgements
	id string
	// to: topic
	rcptto string
	// timestamp for consistency of timestamps in {ctrl} messages
	timestamp time.Time
	// User ID of the sender of the original message.
	from string
	// Originating session to send an aknowledgement to. Could be nil.
	sess *Session
	// Should the packet be sent to the original session? SessionID to skip.
	skipSid string
}

// Generators of server-side error messages {ctrl}.

// NoErr indicates successful completion (200)
func NoErr(id, topic string, ts time.Time) *ServerComMessage {
	return NoErrParams(id, topic, ts, nil)
}

// NoErrParams indicates successful completion with additional parameters (200)
func NoErrParams(id, topic string, ts time.Time, params interface{}) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusOK, // 200
		Text:      "ok",
		Topic:     topic,
		Params:    params,
		Timestamp: ts}}
}

// NoErrCreated indicated successful creation of an object (201).
func NoErrCreated(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusCreated, // 201
		Text:      "created",
		Topic:     topic,
		Timestamp: ts}}
}

// NoErrAccepted indicates request was accepted but not perocessed yet (202).
func NoErrAccepted(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusAccepted, // 202
		Text:      "accepted",
		Topic:     topic,
		Timestamp: ts}}
}

// NoErrEvicted indicates that the user was disconnected from topic for no fault of the user (205).
func NoErrEvicted(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusResetContent, // 205
		Text:      "evicted",
		Topic:     topic,
		Timestamp: ts}}
}

// NoErrShutdown means user was disconnected from topic because system shutdown is in progress (205).
func NoErrShutdown(ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Code:      http.StatusResetContent, // 205
		Text:      "server shutdown",
		Timestamp: ts}}
}

// 3xx

// InfoValidateCredentials requires user to confirm credentials before going forward (300).
func InfoValidateCredentials(id string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusMultipleChoices, // 300
		Text:      "validate credentials",
		Timestamp: ts}}
}

// InfoChallenge requires user to respond to presented challenge before login can be completed (300).
func InfoChallenge(id string, ts time.Time, challenge []byte) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusMultipleChoices, // 300
		Text:      "challenge",
		Params:    map[string]interface{}{"challenge": challenge},
		Timestamp: ts}}
}

// InfoAuthReset is sent in response to request to reset authentication when it was completed but login was not performed (301).
func InfoAuthReset(id string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusMovedPermanently, // 301
		Text:      "auth reset",
		Timestamp: ts}}
}

// InfoAlreadySubscribed response means request to subscribe was ignored because user is already subscribed (304).
func InfoAlreadySubscribed(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "already subscribed",
		Topic:     topic,
		Timestamp: ts}}
}

// InfoNotJoined response means request to leave was ignored because user was not subscribed (304).
func InfoNotJoined(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "not joined",
		Topic:     topic,
		Timestamp: ts}}
}

// InfoNoAction response means request was ignored because the object was already in the desired state (304).
func InfoNoAction(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "no action",
		Topic:     topic,
		Timestamp: ts}}
}

// InfoNotModified response means update request was a noop (304).
func InfoNotModified(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotModified, // 304
		Text:      "not modified",
		Topic:     topic,
		Timestamp: ts}}
}

// InfoFound redirects to a new resource (307).
func InfoFound(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusTemporaryRedirect, // 307
		Text:      "found",
		Topic:     topic,
		Timestamp: ts}}
}

// 4xx Errors

// ErrMalformed request malformed (400).
func ErrMalformed(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusBadRequest, // 400
		Text:      "malformed",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAuthRequired authentication required  - user must authenticate first (401).
func ErrAuthRequired(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "authentication required",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAuthFailed authentication failed (401).
func ErrAuthFailed(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "authentication failed",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAuthUnknownScheme authentication scheme is unrecognized or invalid (401).
func ErrAuthUnknownScheme(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnauthorized, // 401
		Text:      "unknown authentication scheme",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrPermissionDenied user is authenticated but operation is not permitted (403).
func ErrPermissionDenied(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusForbidden, // 403
		Text:      "permission denied",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAPIKeyRequired  valid API key is required (403).
func ErrAPIKeyRequired(ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Code:      http.StatusForbidden,
		Text:      "valid API key required",
		Timestamp: ts}}
}

// ErrSessionNotFound  valid API key is required (403).
func ErrSessionNotFound(ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Code:      http.StatusForbidden,
		Text:      "invalid or expired session",
		Timestamp: ts}}
}

// ErrTopicNotFound topic is not found (404).
func ErrTopicNotFound(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotFound,
		Text:      "topic not found", // 404
		Topic:     topic,
		Timestamp: ts}}
}

// ErrUserNotFound user is not found (404).
func ErrUserNotFound(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotFound, // 404
		Text:      "user not found",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrNotFound is an error for missing objects other than user or topic (404).
func ErrNotFound(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotFound, // 404
		Text:      "not found",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrOperationNotAllowed a valid operation is not permitted in this context (405).
func ErrOperationNotAllowed(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusMethodNotAllowed, // 405
		Text:      "operation or method not allowed",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrInvalidResponse indicates that the client's response in invalid (406).
func ErrInvalidResponse(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotAcceptable, // 406
		Text:      "invalid response",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAlreadyAuthenticated invalid attempt to authenticate an already authenticated session
// Switching users is not supported (409).
func ErrAlreadyAuthenticated(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "already authenticated",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrDuplicateCredential attempt to create a duplicate credential (409).
func ErrDuplicateCredential(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "duplicate credential",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAttachFirst must attach to topic first (409).
func ErrAttachFirst(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "must attach first",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrAlreadyExists the object already exists (409).
func ErrAlreadyExists(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "already exists",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrCommandOutOfSequence invalid sequence of comments, i.e. attempt to {sub} before {hi} (409).
func ErrCommandOutOfSequence(id, unused string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusConflict, // 409
		Text:      "command out of sequence",
		Timestamp: ts}}
}

// ErrGone topic deleted or user banned (410).
func ErrGone(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusGone, // 410
		Text:      "gone",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrTooLarge packet or request size exceeded the limit (413).
func ErrTooLarge(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusRequestEntityTooLarge, // 413
		Text:      "too large",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrPolicy request violates a policy (e.g. password is too weak or too many subscribers) (422).
func ErrPolicy(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusUnprocessableEntity, // 422
		Text:      "policy violation",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrLocked operation rejected because the topic is being deleted (423).
func ErrLocked(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusLocked, // 423
		Text:      "locked",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrUnknown database or other server error (500).
func ErrUnknown(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusInternalServerError, // 500
		Text:      "internal error",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrNotImplemented feature not implemented (501).
func ErrNotImplemented(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusNotImplemented, // 501
		Text:      "not implemented",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrClusterUnreachable in-cluster communication has failed (502).
func ErrClusterUnreachable(id, topic string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusBadGateway, // 502
		Text:      "cluster unreachable",
		Topic:     topic,
		Timestamp: ts}}
}

// ErrVersionNotSupported invalid (too low) protocol version (505).
func ErrVersionNotSupported(id string, ts time.Time) *ServerComMessage {
	return &ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        id,
		Code:      http.StatusHTTPVersionNotSupported, // 505
		Text:      "version not supported",
		Timestamp: ts}}
}
