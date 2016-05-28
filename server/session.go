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
 *  File        :  session.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
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
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	NONE = iota
	WEBSOCK
	LPOLL
	RPC
)

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
	// Most recent HTTP writer, could be nil
	wrt http.ResponseWriter
	// Pointer to session's record in sessionStore
	lpTracker *list.Element
	// --

	// -- Set only for RPC sessions
	rpcnode *ClusterNode
	// --

	// IP address of the client. For long polling this is the IP of the last poll
	remoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet
	userAgent string

	// ID of the current user or 0
	uid types.Uid

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
	ping chan<- string
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
}

// Request to subscribe to a topic
func (s *Session) subscribe(msg *ClientComMessage) {
	log.Printf("Sub to '%s' from '%s'", msg.Sub.Topic, msg.from)

	var topic, expanded string

	if msg.Sub.Topic == "new" {
		// Request to create a new named topic
		expanded = genTopicName()
		topic = expanded
	} else {
		var err *ServerComMessage
		topic, expanded, err = s.validateTopicName(msg.Sub.Id, msg.Sub.Topic, msg.timestamp)
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

	if msg.Leave.Topic == "" {
		s.queueOut(ErrMalformed(msg.Leave.Id, "", msg.timestamp))
		return
	}

	topic := msg.Leave.Topic
	if msg.Leave.Topic == "me" {
		topic = s.uid.UserId()
	} else if msg.Leave.Topic == "fnd" {
		topic = s.uid.FndName()
	}

	if sub, ok := s.subs[topic]; ok {
		// Session is attached to the topic.
		if (msg.Leave.Topic == "me" || msg.Leave.Topic == "fnd") && msg.Leave.Unsub {
			// User should not unsubscribe from 'me' or 'find'. Just leaving is fine.
			s.queueOut(ErrPermissionDenied(msg.Leave.Id, msg.Leave.Topic, msg.timestamp))
		} else {
			// Unlink from topic, topic will send a reply.
			delete(s.subs, topic)
			sub.done <- &sessionLeave{
				sess: s, unsub: msg.Leave.Unsub, topic: msg.Leave.Topic, reqId: msg.Leave.Id}
		}
	} else if globals.cluster.isRemoteTopic(topic) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, topic, s); err != nil {
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

	// TODO(gene): Check for repeated messages with the same ID

	topic, routeTo, err := s.validateTopicName(msg.Pub.Id, msg.Pub.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	data := &ServerComMessage{Data: &MsgServerData{
		Topic:     topic,
		From:      msg.from,
		Timestamp: msg.timestamp,
		Content:   msg.Pub.Content},
		rcptto: routeTo, sessFrom: s, id: msg.Pub.Id, timestamp: msg.timestamp}
	if msg.Pub.NoEcho {
		data.sessSkip = s
	}

	if sub, ok := s.subs[routeTo]; ok {
		// This is a post to a subscribed topic. The message is sent to the topic only
		sub.broadcast <- data
	} else if globals.cluster.isRemoteTopic(routeTo) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, routeTo, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Pub.Id, topic, msg.timestamp))
		}
	}
}

// Authenticate
func (s *Session) login(msg *ClientComMessage) {

	if !s.uid.IsZero() {
		s.queueOut(ErrAlreadyAuthenticated(msg.Login.Id, "", msg.timestamp))
		return
	}

	handler := store.GetAuthHandler(msg.Login.Scheme)
	if handler == nil {
		s.queueOut(ErrAuthUnknownScheme(msg.Login.Id, "", msg.timestamp))
		return
	}

	uid, expires, errType := handler.Authenticate(msg.Login.Secret)
	if errType == auth.ErrMalformed {
		s.queueOut(ErrMalformed(msg.Login.Id, "", msg.timestamp))
		return
	}

	// DB error
	if errType == auth.ErrInternal {
		s.queueOut(ErrUnknown(msg.Login.Id, "", msg.timestamp))
		return
	}

	// All other errors are reported as invalid login or password
	if uid.IsZero() {
		s.queueOut(ErrAuthFailed(msg.Login.Id, "", msg.timestamp))
		return
	}

	s.uid = uid
	s.userAgent = msg.Login.UserAgent

	if msg.Login.Scheme != "token" {
		handler = store.GetAuthHandler("token")
	}

	tokenExp := msg.timestamp.Add(globals.tokenExpiresIn)
	if !expires.IsZero() && tokenExp.After(expires) {
		tokenExp = expires
	}
	// Token GenSecret never fails, ignore the error
	secret, _ := handler.GenSecret(uid, expires)

	s.queueOut(&ServerComMessage{Ctrl: &MsgServerCtrl{
		Id:        msg.Login.Id,
		Code:      http.StatusOK,
		Text:      http.StatusText(http.StatusOK),
		Timestamp: msg.timestamp,
		Params:    map[string]interface{}{"uid": uid.UserId(), "token": secret, "expires": tokenExp}}})
}

// Account creation
func (s *Session) acc(msg *ClientComMessage) {
	if msg.Acc.Auth == nil {
		s.queueOut(ErrMalformed(msg.Acc.Id, "", msg.timestamp))
		return
	} else if len(msg.Acc.Auth) == 0 {
		s.queueOut(ErrAuthUnknownScheme(msg.Acc.Id, "", msg.timestamp))
		return
	}

	if msg.Acc.User == "new" {
		// Request to create a new account
		for _, auth := range msg.Acc.Auth {
			if auth.Scheme == "basic" {
				var private interface{}
				var user types.User
				if msg.Acc.Desc != nil {
					user.Access.Auth = DEFAULT_AUTH_ACCESS
					user.Access.Anon = DEFAULT_ANON_ACCESS

					if msg.Acc.Desc.DefaultAcs != nil {
						if msg.Acc.Desc.DefaultAcs.Auth != "" {
							user.Access.Auth.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Auth))
						}
						if msg.Acc.Desc.DefaultAcs.Anon != "" {
							user.Access.Anon.UnmarshalText([]byte(msg.Acc.Desc.DefaultAcs.Anon))
						}
					}
					if !isNullValue(msg.Acc.Desc.Public) {
						user.Public = msg.Acc.Desc.Public
					}
					if !isNullValue(msg.Acc.Desc.Private) {
						private = msg.Acc.Desc.Private
					}
				}
				_, err := store.Users.Create(&user, private)
				if err != nil {
					if err.Error() == "duplicate credential" {
						s.queueOut(ErrDuplicateCredential(msg.Acc.Id, "", msg.timestamp))
					} else {
						s.queueOut(ErrUnknown(msg.Acc.Id, "", msg.timestamp))
					}
					return
				}

				reply := NoErrCreated(msg.Acc.Id, "", msg.timestamp)
				desc := &MsgTopicDesc{
					CreatedAt: &user.CreatedAt,
					UpdatedAt: &user.UpdatedAt,
					DefaultAcs: &MsgDefaultAcsMode{
						Auth: user.Access.Auth.String(),
						Anon: user.Access.Anon.String()},
					Public:  user.Public,
					Private: private}

				reply.Ctrl.Params = map[string]interface{}{
					"uid":  user.Uid().UserId(),
					"desc": desc,
				}
				s.queueOut(NoErr(msg.Acc.Id, "", msg.timestamp))
			} else {
				s.queueOut(ErrAuthUnknownScheme(msg.Acc.Id, "", msg.timestamp))
				return
			}
		}
	} else if !s.uid.IsZero() {
		// Request to change auth of an existing account. Only basic auth is currently supported
		for _, auth := range msg.Acc.Auth {
			if auth.Scheme == "basic" {
				if err := store.Users.ChangeAuthCredential(s.uid, auth.Scheme, string(auth.Secret)); err != nil {
					s.queueOut(ErrUnknown(msg.Acc.Id, "", msg.timestamp))
					return
				}

				s.queueOut(NoErr(msg.Acc.Id, "", msg.timestamp))
			} else {
				s.queueOut(ErrAuthUnknownScheme(msg.Acc.Id, "", msg.timestamp))
				return
			}
		}
	} else {
		// session is not authenticated and this is not an attempt to create a new account
		s.queueOut(ErrPermissionDenied(msg.Acc.Id, "", msg.timestamp))
		return
	}
}

func (s *Session) get(msg *ClientComMessage) {
	log.Println("s.get: processing 'get." + msg.Get.What + "'")

	// Validate topic name
	original, expanded, err := s.validateTopicName(msg.Get.Id, msg.Get.Topic, msg.timestamp)
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
		s.queueOut(ErrMalformed(msg.Get.Id, original, msg.timestamp))
		log.Println("s.get: invalid Get message action: '" + msg.Get.What + "'")
	} else if ok {
		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Get.Id, original, msg.timestamp))
		}
	} else {
		if (meta.what&constMsgMetaData != 0) || (meta.what&constMsgMetaSub != 0) {
			log.Println("s.get: invalid Get message action for hub routing: '" + msg.Get.What + "'")
			s.queueOut(ErrPermissionDenied(msg.Get.Id, original, msg.timestamp))
		} else {
			// Description of a topic not currently subscribed to. Request desc from the hub
			globals.hub.meta <- meta
		}
	}
}

func (s *Session) set(msg *ClientComMessage) {
	log.Println("s.set: processing 'set'")

	// Validate topic name
	original, expanded, err := s.validateTopicName(msg.Set.Id, msg.Set.Topic, msg.timestamp)
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
			s.queueOut(ErrMalformed(msg.Set.Id, original, msg.timestamp))
			log.Println("s.set: nil Set action")
		}

		log.Println("s.set: sending to topic")
		sub.meta <- meta
	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Set.Id, original, msg.timestamp))
		}
	} else {
		log.Println("s.set: can Set for subscribed topics only")
		s.queueOut(ErrPermissionDenied(msg.Set.Id, original, msg.timestamp))
	}
}

func (s *Session) del(msg *ClientComMessage) {
	log.Println("s.del: processing 'del." + msg.Del.What + "'")

	// Validate topic name
	original, expanded, err := s.validateTopicName(msg.Del.Id, msg.Del.Topic, msg.timestamp)
	if err != nil {
		s.queueOut(err)
		return
	}

	sub, ok := s.subs[expanded]
	what := parseMsgClientDel(msg.Del.What)
	if what == 0 {
		s.queueOut(ErrMalformed(msg.Del.Id, original, msg.timestamp))
		log.Println("s.del: invalid Del action '" + msg.Del.What + "'")
	}

	if ok {
		log.Println("s.del: sending to topic")
		sub.meta <- &metaReq{
			topic: expanded,
			pkt:   msg,
			sess:  s,
			what:  what}

	} else if globals.cluster.isRemoteTopic(expanded) {
		// The topic is handled by a remote node. Forward message to it.
		if err := globals.cluster.routeToTopic(msg, expanded, s); err != nil {
			s.queueOut(ErrClusterNodeUnreachable(msg.Del.Id, original, msg.timestamp))
		}
	} else if what == constMsgDelTopic {
		globals.hub.unreg <- &topicUnreg{
			topic:       expanded,
			msg:         msg.Del,
			sess:        s,
			fromSession: true,
			del:         true}
	} else {
		// Must join the topic first to delete messages.
		s.queueOut(ErrAttachFirst(msg.Del.Id, original, msg.timestamp))
		log.Println("s.del: invalid Del action while unsubbed '" + msg.Del.What + "'")
	}
}

// Broadcast a transient {ping} message to active topic subscribers
// Not reporting any errors
func (s *Session) note(msg *ClientComMessage) {

	_, routeTo, err := s.validateTopicName("", msg.Note.Topic, msg.timestamp)
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

	if sub, ok := s.subs[routeTo]; ok {
		// Pings can be sent to subscribed topics only
		sub.broadcast <- &ServerComMessage{Info: &MsgServerInfo{
			Topic: msg.Note.Topic,
			From:  s.uid.UserId(),
			What:  msg.Note.What,
			SeqId: msg.Note.SeqId,
		}, rcptto: routeTo, timestamp: msg.timestamp, sessSkip: s}
	} else if globals.cluster.isRemoteTopic(routeTo) {
		// The topic is handled by a remote node. Forward message to it.
		globals.cluster.routeToTopic(msg, routeTo, s)
	}
}

// validateTopicName expands session specific topic name to global name
// Returns
//   topic: session-specific topic name the message recepient should see
//   routeTo: routable global topic name
//   err: *ServerComMessage with an error to return to the sender
func (s *Session) validateTopicName(msgId, topic string, timestamp time.Time) (string, string, *ServerComMessage) {

	if topic == "" {
		return "", "", ErrMalformed(msgId, "", timestamp)
	}

	if !strings.HasPrefix(topic, "grp") && s.uid.IsZero() {
		// me and p2p topics require authentication
		return "", "", ErrAuthRequired(msgId, topic, timestamp)
	}

	// Topic to route to i.e. rcptto: or s.subs[routeTo]
	routeTo := topic

	if topic == "me" {
		routeTo = s.uid.UserId()
	} else if topic == "fnd" {
		routeTo = s.uid.FndName()
	} else if strings.HasPrefix(topic, "usr") {
		// Initiating a p2p topic
		uid2 := types.ParseUserId(topic)
		if uid2.IsZero() {
			// Ensure the user id is valid
			return "", "", ErrMalformed(msgId, topic, timestamp)
		} else if uid2 == s.uid {
			// Use 'me' to access self-topic
			return "", "", ErrPermissionDenied(msgId, topic, timestamp)
		}
		routeTo = s.uid.P2PName(uid2)
		topic = routeTo
	} else if strings.HasPrefix(topic, "p2p") {
		uid1, uid2, err := types.ParseP2P(topic)
		if err != nil || uid1.IsZero() || uid2.IsZero() || uid1 == uid2 {
			// Ensure the user ids are valid
			return "", "", ErrMalformed(msgId, topic, timestamp)
		} else if uid1 != s.uid && uid2 != s.uid {
			// One can't access someone else's p2p topic
			return "", "", ErrPermissionDenied(msgId, topic, timestamp)
		}
	}

	return topic, routeTo, nil
}

// pingMeTopic tells current user's 'me' topic that this session was active
func (s *Session) pingMeTopic(ua string) {
	if sub, ok := s.subs[s.uid.UserId()]; ok {
		sub.ping <- s.userAgent
	}
}
