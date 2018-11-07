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
)

// Wire transport
const (
	NONE = iota
	WEBSOCK
	LPOLL
	GRPC
	CLUSTER
)

var minSupportedVersionValue = parseVersion(minSupportedVersion)

// Session represents a single WS connection or a long polling session. A user may have multiple
// sessions.
type Session struct {
	// protocol - NONE (unset), WEBSOCK, LPOLL, CLUSTER, GRPC
	proto int

	// Websocket. Set only for websocket sessions
	ws *websocket.Conn

	// Pointer to session's record in sessionStore. Set only for Long Poll sessions
	lpTracker *list.Element

	// gRPC handle. Set only for gRPC clients
	grpcnode pbx.Node_MessageLoopServer

	// Reference to the cluster node where the session has originated. Set only for cluster RPC sessions
	clnode *ClusterNode

	// IP address of the client. For long polling this is the IP of the last poll
	remoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet
	userAgent string

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff)
	ver int

	// Device ID of the client
	deviceID string
	// Platform: web, ios, android
	platf string
	// Human language of the client
	lang string

	// ID of the current user or 0
	uid types.Uid

	// Authentication level - NONE (unset), ANON, AUTH, ROOT
	authLvl auth.Level

	// Time when the long polling session was last refreshed
	lastTouched time.Time

	// Time when the session received any packer from client
	lastAction time.Time

	// Outbound mesages, buffered.
	// The content must be serialized in format suitable for the session.
	send chan interface{}

	// Channel for shutting down the session, buffer 1.
	// Content in the same format as for 'send'
	stop chan interface{}

	// detach - channel for detaching session from topic, buffered
	detach chan string

	// Map of topic subscriptions, indexed by topic name.
	// Don't access directly. Use getters/setters.
	subs map[string]*Subscription
	// Mutex for subs access: both topic go routines and network go routines access
	// subs concurrently.
	subsLock sync.RWMutex

	// Cluster nodes to inform when the session is disconnected
	nodes map[string]bool

	// Session ID
	sid string

	// Needed for long polling and grpc.
	lock sync.Mutex
}

// Subscription is a mapper of sessions to topics.
type Subscription struct {
	// Root's session may have multiple subscriptions to topic on behalf of other users.
	// This is the use counter.
	count int

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

func (s *Session) addSub(topic string, sub *Subscription) {
	s.subsLock.Lock()
	defer s.subsLock.Unlock()

	if xsub, ok := s.subs[topic]; ok {
		xsub.count++
	} else {
		sub.count = 1
		s.subs[topic] = sub
	}
}

func (s *Session) getSub(topic string) *Subscription {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()

	return s.subs[topic]
}

func (s *Session) delSub(topic string) {
	s.subsLock.Lock()
	defer s.subsLock.Unlock()

	if xsub, ok := s.subs[topic]; ok {
		if xsub.count <= 1 {
			delete(s.subs, topic)
		} else {
			xsub.count--
		}
	}
}

// Inform topics that the session is being terminated.
// sessionLeave.userId is not set because the whole session is being dropped.
func (s *Session) unsubAll() {
	s.subsLock.RLock()
	defer s.subsLock.RUnlock()

	for _, sub := range s.subs {
		// sub.done is the same as topic.unreg
		sub.done <- &sessionLeave{sess: s}
	}
}

// queueOut attempts to send a ServerComMessage to a session; if the send buffer is full, timeout is 50 usec
func (s *Session) queueOut(msg *ServerComMessage) bool {
	if s == nil {
		return true
	}

	select {
	case s.send <- s.serialize(msg):
	case <-time.After(time.Microsecond * 50):
		log.Println("s.queueOut: timeout", s.sid)
		return false
	}
	return true
}

// queueOutBytes attempts to send a ServerComMessage already serialized to []byte.
// If the send buffer is full, timeout is 50 usec
func (s *Session) queueOutBytes(data []byte) bool {
	if s == nil {
		return true
	}

	select {
	case s.send <- data:
	case <-time.After(time.Microsecond * 50):
		log.Println("s.queueOutBytes: timeout", s.sid)
		return false
	}
	return true
}

func (s *Session) cleanUp(expired bool) {
	if !expired {
		globals.sessionStore.Delete(s)
	}
	globals.cluster.sessionGone(s)
	s.unsubAll()
}

// Message received, convert bytes to ClientComMessage and dispatch
func (s *Session) dispatchRaw(raw []byte) {
	var msg ClientComMessage

	toLog := raw
	truncated := ""
	if len(raw) > 512 {
		toLog = raw[:512]
		truncated = "<...>"
	}
	log.Printf("in: '%s%s' ip='%s' sid='%s' uid='%s'", toLog, truncated, s.remoteAddr, s.sid, s.uid)

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

	if msg.from == "" {
		msg.from = s.uid.UserId()
		msg.authLvl = int(s.authLvl)
	} else if s.authLvl != auth.LevelRoot {
		// Only root user can set non-default msg.from && msg.authLvl values.
		s.queueOut(ErrPermissionDenied("", "", msg.timestamp))
		log.Println("s.dispatch: non-root asigned msg.from", s.sid)
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
				s.queueOut(ErrCommandOutOfSequence(m.id, m.topic, m.timestamp))
				return
			}
			handler(m)
		}
	}

	// Check if user is logged in
	checkUser := func(m *ClientComMessage, handler func(*ClientComMessage)) func(*ClientComMessage) {
		return func(m *ClientComMessage) {
			if msg.from == "" {
				log.Println("s.dispatch: authentication required", s.sid)
				s.queueOut(ErrAuthRequired(m.id, m.topic, msg.timestamp))
				return
			}
			handler(m)
		}
	}

	switch {
	case msg.Pub != nil:
		handler = checkVers(msg, checkUser(msg, s.publish))
		msg.id = msg.Pub.Id
		msg.topic = msg.Pub.Topic
		uaRefresh = true

	case msg.Sub != nil:
		handler = checkVers(msg, checkUser(msg, s.subscribe))
		msg.id = msg.Sub.Id
		msg.topic = msg.Sub.Topic
		uaRefresh = true

	case msg.Leave != nil:
		handler = checkVers(msg, checkUser(msg, s.leave))
		msg.id = msg.Leave.Id
		msg.topic = msg.Leave.Topic

	case msg.Hi != nil:
		handler = s.hello
		msg.id = msg.Hi.Id

	case msg.Login != nil:
		handler = checkVers(msg, s.login)
		msg.id = msg.Login.Id

	case msg.Get != nil:
		handler = checkVers(msg, checkUser(msg, s.get))
		msg.id = msg.Get.Id
		msg.topic = msg.Get.Topic
		uaRefresh = true

	case msg.Set != nil:
		handler = checkVers(msg, checkUser(msg, s.set))
		msg.id = msg.Set.Id
		msg.topic = msg.Set.Topic
		uaRefresh = true

	case msg.Del != nil:
		handler = checkVers(msg, checkUser(msg, s.del))
		msg.id = msg.Del.Id
		msg.topic = msg.Del.Topic

	case msg.Acc != nil:
		handler = checkVers(msg, s.acc)
		msg.id = msg.Acc.Id

	case msg.Note != nil:
		handler = s.note
		msg.topic = msg.Note.Topic
		uaRefresh = true

	default:
		// Unknown message
		s.queueOut(ErrMalformed("", "", msg.timestamp))
		log.Println("s.dispatch: unknown message", s.sid)
		return
	}

	handler(msg)

	// Notify 'me' topic that this session is currently active
	if uaRefresh && msg.from != "" && s.userAgent != "" {
		if sub := s.getSub(msg.from); sub != nil {
			// The chan is buffered. If the buffer is exhaused, the session will wait for 'me' to become available
			sub.uaChange <- s.userAgent
		}
	}
}

// Request to subscribe to a topic
func (s *Session) subscribe(msg *ClientComMessage) {
	var expanded string
	if strings.HasPrefix(msg.topic, "new") {
		// Request to create a new named topic
		expanded = genTopicName()
		// msg.topic = expanded
	} else {
		var resp *ServerComMessage
		expanded, resp = s.expandTopicName(msg)
		if resp != nil {
			s.queueOut(resp)
			return
		}
	}

	if sub := s.getSub(expanded); sub != nil {
		log.Println("s.subscribe: already subscribed to topic=", expanded, s.sid)
		s.queueOut(InfoAlreadySubscribed(msg.id, msg.topic, msg.timestamp))
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			log.Println("s.subscribe:", err, s.sid)
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else {
		globals.hub.join <- &sessionJoin{
			topic: expanded,
			pkt:   msg,
			sess:  s}
		// Hub will send Ctrl success/failure packets back to session
	}
}

// Leave/Unsubscribe a topic
func (s *Session) leave(msg *ClientComMessage) {
	// Expand topic name
	expanded, resp := s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	if sub := s.getSub(expanded); sub != nil {
		// Session is attached to the topic.
		if (msg.topic == "me" || msg.topic == "fnd") && msg.Leave.Unsub {
			// User should not unsubscribe from 'me' or 'find'. Just leaving is fine.
			s.queueOut(ErrPermissionDenied(msg.id, msg.topic, msg.timestamp))
		} else {
			// Unlink from topic, topic will send a reply.
			s.delSub(expanded)
			sub.done <- &sessionLeave{
				userId: types.ParseUserId(msg.from),
				topic:  msg.topic,
				sess:   s,
				unsub:  msg.Leave.Unsub,
				reqID:  msg.id}
		}
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			log.Println("s.leave:", err, s.sid)
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else if !msg.Leave.Unsub {
		// Session is not attached to the topic, wants to leave - fine, no change
		s.queueOut(InfoNotJoined(msg.id, msg.topic, msg.timestamp))
	} else {
		// Session wants to unsubscribe from the topic it did not join
		// FIXME(gene): allow topic to unsubscribe without joining first; send to hub to unsub
		log.Println("s.leave:", "must attach first", s.sid)
		s.queueOut(ErrAttachFirst(msg.id, msg.topic, msg.timestamp))
	}
}

// Broadcast a message to all topic subscribers
func (s *Session) publish(msg *ClientComMessage) {
	// TODO(gene): Check for repeated messages with the same ID
	expanded, resp := s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	// Add "sender" header if the message is sent on behalf of another user.
	if msg.from != s.uid.UserId() {
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
		Topic:     msg.topic,
		From:      msg.from,
		Timestamp: msg.timestamp,
		Head:      msg.Pub.Head,
		Content:   msg.Pub.Content},
		// Unroutable values.
		rcptto:    expanded,
		sess:      s,
		id:        msg.id,
		timestamp: msg.timestamp,
		from:      msg.from}
	if msg.Pub.NoEcho {
		data.skipSid = s.sid
	}

	if sub := s.getSub(expanded); sub != nil {
		// This is a post to a subscribed topic. The message is sent to the topic only
		sub.broadcast <- data
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			log.Println("s.publish:", err, s.sid)
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else {
		// Publish request received without attaching to topic first.
		s.queueOut(ErrAttachFirst(msg.id, msg.topic, msg.timestamp))
		log.Println("s.publish:", "must attach first", s.sid)
	}
}

// Client metadata
func (s *Session) hello(msg *ClientComMessage) {
	var params map[string]interface{}

	if s.ver == 0 {
		s.ver = parseVersion(msg.Hi.Version)
		if s.ver == 0 {
			log.Println("s.hello:", "failed to parse version", s.sid)
			s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
			return
		}
		// Check version compatibility
		if versionCompare(s.ver, minSupportedVersionValue) < 0 {
			s.ver = 0
			s.queueOut(ErrVersionNotSupported(msg.id, "", msg.timestamp))
			log.Println("s.hello:", "unsupported version", s.sid)
			return
		}
		params = map[string]interface{}{"ver": currentVersion, "build": store.GetAdapterName() + ":" + buildstamp}

		// Set ua & platform in the beginning of the session.
		// Don't change them later.
		s.userAgent = msg.Hi.UserAgent
		s.platf = msg.Hi.Platform
		if s.platf == "" {
			s.platf = platformFromUA(msg.Hi.UserAgent)
		}
	} else if msg.Hi.Version == "" || parseVersion(msg.Hi.Version) == s.ver {
		// Save changed device ID or Lang. Platform cannot be changed.
		if !s.uid.IsZero() {
			if err := store.Devices.Update(s.uid, s.deviceID, &types.DeviceDef{
				DeviceId: msg.Hi.DeviceID,
				Platform: s.platf,
				LastSeen: msg.timestamp,
				Lang:     msg.Hi.Lang,
			}); err != nil {
				log.Println("s.hello:", "database error", err, s.sid)
				s.queueOut(ErrUnknown(msg.id, "", msg.timestamp))
				return
			}
		}
	} else {
		// Version cannot be changed mid-session.
		s.queueOut(ErrCommandOutOfSequence(msg.id, "", msg.timestamp))
		log.Println("s.hello:", "version cannot be changed", s.sid)
		return
	}

	s.deviceID = msg.Hi.DeviceID
	s.lang = msg.Hi.Lang

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

	ctrl := &MsgServerCtrl{Id: msg.id, Code: httpStatus, Text: httpStatusText, Timestamp: msg.timestamp}
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

	authhdl := store.GetLogicalAuthHandler(msg.Acc.Scheme)
	if strings.HasPrefix(msg.Acc.User, "new") {
		// New account

		// The session cannot authenticate with the new account because  it's already authenticated.
		if msg.Acc.Login && (!s.uid.IsZero() || rec != nil) {
			s.queueOut(ErrAlreadyAuthenticated(msg.id, "", msg.timestamp))
			log.Println("s.acc: login requested while already authenticated", s.sid)
			return
		}

		if authhdl == nil {
			// New accounts must have an authentication scheme
			s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
			log.Println("s.acc: unknown auth handler", s.sid)
			return
		}

		// Check if login is unique.
		if ok, err := authhdl.IsUnique(msg.Acc.Secret); !ok {
			log.Println("s.acc: auth secret is not unique", err, s.sid)
			s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp,
				map[string]interface{}{"what": "auth"}))
			return
		}

		var user types.User
		var private interface{}

		// Assign default access values in case the acc creator has not provided them
		user.Access.Auth = getDefaultAccess(types.TopicCatP2P, true)
		user.Access.Anon = getDefaultAccess(types.TopicCatP2P, false)

		if tags := normalizeTags(msg.Acc.Tags); tags != nil {
			if !restrictedTagsEqual(tags, nil, globals.immutableTagNS) {
				log.Println("a.acc: attempt to directly assign restricted tags", s.sid)
				msg := ErrPermissionDenied(msg.id, "", msg.timestamp)
				msg.Ctrl.Params = map[string]interface{}{"what": "tags"}
				s.queueOut(msg)
				return
			}
			user.Tags = tags
		}

		// Pre-check credentials for validity. We don't know user's access level
		// consequently cannot check presence of required credentials. Must do that later.
		creds := normalizeCredentials(msg.Acc.Cred, true)
		for i := range creds {
			cr := &creds[i]
			vld := store.GetValidator(cr.Method)
			if err := vld.PreCheck(cr.Value, cr.Params); err != nil {
				log.Println("a.acc: failed credential pre-check", cr, err, s.sid)
				s.queueOut(decodeStoreError(err, msg.Acc.Id, "", msg.timestamp,
					map[string]interface{}{"what": cr.Method}))
				return
			}

			if globals.validators[cr.Method].addToTags {
				user.Tags = append(user.Tags, cr.Method+":"+cr.Value)
			}
		}

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

		if _, err := store.Users.Create(&user, private); err != nil {
			log.Println("a.acc: failed to create user", err, s.sid)
			s.queueOut(ErrUnknown(msg.id, "", msg.timestamp))
			return
		}

		rec, err := authhdl.AddRecord(&auth.Rec{Uid: user.Uid()}, msg.Acc.Secret)
		if err != nil {
			log.Println("s.acc: add auth record failed", err, s.sid)
			// Attempt to delete incomplete user record
			store.Users.Delete(user.Uid(), false)
			s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
			return
		}

		// When creating an account, the user must provide all required credentials.
		// If any are missing, reject the request.
		if len(creds) < len(globals.authValidators[rec.AuthLevel]) {
			log.Println("s.acc: missing credentials; have:", creds, "want:", globals.authValidators[rec.AuthLevel], s.sid)
			// Attempt to delete incomplete user record
			store.Users.Delete(user.Uid(), false)
			_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], credentialMethods(creds))
			s.queueOut(decodeStoreError(types.ErrPolicy, msg.id, "", msg.timestamp,
				map[string]interface{}{"creds": missing}))
			return
		}

		var validated []string
		tmpToken, _, _ := store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
			Uid:       user.Uid(),
			AuthLevel: auth.LevelNone,
			Lifetime:  time.Hour * 24,
			Features:  auth.FeatureNoLogin})
		for i := range creds {
			cr := &creds[i]
			vld := store.GetValidator(cr.Method)
			if err := vld.Request(user.Uid(), cr.Value, s.lang, cr.Response, tmpToken); err != nil {
				log.Println("s.acc: failed to save or validate credential", err, s.sid)
				// Delete incomplete user record.
				store.Users.Delete(user.Uid(), false)
				s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp,
					map[string]interface{}{"what": cr.Method}))
				return
			}

			if cr.Response != "" {
				// If response is provided and Request did not return an error, the request was
				// successfully validated.
				validated = append(validated, cr.Method)
			}
		}

		var reply *ServerComMessage
		if msg.Acc.Login {
			// Process user's login request.
			_, missing := stringSliceDelta(globals.authValidators[rec.AuthLevel], validated)
			reply = s.onLogin(msg.id, msg.timestamp, rec, missing)
		} else {
			// Not using the new account for logging in.
			reply = NoErrCreated(msg.id, "", msg.timestamp)
			reply.Ctrl.Params = map[string]interface{}{"user": user.Uid().UserId()}
		}
		params := reply.Ctrl.Params.(map[string]interface{})
		params["desc"] = &MsgTopicDesc{
			CreatedAt: &user.CreatedAt,
			UpdatedAt: &user.UpdatedAt,
			DefaultAcs: &MsgDefaultAcsMode{
				Auth: user.Access.Auth.String(),
				Anon: user.Access.Anon.String()},
			Public:  user.Public,
			Private: private}

		s.queueOut(reply)

		pluginAccount(&user, plgActCreate)

	} else {
		// Existing account.

		if s.uid.IsZero() && rec == nil {
			// Session is not authenticated and no token provided.
			log.Println("s.acc: not a new account and not authenticated", s.sid)
			s.queueOut(ErrPermissionDenied(msg.id, "", msg.timestamp))
			return
		} else if msg.from != "" && rec != nil {
			// Two UIDs: one from msg.from, one from token. Ambigous, reject.
			log.Println("s.acc: got both authenticated session and token", s.sid)
			s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
			return
		}

		userId := msg.from
		authLvl := auth.Level(msg.authLvl)
		if rec != nil {
			userId = rec.Uid.UserId()
			authLvl = rec.AuthLevel
		}
		if msg.Acc.User != "" && msg.Acc.User != userId {
			if s.authLvl != auth.LevelRoot {
				log.Println("s.acc: attempt to change another's account by non-root", s.sid)
				s.queueOut(ErrPermissionDenied(msg.id, "", msg.timestamp))
				return
			}
			// Root is editing someone else's account.
			userId = msg.Acc.User
			authLvl = auth.ParseAuthLevel(msg.Acc.AuthLevel)
		}

		uid := types.ParseUserId(userId)
		if uid.IsZero() || authLvl == auth.LevelNone {
			// Either msg.Acc.User or msg.Acc.AuthLevel contains invalid data.
			s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
			log.Println("s.acc: either user id or auth level is missing", s.sid)
			return
		}

		var params map[string]interface{}
		if authhdl != nil {
			// Request to update auth of an existing account. Only basic auth is currently supported
			// TODO(gene): support adding new auth schemes
			if err := authhdl.UpdateRecord(&auth.Rec{Uid: uid}, msg.Acc.Secret); err != nil {
				log.Println("s.acc: failed to update auth secret", err, s.sid)
				s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
				return
			}
		} else if msg.Acc.Scheme != "" {
			// Invalid or unknown auth scheme
			log.Println("s.acc: unknown auth scheme", msg.Acc.Scheme, s.sid)
			s.queueOut(ErrMalformed(msg.id, "", msg.timestamp))
			return
		} else if len(msg.Acc.Cred) > 0 {
			// Use provided credentials for validation.
			validated, err := s.getValidatedGred(uid, authLvl, msg.Acc.Cred)
			if err != nil {
				log.Println("s.acc: failed to get validated credentials", err, s.sid)
				s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
				return
			}
			_, missing := stringSliceDelta(globals.authValidators[authLvl], validated)
			if len(missing) > 0 {
				params = map[string]interface{}{"cred": missing}
			}
		}

		resp := NoErr(msg.id, "", msg.timestamp)
		resp.Ctrl.Params = params
		s.queueOut(resp)

		// TODO: Call plugin with the account update
		// like pluginAccount(&types.User{}, plgActUpd)

	}
}

// Authenticate
func (s *Session) login(msg *ClientComMessage) {
	// msg.from is ignored here

	if msg.Login.Scheme == "reset" {
		s.queueOut(decodeStoreError(s.authSecretReset(msg.Login.Secret), msg.Login.Id, "", msg.timestamp, nil))
		return
	}

	if !s.uid.IsZero() {
		s.queueOut(ErrAlreadyAuthenticated(msg.id, "", msg.timestamp))
		return
	}

	handler := store.GetLogicalAuthHandler(msg.Login.Scheme)
	if handler == nil {
		log.Println("Unknown authentication scheme", msg.Login.Scheme)
		s.queueOut(ErrAuthUnknownScheme(msg.id, "", msg.timestamp))
		return
	}

	rec, challenge, err := handler.Authenticate(msg.Login.Secret)
	if err != nil {
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
		return
	}

	if challenge != nil {
		// Multi-stage authentication. Issue challenge to the client.
		s.queueOut(InfoChallenge(msg.id, msg.timestamp, challenge))
		return
	}

	var missing []string
	if rec.Features&auth.FeatureValidated == 0 {
		missing, err = s.getValidatedGred(rec.Uid, rec.AuthLevel, msg.Login.Cred)
		if err == nil {
			_, missing = stringSliceDelta(globals.authValidators[rec.AuthLevel], missing)
		}
	}
	if err != nil {
		log.Println("failed to validate credentials", err)
		s.queueOut(decodeStoreError(err, msg.id, "", msg.timestamp, nil))
	} else {
		s.queueOut(s.onLogin(msg.id, msg.timestamp, rec, missing))
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

	// Tehnically we don't need to check it here, but we are going to mail the 'authName' string to the user.
	// We have to make sure it does not contain any exploits. This is the simplest check.
	if hdl := store.GetLogicalAuthHandler(authScheme); hdl == nil {
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

	token, _, err := store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
		Uid:       uid,
		AuthLevel: auth.LevelNone,
		Lifetime:  time.Hour * 24,
		Features:  auth.FeatureNoLogin})

	if err != nil {
		return err
	}
	log.Println("calling validator.ResetSecret", credValue, authScheme, uid)
	return validator.ResetSecret(credValue, authScheme, s.lang, token)
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
		}
		features |= auth.FeatureValidated

		if len(rec.Tags) > 0 {
			if err := store.Users.Update(rec.Uid,
				map[string]interface{}{"Tags": normalizeTags(rec.Tags)}); err != nil {

				log.Println("failed to update user's tags", err)
			}
		}

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

// Get a list of all validated credentials including those validated in this call.
func (s *Session) getValidatedGred(uid types.Uid, authLvl auth.Level, creds []MsgAccCred) ([]string, error) {

	// Check if credential validation is required.
	if len(globals.authValidators[authLvl]) == 0 {
		return nil, nil
	}

	allCred, err := store.Users.GetAllCred(uid)
	if err != nil {
		return nil, err
	}

	// Compile a list of validated credentials.
	var validated []string
	for _, cr := range allCred {
		if cr.Done {
			validated = append(validated, cr.Method)
		}
	}

	// Add credentials which are validated in this call.
	// Unknown validators are removed.
	creds = normalizeCredentials(creds, false)
	for i := range creds {
		cr := &creds[i]
		if cr.Response == "" {
			// Ignore unknown validation type or empty response.
			continue
		}
		vld := store.GetValidator(cr.Method)
		if err := vld.Check(uid, cr.Response); err != nil {
			// Check failed.
			if storeErr, ok := err.(types.StoreError); ok && storeErr == types.ErrCredentials {
				// Just an invalid response. Keep credential unvalidated.
				continue
			}
			// Actual error. Report back.
			return nil, err
		}
		// Check did not return an error: the request was successfully validated.
		validated = append(validated, cr.Method)
	}

	return validated, nil
}

func (s *Session) get(msg *ClientComMessage) {
	// Expand topic name.
	expanded, resp := s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	sub := s.getSub(expanded)
	meta := &metaReq{
		topic: expanded,
		pkt:   msg,
		sess:  s,
		what:  parseMsgClientMeta(msg.Get.What)}

	if meta.what == 0 {
		s.queueOut(ErrMalformed(msg.id, msg.topic, msg.timestamp))
		log.Println("s.get: invalid Get message action", msg.Get.What)
	} else if sub != nil {
		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else if meta.what&(constMsgMetaData|constMsgMetaSub|constMsgMetaDel) != 0 {
		log.Println("s.get: subscribe first to get=", msg.Get.What)
		s.queueOut(ErrPermissionDenied(msg.id, msg.topic, msg.timestamp))
	} else {
		// Description of a topic not currently subscribed to. Request desc from the hub
		globals.hub.meta <- meta
	}
}

func (s *Session) set(msg *ClientComMessage) {
	// Expand topic name.
	expanded, resp := s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	if sub := s.getSub(expanded); sub != nil {
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
		if msg.Set.Tags != nil {
			meta.what |= constMsgMetaTags
		}
		if meta.what == 0 {
			s.queueOut(ErrMalformed(msg.id, msg.topic, msg.timestamp))
			log.Println("s.set: nil Set action")
		}

		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else {
		log.Println("s.set: can Set for subscribed topics only")
		s.queueOut(ErrPermissionDenied(msg.id, msg.topic, msg.timestamp))
	}
}

func (s *Session) del(msg *ClientComMessage) {

	// Expand topic name and validate request.
	expanded, resp := s.expandTopicName(msg)
	if resp != nil {
		s.queueOut(resp)
		return
	}

	what := parseMsgClientDel(msg.Del.What)
	if what == 0 {
		s.queueOut(ErrMalformed(msg.id, msg.topic, msg.timestamp))
		log.Println("s.del: invalid Del action", msg.Del.What)
	}

	sub := s.getSub(expanded)
	if sub != nil && what != constMsgDelTopic {
		// Session is attached, deleting subscription or messages. Send to topic.
		sub.meta <- &metaReq{
			topic: expanded,
			pkt:   msg,
			sess:  s,
			what:  what}

	} else if sub == nil && globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.id, msg.topic, msg.timestamp))
		}
	} else if what == constMsgDelTopic {
		// Deleting topic: for sessions attached or not attached, send request to hub first.
		// Hub will forward to topic, if appropriate.
		globals.hub.unreg <- &topicUnreg{
			topic: expanded,
			pkt:   msg,
			sess:  s,
			del:   true}
	} else {
		// Must join the topic to delete messages or subscriptions.
		s.queueOut(ErrAttachFirst(msg.id, msg.topic, msg.timestamp))
		log.Println("s.del: invalid Del action while unsubbed", msg.Del.What)
	}
}

// Broadcast a transient {ping} message to active topic subscribers
// Not reporting any errors
func (s *Session) note(msg *ClientComMessage) {

	if s.ver == 0 || msg.from == "" {
		// Silently ignore the message: have not received {hi} or don't know who sent the message.
		return
	}

	// Expand topic name and validate request.
	expanded, resp := s.expandTopicName(msg)
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

	if sub := s.getSub(expanded); sub != nil {
		// Pings can be sent to subscribed topics only
		sub.broadcast <- &ServerComMessage{Info: &MsgServerInfo{
			Topic: msg.topic,
			From:  msg.from,
			What:  msg.Note.What,
			SeqId: msg.Note.SeqId,
		}, rcptto: expanded, timestamp: msg.timestamp, skipSid: s.sid}
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		globals.cluster.routeToTopic(msg, expanded, s)
	}
}

// expandTopicName expands session specific topic name to global name
// Returns
//   topic: session-specific topic name the message recipient should see
//   routeTo: routable global topic name
//   err: *ServerComMessage with an error to return to the sender
func (s *Session) expandTopicName(msg *ClientComMessage) (string, *ServerComMessage) {

	if msg.topic == "" {
		log.Println("s.etn: empty topic name", s.sid)
		return "", ErrMalformed(msg.id, "", msg.timestamp)
	}

	// Expanded name of the topic to route to i.e. rcptto: or s.subs[routeTo]
	var routeTo string
	if msg.topic == "me" {
		routeTo = msg.from
	} else if msg.topic == "fnd" {
		routeTo = types.ParseUserId(msg.from).FndName()
	} else if strings.HasPrefix(msg.topic, "usr") {
		// p2p topic
		uid1 := types.ParseUserId(msg.from)
		uid2 := types.ParseUserId(msg.topic)
		if uid2.IsZero() {
			// Ensure the user id is valid
			log.Println("s.etn: failed to parse p2p topic name", s.sid)
			return "", ErrMalformed(msg.id, msg.topic, msg.timestamp)
		} else if uid2 == uid1 {
			// Use 'me' to access self-topic
			log.Println("s.etn: invalid p2p self-subscription", s.sid)
			return "", ErrPermissionDenied(msg.id, msg.topic, msg.timestamp)
		}
		routeTo = uid1.P2PName(uid2)
	} else {
		routeTo = msg.topic
	}

	return routeTo, nil
}

// SerialFormat is an enum of possible serialization formats.
type SerialFormat int

const (
	// FmtNONE undefined format
	FmtNONE SerialFormat = iota
	// FmtJSON JSON format
	FmtJSON
	// FmtPROTO Protobuffer format
	FmtPROTO
)

func (s *Session) getSerialFormat() SerialFormat {
	if s.proto == GRPC {
		return FmtPROTO
	}
	return FmtJSON
}

func (s *Session) serialize(msg *ServerComMessage) interface{} {
	if s.proto == GRPC {
		return pbServSerialize(msg)
	}
	out, _ := json.Marshal(msg)
	return out
}
