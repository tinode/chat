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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Wire transport
const (
	NONE = iota
	WEBSOCK
	LPOLL
	RPC
)

var MIN_SUPPORTED_VERSION_VAL = parseVersion(MIN_SUPPORTED_VERSION)

// A single WS connection or a long polling session. A user may have multiple
// sessions.
type Session struct {
	// protocol - NONE (unset), WEBSOCK, LPOLL, RPC
	proto int

	// -- Set only for websockets
	// Websocket
	ws *websocket.Conn
	// --

	// -- Set only for Long Poll sessions
	// Pointer to session's record in sessionStore
	lpTracker *list.Element
	// --

	// -- Set only for RPC sessions
	// reference to the cluster node where the session has originated
	rpcnode *ClusterNode
	// --

	// IP address of the client. For long polling this is the IP of the last poll
	remoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet
	userAgent string

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff)
	ver int

	// Device ID of the client
	deviceId string
	// Human language of the client
	lang string

	// ID of the current user or 0
	uid types.Uid

	// Authentication level - NONE (unset), ANON, AUTH, ROOT
	authLvl int

	// Time when the long polling session was last refreshed
	lastTouched time.Time

	// Time when the session received any packer from client
	lastAction time.Time

	// outbound mesages, buffered
	send chan []byte

	// channel for shutting down the session, buffer 1
	stop chan []byte

	// detach - channel for detaching session from topic, buffered
	detach chan string

	// Map of topic subscriptions, indexed by topic name
	subs map[string]*Subscription

	// Nodes to inform when the session is disconnected
	nodes map[string]bool

	// Session ID
	sid string

	// Needed for long polling
	rw sync.RWMutex
}

// Mapper of sessions to topics
type Subscription struct {
	// Channel to communicate with the topic, copy of Topic.broadcast
	broadcast chan<- *ServerComMessage

	// Session sends a signal to Topic when this session is unsubscribed
	// This is a copy of Topic.unreg
	done chan<- *sessionLeave

	// Channel to send {meta} requests, copy of Topic.meta
	meta chan<- *metaReq

	// Channel to ping topic with session's user agent
	uaChange chan<- string
}

// TODO(gene): unify simpleByteSender and QueueOut

// QueueOut attempts to send a ServerComMessage to a session; if the send buffer is full, timeout is 10 milliseconds
func (s *Session) queueOut(msg *ServerComMessage) {
	if s == nil {
		return
	}

	data, _ := json.Marshal(msg)
	select {
	case s.send <- data:
	case <-time.After(time.Millisecond * 10):
		log.Println("session.queueOut: timeout")
	}
}

// Message received, convert bytes to ClientComMessage and dispatch
func (s *Session) dispatchRaw(raw []byte) {
	var msg ClientComMessage

	log.Printf("Session.dispatch got '%s' from '%s'", raw, s.remoteAddr)

	if err := json.Unmarshal(raw, &msg); err != nil {
		// Malformed message
		log.Println("Session.dispatch: " + err.Error())
		s.queueOut(ErrMalformed("", "", time.Now().UTC().Round(time.Millisecond)))
		return
	}

	s.dispatch(&msg)
}

func (s *Session) dispatch(msg *ClientComMessage) {
	s.lastAction = time.Now().UTC().Round(time.Millisecond)

	msg.from = s.uid.UserId()
	msg.timestamp = s.lastAction

	// Locking-unlocking is needed for long polling: the client may issue multiple requests in parallel.
	// Should not affect performance
	if s.proto == LPOLL {
		s.rw.Lock()
		defer s.rw.Unlock()
	}

	switch {
	case msg.Pub != nil:
		s.publish(msg)
		log.Println("dispatch: Pub done")

	case msg.Sub != nil:
		s.subscribe(msg)
		log.Println("dispatch: Sub done")

	case msg.Leave != nil:
		s.leave(msg)
		log.Println("dispatch: Leave done")

	case msg.Hi != nil:
		s.hello(msg)
		log.Println("dispatch: Hi done")

	case msg.Login != nil:
		s.login(msg)
		log.Println("dispatch: Login done")

	case msg.Get != nil:
		s.get(msg)
		log.Println("dispatch: Get." + msg.Get.What + " done")

	case msg.Set != nil:
		s.set(msg)
		log.Println("dispatch: Set done")

	case msg.Del != nil:
		s.del(msg)
		log.Println("dispatch: Del." + msg.Del.What + " done")

	case msg.Acc != nil:
		s.acc(msg)
		log.Println("dispatch: Acc done")

	case msg.Note != nil:
		s.note(msg)
		log.Println("dispatch: Note." + msg.Note.What + " done")

	default:
		// Unknown message
		s.queueOut(ErrMalformed("", "", msg.timestamp))
		log.Println("Session.dispatch: unknown message")
	}

	// Notify 'me' topic that this session is currently active
	if msg.Leave == nil && (msg.Del == nil || msg.Del.What != "topic") {
		if sub, ok := s.subs[s.uid.UserId()]; ok {
			// The chan is buffered. If the buffer is exhaused, the session will wait for 'me' to become available
			sub.uaChange <- s.userAgent
		}
	}
}

// Request to subscribe to a topic
func (s *Session) subscribe(msg *ClientComMessage) {
	log.Printf("Sub to '%s' from '%s'", msg.Sub.Topic, msg.from)

	var topic, expanded string

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Sub.Id, msg.Sub.Topic, msg.timestamp))
		return
	}

	if strings.HasPrefix(msg.Sub.Topic, "new") {
		// Request to create a new named topic
		expanded = genTopicName()
		topic = expanded
	} else {
		var err *ServerComMessage
		expanded, err = s.validateTopicName(msg.Sub.Id, msg.Sub.Topic, msg.timestamp)
		if err != nil {
			s.queueOut(err)
			return
		}
	}

	if _, ok := s.subs[expanded]; ok {
		log.Printf("sess.subscribe: already subscribed to '%s'", expanded)
		s.queueOut(InfoAlreadySubscribed(msg.Sub.Id, topic, msg.timestamp))
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Sub.Id, topic, msg.timestamp))
		}
	} else {
		//log.Printf("Sub to'%s' (%s) from '%s' as '%s' -- OK!", expanded, msg.Sub.Topic, msg.from, topic)
		globals.hub.join <- &sessionJoin{topic: expanded, pkt: msg.Sub, sess: s}
		// Hub will send Ctrl success/failure packets back to session
	}
}

// Leave/Unsubscribe a topic
func (s *Session) leave(msg *ClientComMessage) {

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
		return
	}

	expanded, err := s.validateTopicName(msg.Leave.Id, msg.Leave.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	if sub, ok := s.subs[expanded]; ok {
		// Session is attached to the topic.
		if (msg.Leave.Topic == "me" || msg.Leave.Topic == "fnd") && msg.Leave.Unsub {
			// User should not unsubscribe from 'me' or 'find'. Just leaving is fine.
			s.queueOut(ErrPermissionDenied(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
		} else {
			// Unlink from topic, topic will send a reply.
			delete(s.subs, expanded)
			sub.done <- &sessionLeave{
				sess: s, unsub: msg.Leave.Unsub, topic: msg.Leave.Topic, reqId: msg.Leave.Id}
		}
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
		}
	} else if !msg.Leave.Unsub {
		// Session is not attached to the topic, wants to leave - fine, no change
		s.queueOut(InfoNotJoined(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
	} else {
		// Session wants to unsubscribe from the topic it did not join
		// FIXME(gene): allow topic to unsubscribe without joining first; send to hub to unsub
		s.queueOut(ErrAttachFirst(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
	}
}

// Broadcast a message to all topic subscribers
func (s *Session) publish(msg *ClientComMessage) {

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Pub.Id, msg.Pub.Topic, msg.timestamp))
		return
	}

	// TODO(gene): Check for repeated messages with the same ID

	expanded, err := s.validateTopicName(msg.Pub.Id, msg.Pub.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	data := &ServerComMessage{Data: &MsgServerData{
		Topic:     msg.Pub.Topic,
		From:      msg.from,
		Timestamp: msg.timestamp,
		Head:      msg.Pub.Head,
		Content:   msg.Pub.Content},
		rcptto: expanded, sessFrom: s, id: msg.Pub.Id, timestamp: msg.timestamp}
	if msg.Pub.NoEcho {
		data.skipSid = s.sid
	}

	if sub, ok := s.subs[expanded]; ok {
		// This is a post to a subscribed topic. The message is sent to the topic only
		sub.broadcast <- data
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Pub.Id, msg.Pub.Topic, msg.timestamp))
		}
	} else {
		// Publish request received without attaching to topic first.
		s.queueOut(ErrAttachFirst(msg.Pub.Id, msg.Pub.Topic, msg.timestamp))
	}
}

func parseVersion(vers string) int {
	dot := strings.Index(vers, ".")
	if dot < 0 {
		return 0
	}
	major, err := strconv.Atoi(vers[:dot])
	if err != nil || major < 0 || major >= 0xff {
		return 0
	}
	minor, err := strconv.Atoi(vers[dot+1:])
	if err != nil || minor < 0 || minor >= 0xff {
		return 0
	}
	return (major << 8) | minor
}

// Authenticate
func (s *Session) hello(msg *ClientComMessage) {

	if msg.Hi.Version == "" {
		s.queueOut(ErrMalformed(msg.Hi.Id, "", msg.timestamp))
		return
	}

	if s.ver == 0 {
		s.ver = parseVersion(msg.Hi.Version)
		if s.ver == 0 {
			s.queueOut(ErrMalformed(msg.Hi.Id, "", msg.timestamp))
			return
		}
		// Check version compatibility
		if MIN_SUPPORTED_VERSION_VAL > s.ver {
			s.ver = 0
			s.queueOut(ErrVersionNotSupported(msg.Hi.Id, "", msg.timestamp))
			return
		}
	} else {
		s.queueOut(ErrCommandOutOfSequence(msg.Hi.Id, "", msg.timestamp))
		return
	}

	s.userAgent = msg.Hi.UserAgent
	s.deviceId = msg.Hi.DeviceID
	s.lang = msg.Hi.Lang

	params := map[string]interface{}{"ver": VERSION, "build": buildstamp}
	var httpStatus int
	var httpStatusText string
	if s.proto == LPOLL {
		// In case of long polling StatusCreated was reported earlier.
		httpStatus = http.StatusOK
		httpStatusText = "ok"

	} else {
		httpStatus = http.StatusCreated
		httpStatusText = "created"
	}
	s.queueOut(&ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        msg.Hi.Id,
		Code:      httpStatus,
		Text:      httpStatusText,
		Params:    params,
		Timestamp: msg.timestamp}})
}

// Authenticate
func (s *Session) login(msg *ClientComMessage) {

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Login.Id, "", msg.timestamp))
		return
	}

	if !s.uid.IsZero() {
		s.queueOut(ErrAlreadyAuthenticated(msg.Login.Id, "", msg.timestamp))
		return
	}

	handler := store.GetAuthHandler(msg.Login.Scheme)
	if handler == nil {
		s.queueOut(ErrAuthUnknownScheme(msg.Login.Id, "", msg.timestamp))
		return
	}

	uid, authLvl, expires, authErr := handler.Authenticate(msg.Login.Secret)
	if authErr.IsError() {
		log.Println(authErr.Err)
	}

	if authErr.Code == auth.ErrMalformed {
		s.queueOut(ErrMalformed(msg.Login.Id, "", msg.timestamp))
		return
	}

	// DB error
	if authErr.Code == auth.ErrInternal {
		s.queueOut(ErrUnknown(msg.Login.Id, "", msg.timestamp))
		return
	}

	// All other errors are reported as invalid login or password
	if uid.IsZero() {
		s.queueOut(ErrAuthFailed(msg.Login.Id, "", msg.timestamp))
		return
	}

	s.uid = uid
	s.authLvl = authLvl

	if msg.Login.Scheme != "token" {
		handler = store.GetAuthHandler("token")
	}

	var tokenLifetime time.Duration
	if !expires.IsZero() {
		tokenLifetime = time.Until(expires)
	}
	secret, expires, authErr := handler.GenSecret(uid, authLvl, tokenLifetime)
	if authErr.IsError() {
		log.Println(authErr.Err)
		s.queueOut(ErrAuthFailed(msg.Login.Id, "", msg.timestamp))
		return
	}

	// Record deviceId used in this session
	if s.deviceId != "" {
		store.Devices.Update(uid, &types.DeviceDef{
			DeviceId: s.deviceId,
			Platform: "",
			LastSeen: msg.timestamp,
			Lang:     s.lang,
		})
	}

	s.queueOut(&ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        msg.Login.Id,
		Code:      http.StatusOK,
		Text:      http.StatusText(http.StatusOK),
		Timestamp: msg.timestamp,
		Params:    map[string]interface{}{"user": uid.UserId(), "token": secret, "expires": expires}}})
}

// Account creation
func (s *Session) acc(msg *ClientComMessage) {

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Acc.Id, "", msg.timestamp))
		return
	}

	// FIXME(gene): it should be possible to change Tags without stating the auth scheme
	authhdl := store.GetAuthHandler(msg.Acc.Scheme)
	if authhdl == nil {
		s.queueOut(ErrMalformed(msg.Acc.Id, "", msg.timestamp))
		return
	}

	if strings.HasPrefix(msg.Acc.User, "new") {
		// User cannot authenticate with the new account because the user is already authenticated
		if msg.Acc.Login && !s.uid.IsZero() {
			s.queueOut(ErrAlreadyAuthenticated(msg.Acc.Id, "", msg.timestamp))
			return
		}

		// Request to create a new account
		if ok, authErr := authhdl.IsUnique(msg.Acc.Secret); !ok {
			log.Println("Not unique: ", authErr.Err)
			if authErr.Code == auth.ErrDuplicate {
				s.queueOut(ErrDuplicateCredential(msg.Acc.Id, "", msg.timestamp))
			} else {
				s.queueOut(ErrUnknown(msg.Acc.Id, "", msg.timestamp))
			}
			return
		}

		var user types.User
		var private interface{}

		// Assign default access values in case the acc creator has not provided them
		user.Access.Auth = getDefaultAccess(types.TopicCat_P2P, true)
		user.Access.Anon = getDefaultAccess(types.TopicCat_P2P, false)

		if msg.Acc.Desc != nil {

			if msg.Acc.Desc.DefaultAcs != nil {
				if msg.Acc.Desc.DefaultAcs.Auth != "" {
					user.Access.Auth.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Auth))
					user.Access.Auth &= types.ModeCP2P
					if user.Access.Auth != types.ModeNone {
						user.Access.Auth |= types.ModeApprove
					}
				}
				if msg.Acc.Desc.DefaultAcs.Anon != "" {
					user.Access.Anon.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Anon))
					user.Access.Anon &= types.ModeCP2P
					if user.Access.Anon != types.ModeNone {
						user.Access.Anon |= types.ModeApprove
					}
				}
			}
			if !isNullValue(msg.Acc.Desc.Public) {
				user.Public = msg.Acc.Desc.Public
			}
			if !isNullValue(msg.Acc.Desc.Private) {
				private = msg.Acc.Desc.Private
			}
		}

		if msg.Acc.Tags != nil && len(msg.Acc.Tags) > 0 {
			tags := make([]string, 0, len(msg.Acc.Tags))
			if filterTags(&tags, msg.Acc.Tags) > 0 {
				user.Tags = tags
			}
		}

		if _, err := store.Users.Create(&user, private); err != nil {
			s.queueOut(ErrUnknown(msg.Acc.Id, "", msg.timestamp))
			return
		}

		var authLvl int
		if al, authErr := authhdl.AddRecord(user.Uid(), msg.Acc.Secret, 0); authErr.IsError() {
			log.Println(authErr.Err)
			// Attempt to delete incomplete user record
			store.Users.Delete(user.Uid(), false)
			s.queueOut(decodeAuthError(authErr.Code, msg.Acc.Id, msg.timestamp))
			return
		} else {
			authLvl = al
		}

		reply := NoErrCreated(msg.Acc.Id, "", msg.timestamp)
		params := map[string]interface{}{
			"user": user.Uid().UserId(),
		}

		params["desc"] = &MsgTopicDesc{
			CreatedAt: &user.CreatedAt,
			UpdatedAt: &user.UpdatedAt,
			DefaultAcs: &MsgDefaultAcsMode{
				Auth: user.Access.Auth.String(),
				Anon: user.Access.Anon.String()},
			Public:  user.Public,
			Private: private}

		if msg.Acc.Login {
			// User wants to use the new account for authentication. Generate token and resord session.

			s.uid = user.Uid()
			s.authLvl = authLvl

			params["authlvl"] = auth.AuthLevelName(authLvl)
			params["token"], params["expires"], _ = store.GetAuthHandler("token").GenSecret(s.uid, s.authLvl, 0)

			// Record session
			if s.deviceId != "" {
				store.Devices.Update(s.uid, &types.DeviceDef{
					DeviceId: s.deviceId,
					Platform: "",
					LastSeen: msg.timestamp,
					Lang:     s.lang,
				})
			}
		}

		reply.Ctrl.Params = params
		s.queueOut(reply)

	} else if !s.uid.IsZero() {
		// Request to update auth of an existing account. Only basic auth is currently supported
		// TODO(gene): support adding new auth schemes
		// TODO(gene): support the case when msg.Acc.User is not equal to the current user
		if authErr := authhdl.UpdateRecord(s.uid, msg.Acc.Secret, 0); authErr.IsError() {
			log.Println("failed to update credentials", authErr.Err)
			s.queueOut(decodeAuthError(authErr.Code, msg.Acc.Id, msg.timestamp))
			return
		}

		// TODO(gene): handle tags
		s.queueOut(NoErr(msg.Acc.Id, "", msg.timestamp))

	} else {
		// session is not authenticated and this is not an attempt to create a new account
		s.queueOut(ErrPermissionDenied(msg.Acc.Id, "", msg.timestamp))
		return
	}
}

func (s *Session) get(msg *ClientComMessage) {
	log.Println("s.get: processing 'get." + msg.Get.What + "'")

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Get.Id, msg.Get.Topic, msg.timestamp))
		return
	}

	// Validate topic name
	expanded, err := s.validateTopicName(msg.Get.Id, msg.Get.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	sub, ok := s.subs[expanded]
	meta := &metaReq{
		topic: expanded,
		pkt:   msg,
		sess:  s,
		what:  parseMsgClientMeta(msg.Get.What)}

	if meta.what == 0 {
		s.queueOut(ErrMalformed(msg.Get.Id, msg.Get.Topic, msg.timestamp))
		log.Println("s.get: invalid Get message action: '" + msg.Get.What + "'")
	} else if ok {
		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Get.Id, msg.Get.Topic, msg.timestamp))
		}
	} else {
		if (meta.what&constMsgMetaData != 0) || (meta.what&constMsgMetaSub != 0) {
			log.Println("s.get: invalid Get message action for hub routing: '" + msg.Get.What + "'")
			s.queueOut(ErrPermissionDenied(msg.Get.Id, msg.Get.Topic, msg.timestamp))
		} else {
			// Description of a topic not currently subscribed to. Request desc from the hub
			globals.hub.meta <- meta
		}
	}
}

func (s *Session) set(msg *ClientComMessage) {
	log.Println("s.set: processing 'set'")

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Set.Id, msg.Set.Topic, msg.timestamp))
		return
	}

	// Validate topic name
	expanded, err := s.validateTopicName(msg.Set.Id, msg.Set.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	if sub, ok := s.subs[expanded]; ok {
		meta := &metaReq{
			topic: expanded,
			pkt:   msg,
			sess:  s}

		if msg.Set.Desc != nil {
			meta.what = constMsgMetaDesc
		}
		if msg.Set.Sub != nil {
			meta.what |= constMsgMetaSub
		}
		if meta.what == 0 {
			s.queueOut(ErrMalformed(msg.Set.Id, msg.Set.Topic, msg.timestamp))
			log.Println("s.set: nil Set action")
		}

		log.Println("s.set: sending to topic")
		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Set.Id, msg.Set.Topic, msg.timestamp))
		}
	} else {
		log.Println("s.set: can Set for subscribed topics only")
		s.queueOut(ErrPermissionDenied(msg.Set.Id, msg.Set.Topic, msg.timestamp))
	}
}

func (s *Session) del(msg *ClientComMessage) {
	log.Println("s.del: processing 'del." + msg.Del.What + "'")

	if s.ver == 0 {
		s.queueOut(ErrCommandOutOfSequence(msg.Del.Id, msg.Del.Topic, msg.timestamp))
		return
	}

	// Validate topic name
	expanded, err := s.validateTopicName(msg.Del.Id, msg.Del.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	what := parseMsgClientDel(msg.Del.What)
	if what == 0 {
		s.queueOut(ErrMalformed(msg.Del.Id, msg.Del.Topic, msg.timestamp))
		log.Println("s.del: invalid Del action '" + msg.Del.What + "'")
	}

	sub, ok := s.subs[expanded]
	if ok && what != constMsgDelTopic {
		// Session is attached, deleting subscription or messages. Send to topic.
		log.Println("s.del: sending to topic")
		sub.meta <- &metaReq{
			topic: expanded,
			pkt:   msg,
			sess:  s,
			what:  what}

	} else if !ok && globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Del.Id, msg.Del.Topic, msg.timestamp))
		}
	} else if what == constMsgDelTopic {
		// Deleting topic: for sessions attached or not attached, send request to hub first.
		// Hub will forward to topic, if appropriate.
		globals.hub.unreg <- &topicUnreg{
			topic: expanded,
			msg:   msg.Del,
			sess:  s,
			del:   true}
	} else {
		// Must join the topic to delete messages or subscriptions.
		s.queueOut(ErrAttachFirst(msg.Del.Id, msg.Del.Topic, msg.timestamp))
		log.Println("s.del: invalid Del action while unsubbed '" + msg.Del.What + "'")
	}
}

// Broadcast a transient {ping} message to active topic subscribers
// Not reporting any errors
func (s *Session) note(msg *ClientComMessage) {

	if s.ver == 0 {
		return
	}

	expanded, err := s.validateTopicName("", msg.Note.Topic, msg.timestamp)
	if err != nil {
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

	if sub, ok := s.subs[expanded]; ok {
		// Pings can be sent to subscribed topics only
		sub.broadcast <- &ServerComMessage{Info: &MsgServerInfo{
			Topic: msg.Note.Topic,
			From:  s.uid.UserId(),
			What:  msg.Note.What,
			SeqId: msg.Note.SeqId,
		}, rcptto: expanded, timestamp: msg.timestamp, skipSid: s.sid}
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		globals.cluster.routeToTopic(msg, expanded, s)
	}
}

// validateTopicName expands session specific topic name to global name
// Returns
//   topic: session-specific topic name the message recepient should see
//   routeTo: routable global topic name
//   err: *ServerComMessage with an error to return to the sender
func (s *Session) validateTopicName(msgId, topic string, timestamp time.Time) (string, *ServerComMessage) {

	if topic == "" {
		return "", ErrMalformed(msgId, "", timestamp)
	}

	if !strings.HasPrefix(topic, "grp") && s.uid.IsZero() {
		// me, fnd, p2p topics require authentication
		return "", ErrAuthRequired(msgId, topic, timestamp)
	}

	// Topic to route to i.e. rcptto: or s.subs[routeTo]
	routeTo := topic

	if topic == "me" {
		routeTo = s.uid.UserId()
	} else if topic == "fnd" {
		routeTo = s.uid.FndName()
	} else if strings.HasPrefix(topic, "usr") {
		// p2p topic
		uid2 := types.ParseUserId(topic)
		if uid2.IsZero() {
			// Ensure the user id is valid
			return "", ErrMalformed(msgId, topic, timestamp)
		} else if uid2 == s.uid {
			// Use 'me' to access self-topic
			return "", ErrPermissionDenied(msgId, topic, timestamp)
		}
		routeTo = s.uid.P2PName(uid2)
	}

	return routeTo, nil
}

func filterTags(dst *[]string, src []string) int {
	if globals.indexableTags == nil || len(globals.indexableTags) == 0 {
		return 0
	}

	for _, s := range src {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) < 2 {
			continue
		}
		if parts[1] = strings.Trim(parts[1], " "); parts[1] == "" {
			continue
		}
		parts[0] = strings.ToLower(parts[0])
		for _, tag := range globals.indexableTags {
			if parts[0] == tag {
				*dst = append(*dst, s)
			}
		}
	}
	return len(*dst)
}

func decodeAuthError(code int, id string, timestamp time.Time) *ServerComMessage {
	var errmsg *ServerComMessage
	switch code {
	case auth.NoErr:
		errmsg = NoErr(id, "", timestamp)
	case auth.InfoNotModified:
		errmsg = InfoNotModified(id, "", timestamp)
	case auth.ErrInternal:
		errmsg = ErrUnknown(id, "", timestamp)
	case auth.ErrMalformed:
		errmsg = ErrMalformed(id, "", timestamp)
	case auth.ErrFailed:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrDuplicate:
		errmsg = ErrDuplicateCredential(id, "", timestamp)
	case auth.ErrUnsupported:
		errmsg = ErrNotImplemented(id, "", timestamp)
	case auth.ErrExpired:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrPolicy:
		errmsg = ErrPolicy(id, "", timestamp)
	default:
		errmsg = ErrUnknown(id, "", timestamp)
	}
	return errmsg
}
