/******************************************************************************
 *
 *  Description :
 *
 *  Handling of user sessions/connections. One user may have multiple sesions.
 *  Each session may handle multiple topics
 *
 *****************************************************************************/

package main

import (
	"container/list"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/text/language"
)

// Wait time before abandoning the outbound send operation.
// Timeout is rather long to make sure it's longer than Linux preeption time:
// https://elixir.bootlin.com/linux/latest/source/kernel/sched/fair.c#L38
const sendTimeout = time.Millisecond * 7

// Maximum number of queued messages before session is considered stale and dropped.
const sendQueueLimit = 128

// Time given to a background session to terminate to avoid tiggering presence notifications.
// If session terminates (or unsubscribes from topic) in this time frame notifications are not sent at all.
const deferredNotificationsTimeout = time.Second * 5

var minSupportedVersionValue = parseVersion(minSupportedVersion)

// SessionProto is the type of the wire transport.
type SessionProto int

// Constants defining individual types of wire transports.
const (
	// NONE is undefined/not set.
	NONE SessionProto = iota
	// WEBSOCK represents websocket connection.
	WEBSOCK
	// LPOLL represents a long polling connection.
	LPOLL
	// GRPC is a gRPC connection
	GRPC
	// PROXY is temporary session used as a proxy at master node.
	PROXY
	// MULTIPLEX is a multiplexing session reprsenting a connection from proxy topic to master.
	MULTIPLEX
)

// Session represents a single WS connection or a long polling session. A user may have multiple
// sessions.
type Session struct {
	// protocol - NONE (unset), WEBSOCK, LPOLL, GRPC, PROXY, MULTIPLEX
	proto SessionProto

	// Session ID
	sid string

	// Websocket. Set only for websocket sessions.
	ws *websocket.Conn

	// Pointer to session's record in sessionStore. Set only for Long Poll sessions.
	lpTracker *list.Element

	// gRPC handle. Set only for gRPC clients.
	grpcnode pbx.Node_MessageLoopServer

	// Reference to the cluster node where the session has originated. Set only for cluster RPC sessions.
	clnode *ClusterNode

	// Reference to multiplexing session. Set only for proxy sessions.
	multi *Session

	// IP address of the client. For long polling this is the IP of the last poll.
	remoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet.
	userAgent string

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff).
	ver int

	// Device ID of the client
	deviceID string
	// Platform: web, ios, android
	platf string
	// Human language of the client
	lang string
	// Country code of the client
	countryCode string

	// ID of the current user. Could be zero if session is not authenticated
	// or for multiplexing sessions.
	uid types.Uid

	// Authentication level - NONE (unset), ANON, AUTH, ROOT.
	authLvl auth.Level

	// Time when the long polling session was last refreshed
	lastTouched time.Time

	// Time when the session received any packer from client
	lastAction time.Time

	// Background session: subscription presence notifications and online status are delayed.
	background bool
	// Timer which triggers after some seconds to mark background session as foreground.
	bkgTimer *time.Timer

	// Outbound mesages, buffered.
	// The content must be serialized in format suitable for the session.
	send chan interface{}

	// Channel for shutting down the session, buffer 1.
	// Content in the same format as for 'send'
	stop chan interface{}

	// detach - channel for detaching session from topic, buffered.
	// Content is topic name to detach from.
	detach chan string

	// Map of topic subscriptions, indexed by topic name.
	// Don't access directly. Use getters/setters.
	subs map[string]*Subscription
	// Mutex for subs access: both topic go routines and network go routines access
	// subs concurrently.
	subsLock sync.RWMutex

	// Needed for long polling and grpc.
	lock sync.Mutex

	// Field used only in cluster mode by topic master node.

	// Type of proxy to master request being handled.
	proxyReq ProxyReqType
}

// Subscription is a mapper of sessions to topics.
type Subscription struct {
	// Channel to communicate with the topic, copy of Topic.broadcast
	broadcast chan<- *ServerComMessage

	// Session sends a signal to Topic when this session is unsubscribed
	// This is a copy of Topic.unreg
	done chan<- *sessionLeave

	// Channel to send {meta} requests, copy of Topic.meta
	meta chan<- *metaReq

	// Channel to ping topic with session's updates
	supd chan<- *sessionUpdate
}

func (s *Session) addSub(topic string, sub *Subscription) {
	if s.multi != nil {
		s.multi.addSub(topic, sub)
		return
	}
	s.subsLock.Lock()

	// Sessions that serve as an interface between proxy topics and their masters (proxy sessions)
	// may have only one subscription, that is, to its master topic.
	// Normal sessions may be subscribed to multiple topics.

	if !s.isMultiplex() || s.countSub() == 0 {
		s.subs[topic] = sub
	}
	s.subsLock.Unlock()
}

func (s *Session) getSub(topic string) *Subscription {
	// Don't check s.multi here. Let it panic if called for proxy session.

	s.subsLock.RLock()
	defer s.subsLock.RUnlock()

	return s.subs[topic]
}

func (s *Session) delSub(topic string) {
	if s.multi != nil {
		s.multi.delSub(topic)
		return
	}
	s.subsLock.Lock()
	delete(s.subs, topic)
	s.subsLock.Unlock()
}

func (s *Session) countSub() int {
	if s.multi != nil {
		return s.multi.countSub()
	}
	return len(s.subs)
}

// Inform topics that the session is being terminated.
// No need to check for s.multi because it's not called for PROXY sessions.
func (s *Session) unsubAll() {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()

	for _, sub := range s.subs {
		// sub.done is the same as topic.unreg
		// Leave message is not set because the whole session is being dropped.
		sub.done <- &sessionLeave{sess: s}
	}
}

// Represents a proxied (remote) session.
type remoteSession struct {
	// User id of the proxied session.
	uid types.Uid
	// Whether the proxied session is background.
	isBackground bool
}

// Indicates whether this session is a local interface for a remote proxy topic.
// It multiplexes multiple sessions.
func (s *Session) isMultiplex() bool {
	return s.proto == MULTIPLEX
}

// Indicates whether this session is a short-lived proxy for a remote session.
func (s *Session) isProxy() bool {
	return s.proto == PROXY
}

// Cluster session: either a proxy or a multiplexing session.
func (s *Session) isCluster() bool {
	return s.isProxy() || s.isMultiplex()
}

// queueOut attempts to send a ServerComMessage to a session write loop; if the send buffer is full,
// timeout is `sendTimeout`.
func (s *Session) queueOut(msg *ServerComMessage) bool {
	if s.multi != nil {
		// In case of a cluster we need to pass a copy of the actual session.
		msg.sess = s
		return s.multi.queueOut(msg)
	}

	// Record latency only on {ctrl} messages and end-user sessions.
	if msg.Ctrl != nil && msg.Id != "" && !msg.Ctrl.Timestamp.IsZero() && !s.isCluster() {
		duration := time.Since(msg.Ctrl.Timestamp).Milliseconds()
		statsAddSample("RequestLatency", float64(duration))
	}

	select {
	case s.send <- s.serialize(msg):
	case <-time.After(sendTimeout):
		log.Println("s.queueOut: timeout", s.sid)
		return false
	}
	return true
}

// queueOutBytes attempts to send a ServerComMessage already serialized to []byte.
// If the send buffer is full, timeout is `sendTimeout`.
func (s *Session) queueOutBytes(data []byte) bool {
	if s == nil {
		return true
	}

	select {
	case s.send <- data:
	case <-time.After(sendTimeout):
		log.Println("s.queueOutBytes: timeout", s.sid)
		return false
	}
	return true
}

// cleanUp is called when the session is terminated to perform resource cleanup.
func (s *Session) cleanUp(expired bool) {
	if !expired {
		globals.sessionStore.Delete(s)
	}

	s.background = false
	s.bkgTimer.Stop()
	s.unsubAll()
}

// Message received, convert bytes to ClientComMessage and dispatch
func (s *Session) dispatchRaw(raw []byte) {
	var msg ClientComMessage

	if len(raw) == 1 && raw[0] == 0x31 {
		// 0x31 == '1'. This is a network probe message. Respond with a '0':
		s.queueOutBytes([]byte{0x30})
		return
	}

	toLog := raw
	truncated := ""
	if len(raw) > 512 {
		toLog = raw[:512]
		truncated = "<...>"
	}
	log.Printf("in: '%s%s' sid='%s' uid='%s'", toLog, truncated, s.sid, s.uid)

	if err := json.Unmarshal(raw, &msg); err != nil {
		// Malformed message
		log.Println("s.dispatch", err, s.sid)
		s.queueOut(ErrMalformed("", "", time.Now().UTC().Round(time.Millisecond)))
		return
	}

	s.dispatch(&msg)
}

func (s *Session) dispatch(msg *ClientComMessage) {
	s.lastAction = types.TimeNow()
	msg.timestamp = s.lastAction

	if msg.AsUser == "" {
		msg.AsUser = s.uid.UserId()
		msg.AuthLvl = int(s.authLvl)
	} else if s.authLvl != auth.LevelRoot {
		// Only root user can set non-default msg.from && msg.authLvl values.
		s.queueOut(ErrPermissionDenied("", "", msg.timestamp))
		log.Println("s.dispatch: non-root asigned msg.from", s.sid)
		return
	} else if fromUid := types.ParseUserId(msg.AsUser); fromUid.IsZero() {
		s.queueOut(ErrMalformed("", "", msg.timestamp))
		log.Println("s.dispatch: malformed msg.from: ", msg.AsUser, s.sid)
		return
	}

	var resp *ServerComMessage
	if msg, resp = pluginFireHose(s, msg); resp != nil {
		// Plugin provided a response. No further processing is needed.
		s.queueOut(resp)
		return
	} else if msg == nil {
		// Plugin requested to silently drop the request.
		return
	}

	var handler func(*ClientComMessage)
	var uaRefresh bool

	// Check if s.ver is defined
	checkVers := func(m *ClientComMessage, handler func(*ClientComMessage)) func(*ClientComMessage) {
		return func(m *ClientComMessage) {
			if s.ver == 0 {
				log.Println("s.dispatch: {hi} is missing", s.sid)
				s.queueOut(ErrCommandOutOfSequence(m.Id, m.Original, m.timestamp))
				return
			}
			handler(m)
		}
	}

	// Check if user is logged in
	checkUser := func(m *ClientComMessage, handler func(*ClientComMessage)) func(*ClientComMessage) {
		return func(m *ClientComMessage) {
			if msg.AsUser == "" {
				log.Println("s.dispatch: authentication required", s.sid)
				s.queueOut(ErrAuthRequired(m.Id, m.Original, msg.timestamp))
				return
			}
			handler(m)
		}
	}

	switch {
	case msg.Pub != nil:
		handler = checkVers(msg, checkUser(msg, s.publish))
		msg.Id = msg.Pub.Id
		msg.Original = msg.Pub.Topic
		uaRefresh = true

	case msg.Sub != nil:
		handler = checkVers(msg, checkUser(msg, s.subscribe))
		msg.Id = msg.Sub.Id
		msg.Original = msg.Sub.Topic
		uaRefresh = true

	case msg.Leave != nil:
		handler = checkVers(msg, checkUser(msg, s.leave))
		msg.Id = msg.Leave.Id
		msg.Original = msg.Leave.Topic

	case msg.Hi != nil:
		handler = s.hello
		msg.Id = msg.Hi.Id

	case msg.Login != nil:
		handler = checkVers(msg, s.login)
		msg.Id = msg.Login.Id

	case msg.Get != nil:
		handler = checkVers(msg, checkUser(msg, s.get))
		msg.Id = msg.Get.Id
		msg.Original = msg.Get.Topic
		uaRefresh = true

	case msg.Set != nil:
		handler = checkVers(msg, checkUser(msg, s.set))
		msg.Id = msg.Set.Id
		msg.Original = msg.Set.Topic
		uaRefresh = true

	case msg.Del != nil:
		handler = checkVers(msg, checkUser(msg, s.del))
		msg.Id = msg.Del.Id
		msg.Original = msg.Del.Topic

	case msg.Acc != nil:
		handler = checkVers(msg, s.acc)
		msg.Id = msg.Acc.Id

	case msg.Note != nil:
		handler = s.note
		msg.Original = msg.Note.Topic
		uaRefresh = true

	default:
		// Unknown message
		s.queueOut(ErrMalformed("", "", msg.timestamp))
		log.Println("s.dispatch: unknown message", s.sid)
		return
	}

	if globals.cluster.isPartitioned() {
		// The cluster is partitioned due to network or other failure and this node is a part of the smaller partition.
		// In order to avoid data inconsistency across the cluster we must reject all requests.
		s.queueOut(ErrClusterUnreachable(msg.Id, msg.Original, msg.timestamp))
		return
	}

	handler(msg)

	// Notify 'me' topic that this session is currently active.
	if uaRefresh && msg.AsUser != "" && s.userAgent != "" {
		if sub := s.getSub(msg.AsUser); sub != nil {
			// The chan is buffered. If the buffer is exhaused, the session will wait for 'me' to become available
			sub.supd <- &sessionUpdate{userAgent: s.userAgent}
		}
	}
}

// Request to subscribe to a topic
func (s *Session) subscribe(msg *ClientComMessage) {
	if strings.HasPrefix(msg.Original, "new") {
		// Request to create a new named topic.
		// If we are in a cluster, make sure the new topic belongs to the current node.
		msg.RcptTo = globals.cluster.genLocalTopicName()
	} else {
		var resp *ServerComMessage
		msg.RcptTo, resp = s.expandTopicName(msg)
		if resp != nil {
			s.queueOut(resp)
			return
		}
	}

	// Session can subscribe to topic on behalf of a single user at a time.
	if sub := s.getSub(msg.RcptTo); sub != nil {
		s.queueOut(InfoAlreadySubscribed(msg.Id, msg.Original, msg.timestamp))
	} else {
		globals.hub.join <- &sessionJoin{
			pkt:  msg,
			sess: s}
		// Hub will send Ctrl success/failure packets back to session
	}
}

// Leave/Unsubscribe a topic
func (s *Session) leave(msg *ClientComMessage) {
	// Expand topic name
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	if sub := s.getSub(msg.RcptTo); sub != nil {
		// Session is attached to the topic.
		if (msg.Original == "me" || msg.Original == "fnd") && msg.Leave.Unsub {
			// User should not unsubscribe from 'me' or 'find'. Just leaving is fine.
			s.queueOut(ErrPermissionDenied(msg.Id, msg.Original, msg.timestamp))
		} else {
			// Unlink from topic, topic will send a reply.
			s.delSub(msg.RcptTo)
			sub.done <- &sessionLeave{
				pkt:  msg,
				sess: s}
		}
	} else if !msg.Leave.Unsub {
		// Session is not attached to the topic, wants to leave - fine, no change
		s.queueOut(InfoNotJoined(msg.Id, msg.Original, msg.timestamp))
	} else {
		// Session wants to unsubscribe from the topic it did not join
		// FIXME(gene): allow topic to unsubscribe without joining first; send to hub to unsub
		log.Println("s.leave:", "must attach first", s.sid)
		s.queueOut(ErrAttachFirst(msg.Id, msg.Original, msg.timestamp))
	}
}

// Broadcast a message to all topic subscribers
func (s *Session) publish(msg *ClientComMessage) {
	// TODO(gene): Check for repeated messages with the same ID
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	// Add "sender" header if the message is sent on behalf of another user.
	if msg.AsUser != s.uid.UserId() {
		if msg.Pub.Head == nil {
			msg.Pub.Head = make(map[string]interface{})
		}
		msg.Pub.Head["sender"] = s.uid.UserId()
	} else if msg.Pub.Head != nil {
		// Clear potentially false "sender" field.
		delete(msg.Pub.Head, "sender")
		if len(msg.Pub.Head) == 0 {
			msg.Pub.Head = nil
		}
	}

	data := &ServerComMessage{Data: &MsgServerData{
		Topic:     msg.Original,
		From:      msg.AsUser,
		Timestamp: msg.timestamp,
		Head:      msg.Pub.Head,
		Content:   msg.Pub.Content},
		// Internal-only values.
		Id:        msg.Id,
		RcptTo:    msg.RcptTo,
		AsUser:    msg.AsUser,
		Timestamp: msg.timestamp,
		sess:      s}
	if msg.Pub.NoEcho {
		data.SkipSid = s.sid
	}
	if sub := s.getSub(msg.RcptTo); sub != nil {
		// This is a post to a subscribed topic. The message is sent to the topic only
		sub.broadcast <- data
	} else if msg.RcptTo == "sys" {
		// Publishing to "sys" topic requires no subsription.
		globals.hub.route <- data
	} else {
		// Publish request received without attaching to topic first.
		s.queueOut(ErrAttachFirst(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.publish:", "must attach first", s.sid)
	}
}

// Client metadata
func (s *Session) hello(msg *ClientComMessage) {
	var params map[string]interface{}
	var deviceIDUpdate bool

	if s.ver == 0 {
		s.ver = parseVersion(msg.Hi.Version)
		if s.ver == 0 {
			log.Println("s.hello:", "failed to parse version", s.sid)
			s.queueOut(ErrMalformed(msg.Id, "", msg.timestamp))
			return
		}
		// Check version compatibility
		if versionCompare(s.ver, minSupportedVersionValue) < 0 {
			s.ver = 0
			s.queueOut(ErrVersionNotSupported(msg.Id, msg.timestamp))
			log.Println("s.hello:", "unsupported version", s.sid)
			return
		}

		params = map[string]interface{}{
			"ver":                currentVersion,
			"build":              store.GetAdapterName() + ":" + buildstamp,
			"maxMessageSize":     globals.maxMessageSize,
			"maxSubscriberCount": globals.maxSubscriberCount,
			"minTagLength":       minTagLength,
			"maxTagLength":       maxTagLength,
			"maxTagCount":        globals.maxTagCount,
			"maxFileUploadSize":  globals.maxFileUploadSize,
		}

		// Set ua & platform in the beginning of the session.
		// Don't change them later.
		s.userAgent = msg.Hi.UserAgent
		s.platf = msg.Hi.Platform
		if s.platf == "" {
			s.platf = platformFromUA(msg.Hi.UserAgent)
		}
		// This is a background session. Start a timer.
		if msg.Hi.Background {
			s.bkgTimer.Reset(deferredNotificationsTimeout)
		}
	} else if msg.Hi.Version == "" || parseVersion(msg.Hi.Version) == s.ver {
		// Save changed device ID+Lang or delete earlier specified device ID.
		// Platform cannot be changed.
		if !s.uid.IsZero() {
			var err error
			if msg.Hi.DeviceID == types.NullValue {
				deviceIDUpdate = true
				err = store.Devices.Delete(s.uid, s.deviceID)
			} else if msg.Hi.DeviceID != "" {
				deviceIDUpdate = true
				err = store.Devices.Update(s.uid, s.deviceID, &types.DeviceDef{
					DeviceId: msg.Hi.DeviceID,
					Platform: s.platf,
					LastSeen: msg.timestamp,
					Lang:     msg.Hi.Lang,
				})
			}

			if err != nil {
				log.Println("s.hello:", "device ID", err, s.sid)
				s.queueOut(ErrUnknown(msg.Id, "", msg.timestamp))
				return
			}
		}
	} else {
		// Version cannot be changed mid-session.
		s.queueOut(ErrCommandOutOfSequence(msg.Id, "", msg.timestamp))
		log.Println("s.hello:", "version cannot be changed", s.sid)
		return
	}

	if msg.Hi.DeviceID == types.NullValue {
		msg.Hi.DeviceID = ""
	}
	s.deviceID = msg.Hi.DeviceID
	s.lang = msg.Hi.Lang
	// Try to deduce the country from the locale.
	if tag, err := language.Parse(s.lang); err == nil {
		if region, conf := tag.Region(); region.IsCountry() && conf >= language.High {
			s.countryCode = region.String()
		}
	}
	if s.countryCode == "" {
		if len(s.lang) > 2 {
			// Logging strings longer than 2 b/c language.Parse(XX) always succeeds
			// returning confidence Low.
			log.Println("s.hello:", "could not parse locale ", s.lang)
		}
		s.countryCode = globals.defaultCountryCode
	}

	var httpStatus int
	var httpStatusText string
	if s.proto == LPOLL || deviceIDUpdate {
		// In case of long polling StatusCreated was reported earlier.
		// In case of deviceID update just report success.
		httpStatus = http.StatusOK
		httpStatusText = "ok"

	} else {
		httpStatus = http.StatusCreated
		httpStatusText = "created"
	}

	ctrl := &MsgServerCtrl{Id: msg.Id, Code: httpStatus, Text: httpStatusText, Timestamp: msg.timestamp}
	if len(params) > 0 {
		ctrl.Params = params
	}
	s.queueOut(&ServerComMessage{Ctrl: ctrl})
}

// Account creation
func (s *Session) acc(msg *ClientComMessage) {

	// If token is provided, get the user ID from it.
	var rec *auth.Rec
	if msg.Acc.Token != nil {
		if !s.uid.IsZero() {
			s.queueOut(ErrAlreadyAuthenticated(msg.Acc.Id, "", msg.timestamp))
			log.Println("s.acc: got token while already authenticated", s.sid)
			return
		}

		var err error
		rec, _, err = store.GetLogicalAuthHandler("token").Authenticate(msg.Acc.Token)
		if err != nil {
			s.queueOut(decodeStoreError(err, msg.Acc.Id, "", msg.timestamp,
				map[string]interface{}{"what": "auth"}))
			log.Println("s.acc: invalid token", err, s.sid)
			return
		}
	}

	if strings.HasPrefix(msg.Acc.User, "new") {
		// New account
		replyCreateUser(s, msg, rec)
	} else {
		// Existing account.
		replyUpdateUser(s, msg, rec)
	}
}

// Authenticate
func (s *Session) login(msg *ClientComMessage) {
	// msg.from is ignored here

	if msg.Login.Scheme == "reset" {
		if err := s.authSecretReset(msg.Login.Secret); err != nil {
			s.queueOut(decodeStoreError(err, msg.Id, "", msg.timestamp, nil))
		} else {
			s.queueOut(InfoAuthReset(msg.Id, msg.timestamp))
		}
		return
	}

	if !s.uid.IsZero() {
		// TODO: change error to notice InfoNoChange and return current user ID & auth level
		// params := map[string]interface{}{"user": s.uid.UserId(), "authlvl": s.authLevel.String()}
		s.queueOut(ErrAlreadyAuthenticated(msg.Id, "", msg.timestamp))
		return
	}

	handler := store.GetLogicalAuthHandler(msg.Login.Scheme)
	if handler == nil {
		log.Println("s.login: unknown authentication scheme", msg.Login.Scheme, s.sid)
		s.queueOut(ErrAuthUnknownScheme(msg.Id, "", msg.timestamp))
		return
	}

	rec, challenge, err := handler.Authenticate(msg.Login.Secret)
	if err != nil {
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.timestamp, nil))
		return
	}

	// If authenticator did not check user state, it returns state "undef". If so, check user state here.
	if rec.State == types.StateUndefined {
		rec.State, err = userGetState(rec.Uid)
	}
	if err == nil && rec.State != types.StateOK {
		err = types.ErrPermissionDenied
	}

	if err != nil {
		log.Println("s.login: user state check failed", rec.Uid, err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.timestamp, nil))
		return
	}

	if challenge != nil {
		// Multi-stage authentication. Issue challenge to the client.
		s.queueOut(InfoChallenge(msg.Id, msg.timestamp, challenge))
		return
	}

	var missing []string
	if rec.Features&auth.FeatureValidated == 0 && len(globals.authValidators[rec.AuthLevel]) > 0 {
		var validated []string
		// Check responses. Ignore invalid responses, just keep cred unvalidated.
		if validated, _, err = validatedCreds(rec.Uid, rec.AuthLevel, msg.Login.Cred, false); err == nil {
			// Get a list of credentials which have not been validated.
			_, missing = stringSliceDelta(globals.authValidators[rec.AuthLevel], validated)
		}
	}
	if err != nil {
		log.Println("s.login: failed to validate credentials:", err, s.sid)
		s.queueOut(decodeStoreError(err, msg.Id, "", msg.timestamp, nil))
	} else {
		s.queueOut(s.onLogin(msg.Id, msg.timestamp, rec, missing))
	}
}

// authSecretReset resets an authentication secret;
//  params: "auth-method-to-reset:credential-method:credential-value".
func (s *Session) authSecretReset(params []byte) error {
	var authScheme, credMethod, credValue string
	if parts := strings.Split(string(params), ":"); len(parts) == 3 {
		authScheme, credMethod, credValue = parts[0], parts[1], parts[2]
	} else {
		return types.ErrMalformed
	}

	// Technically we don't need to check it here, but we are going to mail the 'authName' string to the user.
	// We have to make sure it does not contain any exploits. This is the simplest check.
	hdl := store.GetLogicalAuthHandler(authScheme)
	if hdl == nil {
		return types.ErrUnsupported
	}
	validator := store.GetValidator(credMethod)
	if validator == nil {
		return types.ErrUnsupported
	}
	uid, err := store.Users.GetByCred(credMethod, credValue)
	if err != nil {
		return err
	}
	if uid.IsZero() {
		return types.ErrNotFound
	}

	resetParams, err := hdl.GetResetParams(uid)
	if err != nil {
		return err
	}

	token, _, err := store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
		Uid:       uid,
		AuthLevel: auth.LevelAuth,
		Lifetime:  time.Hour * 24,
		Features:  auth.FeatureNoLogin})

	if err != nil {
		return err
	}

	return validator.ResetSecret(credValue, authScheme, s.lang, token, resetParams)
}

// onLogin performs steps after successful authentication.
func (s *Session) onLogin(msgID string, timestamp time.Time, rec *auth.Rec, missing []string) *ServerComMessage {

	var reply *ServerComMessage
	var params map[string]interface{}

	features := rec.Features

	params = map[string]interface{}{
		"user":    rec.Uid.UserId(),
		"authlvl": rec.AuthLevel.String()}
	if len(missing) > 0 {
		// Some credentials are not validated yet. Respond with request for validation.
		reply = InfoValidateCredentials(msgID, timestamp)

		params["cred"] = missing
	} else {
		// Everything is fine, authenticate the session.

		reply = NoErr(msgID, "", timestamp)

		// Check if the token is suitable for session authentication.
		if features&auth.FeatureNoLogin == 0 {
			// Authenticate the session.
			s.uid = rec.Uid
			s.authLvl = rec.AuthLevel
			// Reset expiration time.
			rec.Lifetime = 0
		}
		features |= auth.FeatureValidated

		// Record deviceId used in this session
		if s.deviceID != "" {
			if err := store.Devices.Update(rec.Uid, "", &types.DeviceDef{
				DeviceId: s.deviceID,
				Platform: s.platf,
				LastSeen: timestamp,
				Lang:     s.lang,
			}); err != nil {
				log.Println("failed to update device record", err)
			}
		}
	}

	// GenSecret fails only if tokenLifetime is < 0. It can't be < 0 here,
	// otherwise login would have failed earlier.
	rec.Features = features
	params["token"], params["expires"], _ = store.GetLogicalAuthHandler("token").GenSecret(rec)

	reply.Ctrl.Params = params
	return reply
}

func (s *Session) get(msg *ClientComMessage) {
	// Expand topic name.
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	msg.MetaWhat = parseMsgClientMeta(msg.Get.What)

	sub := s.getSub(msg.RcptTo)
	meta := &metaReq{
		pkt:  msg,
		sess: s}

	if meta.pkt.MetaWhat == 0 {
		s.queueOut(ErrMalformed(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.get: invalid Get message action", msg.Get.What)
	} else if sub != nil {
		sub.meta <- meta
	} else if meta.pkt.MetaWhat&(constMsgMetaDesc|constMsgMetaSub) != 0 {
		// Request some minimal info from a topic not currently attached to.
		globals.hub.meta <- meta
	} else {
		log.Println("s.get: subscribe first to get=", msg.Get.What)
		s.queueOut(ErrPermissionDenied(msg.Id, msg.Original, msg.timestamp))
	}
}

func (s *Session) set(msg *ClientComMessage) {
	// Expand topic name.
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	meta := &metaReq{
		pkt:  msg,
		sess: s}

	if msg.Set.Desc != nil {
		meta.pkt.MetaWhat = constMsgMetaDesc
	}
	if msg.Set.Sub != nil {
		meta.pkt.MetaWhat |= constMsgMetaSub
	}
	if msg.Set.Tags != nil {
		meta.pkt.MetaWhat |= constMsgMetaTags
	}
	if msg.Set.Cred != nil {
		meta.pkt.MetaWhat |= constMsgMetaCred
	}

	if meta.pkt.MetaWhat == 0 {
		s.queueOut(ErrMalformed(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.set: nil Set action")
	} else if sub := s.getSub(msg.RcptTo); sub != nil {
		sub.meta <- meta
	} else if meta.pkt.MetaWhat&(constMsgMetaTags|constMsgMetaCred) != 0 {
		log.Println("s.set: can Set tags/creds for subscribed topics only", meta.pkt.MetaWhat)
		s.queueOut(ErrPermissionDenied(msg.Id, msg.Original, msg.timestamp))
	} else {
		// Desc.Private and Sub updates are possible without the subscription.
		globals.hub.meta <- meta
	}
}

func (s *Session) del(msg *ClientComMessage) {
	msg.MetaWhat = parseMsgClientDel(msg.Del.What)

	// Delete user
	if msg.MetaWhat == constMsgDelUser {
		replyDelUser(s, msg)
		return
	}

	// Delete something other than user: topic, subscription, message(s)

	// Expand topic name and validate request.
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	if msg.MetaWhat == 0 {
		s.queueOut(ErrMalformed(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.del: invalid Del action", msg.Del.What, s.sid)
		return
	}
	sub := s.getSub(msg.RcptTo)
	if sub != nil && msg.MetaWhat != constMsgDelTopic {
		// Session is attached, deleting subscription or messages. Send to topic.
		sub.meta <- &metaReq{
			pkt:  msg,
			sess: s}
	} else if msg.MetaWhat == constMsgDelTopic {
		// Deleting topic: for sessions attached or not attached, send request to hub first.
		// Hub will forward to topic, if appropriate.
		globals.hub.unreg <- &topicUnreg{
			rcptTo: msg.RcptTo,
			pkt:    msg,
			sess:   s,
			del:    true}
	} else {
		// Must join the topic to delete messages or subscriptions.
		s.queueOut(ErrAttachFirst(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.del: invalid Del action while unsubbed", msg.Del.What, s.sid)
	}
}

// Broadcast a transient {ping} message to active topic subscribers
// Not reporting any errors
func (s *Session) note(msg *ClientComMessage) {

	if s.ver == 0 || msg.AsUser == "" {
		// Silently ignore the message: have not received {hi} or don't know who sent the message.
		return
	}

	// Expand topic name and validate request.
	var resp *ServerComMessage
	msg.RcptTo, resp = s.expandTopicName(msg)
	if resp != nil {
		// Silently ignoring the message
		return
	}

	switch msg.Note.What {
	case "kp":
		if msg.Note.SeqId != 0 {
			return
		}
	case "read", "recv":
		if msg.Note.SeqId <= 0 {
			return
		}
	default:
		return
	}

	if sub := s.getSub(msg.RcptTo); sub != nil {
		// Pings can be sent to subscribed topics only
		sub.broadcast <- &ServerComMessage{
			Info: &MsgServerInfo{
				Topic: msg.Original,
				From:  msg.AsUser,
				What:  msg.Note.What,
				SeqId: msg.Note.SeqId},
			RcptTo:    msg.RcptTo,
			AsUser:    msg.AsUser,
			Timestamp: msg.timestamp,
			SkipSid:   s.sid,
			sess:      s}
	} else {
		s.queueOut(ErrAttachFirst(msg.Id, msg.Original, msg.timestamp))
		log.Println("s.note: note to invalid topic - must subscribe first", msg.Note.What, s.sid)
	}
}

// expandTopicName expands session specific topic name to global name
// Returns
//   topic: session-specific topic name the message recipient should see
//   routeTo: routable global topic name
//   err: *ServerComMessage with an error to return to the sender
func (s *Session) expandTopicName(msg *ClientComMessage) (string, *ServerComMessage) {

	if msg.Original == "" {
		log.Println("s.etn: empty topic name", s.sid)
		return "", ErrMalformed(msg.Id, "", msg.timestamp)
	}

	// Expanded name of the topic to route to i.e. rcptto: or s.subs[routeTo]
	var routeTo string
	if msg.Original == "me" {
		routeTo = msg.AsUser
	} else if msg.Original == "fnd" {
		routeTo = types.ParseUserId(msg.AsUser).FndName()
	} else if strings.HasPrefix(msg.Original, "usr") {
		// p2p topic
		uid1 := types.ParseUserId(msg.AsUser)
		uid2 := types.ParseUserId(msg.Original)
		if uid2.IsZero() {
			// Ensure the user id is valid
			log.Println("s.etn: failed to parse p2p topic name", s.sid)
			return "", ErrMalformed(msg.Id, msg.Original, msg.timestamp)
		} else if uid2 == uid1 {
			// Use 'me' to access self-topic
			log.Println("s.etn: invalid p2p self-subscription", s.sid)
			return "", ErrPermissionDenied(msg.Id, msg.Original, msg.timestamp)
		}
		routeTo = uid1.P2PName(uid2)
	} else {
		routeTo = msg.Original
	}

	return routeTo, nil
}

func (s *Session) serialize(msg *ServerComMessage) interface{} {
	if s.proto == GRPC {
		return pbServSerialize(msg)
	}

	if s.proto == MULTIPLEX {
		// No need to serialize the message to bytes within the cluster,
		// but we have to create a copy because the original msg can be mutated.
		return msg.copy()
	}

	out, _ := json.Marshal(msg)
	return out
}

// onBackgroundTimer marks background session as foreground and informs topics it's subscribed to.
func (s *Session) onBackgroundTimer() {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()

	update := &sessionUpdate{sess: s}
	for _, sub := range s.subs {
		if sub.supd != nil {
			sub.supd <- update
		}
	}
}
