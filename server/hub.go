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
 *  File        :  hub.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
 *
 *  Description :
 *
 *    Create/tear down conversation topics, route messages between topics.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"expvar"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Subscribe session to topic
type sessionJoin struct {
	// Routable (expanded) name of the topic to subscribe to
	topic string
	// Packet, containing request details
	pkt *MsgClientSub
	// Session to subscribe to topic
	sess *Session
	// If this topic was just created
	created bool
	// If the topic was just loaded
	loaded bool
}

// Session wants to leave the topic to topic
type sessionLeave struct {
	// Session which initiated the request
	sess *Session
	// Leave and unsubscribe
	unsub bool
	// Originating request
	pkt *ClientComMessage
}

// Remove topic from hub
type topicUnreg struct {
	appid uint32
	// Name of the topic to drop
	topic string
}

// Request from !pres to another topic to start/stop receiving presence updates
type presSubsReq struct {
	id        types.Uid
	subscribe bool
}

type metaReq struct {
	// Routable name of the topic to get info for
	topic string
	// packet containing details of the Get/Set request
	pkt *ClientComMessage
	// Session which originated the request
	sess *Session
	// what is being requested, constMsgGetInfo, constMsgGetSub, constMsgGetData
	what int
}

type Hub struct {

	// Topics must be indexed by appid!name
	topics map[string]*Topic

	// Channel for routing messages between topics, buffered at 2048
	route chan *ServerComMessage

	// subscribe session to topic, possibly creating a new topic
	join chan *sessionJoin

	// Remove topic from hub
	unreg chan topicUnreg

	// process get.info requests for topic not subscribed to
	meta chan *metaReq

	// Exported counter of live topics
	topicsLive *expvar.Int
}

func (h *Hub) topicKey(appid uint32, name string) string {
	return strconv.FormatInt(int64(appid), 32) + "!" + name
}

func (h *Hub) topicGet(appid uint32, name string) *Topic {
	return h.topics[h.topicKey(appid, name)]
}

func (h *Hub) topicPut(appid uint32, name string, t *Topic) {
	h.topics[h.topicKey(appid, name)] = t
}

func (h *Hub) topicDel(appid uint32, name string) {
	delete(h.topics, h.topicKey(appid, name))
}

func newHub() *Hub {
	var h = &Hub{
		topics: make(map[string]*Topic),
		// this needs to be buffered - hub generates invites and adds them to this queue
		route:      make(chan *ServerComMessage, 2048),
		join:       make(chan *sessionJoin),
		unreg:      make(chan topicUnreg),
		meta:       make(chan *metaReq, 32),
		topicsLive: new(expvar.Int)}

	expvar.Publish("LiveTopics", h.topicsLive)

	go h.run()

	return h
}

func (h *Hub) run() {
	log.Println("Hub started")

	for {
		select {
		case sreg := <-h.join:
			// Handle a subscription request:
			// 1. Init topic
			// 1.1 If a new topic is requested, create it
			// 1.2 If a new subscription to an existing topic is requested:
			// 1.2.1 check if topic is already loaded
			// 1.2.2 if not, load it
			// 1.2.3 if it cannot be loaded (not found), fail
			// 2. Check access rights and reject, if appropriate
			// 3. Attach session to the topic

			t := h.topicGet(sreg.sess.appid, sreg.topic) // is the topic already loaded?
			if t == nil {
				// Topic does not exist or not loaded
				go topicInit(sreg, h)
			} else {
				// Topic found.
				// Topic will check access rights and send appropriate {ctrl}
				t.reg <- sreg
			}

		case msg := <-h.route:
			// This is a message from a connection not subscribed to topic
			// Route incoming message to topic if topic permits such routing

			timestamp := time.Now().UTC().Round(time.Millisecond)
			if dst := h.topicGet(msg.appid, msg.rcptto); dst != nil {
				// Everything is OK, sending packet to known topic
				//log.Printf("Hub. Sending message to '%s'", dst.name)

				if dst.broadcast != nil {
					select {
					case dst.broadcast <- msg:
					default:
						log.Printf("hub: topic's broadcast queue is full '%s'", dst.name)
					}
				}
			} else {
				if msg.Data != nil {
					// Normally the message is persisted at the topic. If the topic is offline,
					// persist message here. The only case of sending to offline topics is invites/info to 'me'
					// The 'me' must receive them, so ignore access settings

					if err := store.Messages.Save(msg.appid, &types.Message{
						ObjHeader: types.ObjHeader{CreatedAt: msg.Data.Timestamp},
						Topic:     msg.rcptto,
						// SeqId is assigned by the store.Mesages.Save
						From:    types.ParseUserId(msg.Data.From).String(),
						Content: msg.Data.Content}); err != nil {

						simpleByteSender(msg.akn, ErrUnknown(msg.id, msg.Data.Topic, timestamp))
						return
					}

					// TODO(gene): validate topic name, discarding invalid topics
					log.Printf("Hub. Topic '%d.%s' is unknown or offline", msg.appid, msg.rcptto)
					for tt, _ := range h.topics {
						log.Printf("Hub contains topic '%s'", tt)
					}
					simpleByteSender(msg.akn, NoErrAccepted(msg.id, msg.rcptto, timestamp))
				}
			}

		case meta := <-h.meta:
			log.Println("hub.meta: got message")
			// Request for topic info from a user who is not subscribed to the topic
			if dst := h.topicGet(meta.sess.appid, meta.topic); dst != nil {
				// If topic is already in memory, pass request to topic
				log.Println("hub.meta: topic already in memory")
				dst.meta <- meta
			} else if meta.pkt.Get != nil {
				// If topic is not in memory, fetch requested info from DB and reply here
				log.Println("hub.meta: topic NOT in memory")
				go replyTopicInfoBasic(meta.sess, meta.topic, meta.pkt.Get)
			}

		case unreg := <-h.unreg:
			if t := h.topicGet(unreg.appid, unreg.topic); t != nil {
				h.topicDel(unreg.appid, unreg.topic)
				h.topicsLive.Add(-1)
				t.sessions = nil
				close(t.reg)
				close(t.unreg)
				close(t.broadcast)
			}

		case <-time.After(IDLETIMEOUT):
		}
	}
}

// topicInit reads an existing topic from database or creates a new topic
func topicInit(sreg *sessionJoin, h *Hub) {
	var t *Topic

	timestamp := time.Now().UTC().Round(time.Millisecond)

	t = &Topic{name: sreg.topic,
		original:  sreg.pkt.Topic,
		appid:     sreg.sess.appid,
		sessions:  make(map[*Session]bool),
		broadcast: make(chan *ServerComMessage, 256),
		reg:       make(chan *sessionJoin, 32),
		unreg:     make(chan *sessionLeave, 32),
		meta:      make(chan *metaReq, 32),
		perUser:   make(map[types.Uid]perUserData),
	}

	// Request to load a me topic. The topic must exist
	if t.original == "me" {
		log.Println("hub: loading me topic")

		t.cat = TopicCat_Me

		// 'me' has no owner, t.owner = nil

		// Ensure all requests to subscribe are automatically rejected
		t.accessAuth = types.ModeBanned
		t.accessAnon = types.ModeBanned

		user, err := store.Users.Get(t.appid, sreg.sess.uid)
		if err != nil {
			log.Println("hub: cannot load user object for 'me'='" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		}

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		}

		t.public = user.Public

		t.created = user.CreatedAt
		t.updated = user.UpdatedAt

		t.lastId = user.SeqId

		// Initiate User Agent with the UA of the creating session so we don't report it later
		t.userAgent = sreg.sess.userAgent

		// Request to create a new p2p topic, then attach to it
	} else if strings.HasPrefix(t.original, "usr") {
		log.Println("hub: new p2p topic")

		t.cat = TopicCat_P2P

		// t.owner is blank for p2p topics

		// Ensure that other users are automatically rejected
		t.accessAuth = types.ModeBanned
		t.accessAnon = types.ModeBanned

		var userData perUserData
		if sreg.pkt.Init != nil && !isNullValue(sreg.pkt.Init.Private) {
			// t.public is not used for p2p topics since each user get a different public

			userData.private = sreg.pkt.Init.Private
			// Init.DefaultAcs and Init.Public are ignored for p2p topics
		}
		// Custom default access levels set in sreg.pkt.Init.DefaultAcs are ignored

		// Fetch user's public and default access
		userId1 := sreg.sess.uid
		userId2 := types.ParseUserId(t.original)
		users, err := store.Users.GetAll(t.appid, userId1, userId2)
		if err != nil {
			log.Println("hub: failed to load users for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.name, timestamp))
			return
		} else if len(users) != 2 {
			// invited user does not exist
			log.Println("hub: missing user for '" + t.name + "'")
			sreg.sess.QueueOut(ErrUserNotFound(sreg.pkt.Id, t.name, timestamp))
			return
		}

		var u1, u2 int
		if users[0].Uid() == userId1 {
			u1 = 0
			u2 = 1
		} else {
			u1 = 1
			u2 = 0
		}

		// User may set non-default access to topic, just make sure it's no higher than the default
		if sreg.pkt.Sub != nil && sreg.pkt.Sub.Mode != "" {
			if err := userData.modeWant.UnmarshalText([]byte(sreg.pkt.Sub.Mode)); err != nil {
				log.Println("hub: invalid access mode for topic '" + t.name + "': '" + sreg.pkt.Sub.Mode + "'")
				userData.modeWant = types.ModeP2P
			} else {
				userData.modeWant &= types.ModeP2P
			}
		} else if users[u1].Access.Auth != types.ModeNone {
			userData.modeWant = users[u1].Access.Auth
		} else {
			userData.modeWant = types.ModeP2P
		}

		user1 := &types.Subscription{
			User:      userId1.String(),
			Topic:     t.name,
			ModeWant:  userData.modeWant,
			ModeGiven: types.ModeP2P,
			Private:   userData.private}
		user1.SetPublic(users[u1].Public)
		user2 := &types.Subscription{
			User:      userId2.String(),
			Topic:     t.name,
			ModeWant:  users[u2].Access.Auth,
			ModeGiven: types.ModeP2P,
			Private:   nil}
		user2.SetPublic(users[u2].Public)

		err = store.Topics.CreateP2P(t.appid, user1, user2)
		if err != nil {
			log.Println("hub: databse error in creating subscriptions '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.name, timestamp))
			return
		}

		// t.public is not used for p2p topics since each user gets a different public

		t.created = user1.CreatedAt
		t.updated = user1.UpdatedAt

		// t.lastId is not set (default 0) for new topics

		userData.public = user2.GetPublic()
		userData.modeWant = user1.ModeWant
		userData.modeGiven = user1.ModeGiven
		t.perUser[userId1] = userData

		t.perUser[userId2] = perUserData{
			public:    user1.GetPublic(),
			modeWant:  user2.ModeWant,
			modeGiven: user2.ModeGiven,
		}

		t.original = t.name
		sreg.created = true

		// Load an existing p2p topic
	} else if strings.HasPrefix(t.name, "p2p") {
		log.Println("hub: existing p2p topic")

		t.cat = TopicCat_P2P

		// t.owner no valid owner for p2p topics, leave blank

		// Ensure that other users are automatically rejected
		t.accessAuth = types.ModeBanned
		t.accessAnon = types.ModeBanned

		// Load the topic object
		stopic, err := store.Topics.Get(sreg.sess.appid, t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		} else if stopic == nil {
			log.Println("hub: topic '" + t.name + "' does not exist")
			sreg.sess.QueueOut(ErrTopicNotFound(sreg.pkt.Id, t.original, timestamp))
			return
		}

		subs, err := store.Topics.GetSubs(t.appid, t.name)
		if err != nil {
			log.Println("hub: cannot load subscritions for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.name, timestamp))
			return
		} else if len(subs) != 2 {
			log.Println("hub: invalid number of subscriptions for '" + t.name + "'")
			sreg.sess.QueueOut(ErrTopicNotFound(sreg.pkt.Id, t.name, timestamp))
			return
		}

		// t.public is not used for p2p topics since each user gets a different public

		t.created = stopic.CreatedAt
		t.updated = stopic.UpdatedAt

		t.lastId = stopic.SeqId

		for i := 0; i < 2; i++ {
			uid := types.ParseUid(subs[i].User)
			t.perUser[uid] = perUserData{
				// Based on other user
				public:  subs[(i+1)%2].GetPublic(),
				private: subs[i].Private,
				// lastSeenTag: subs[i].LastSeen,
				modeWant:  subs[i].ModeWant,
				modeGiven: subs[i].ModeGiven}
		}

		// Processing request to create a new generic (group) topic:
	} else if t.original == "new" {
		log.Println("hub: new group topic")

		t.cat = TopicCat_Grp

		// Generic topics have parameters stored in the topic object
		t.owner = sreg.sess.uid

		t.accessAuth = DEFAULT_AUTH_ACCESS
		t.accessAnon = DEFAULT_ANON_ACCESS

		// Owner/creator gets full access to topic
		userData := perUserData{modeGiven: types.ModeFull}

		// User sent initialization parameters
		if sreg.pkt.Init != nil {
			if !isNullValue(sreg.pkt.Init.Public) {
				t.public = sreg.pkt.Init.Public
			}
			if !isNullValue(sreg.pkt.Init.Private) {
				userData.private = sreg.pkt.Init.Private
			}

			// set default access
			if sreg.pkt.Init.DefaultAcs != nil {
				if auth, anon, err := parseTopicAccess(sreg.pkt.Init.DefaultAcs, t.accessAuth, t.accessAnon); err != nil {
					log.Println("hub: invalid access mode for topic '" + t.name + "': '" + err.Error() + "'")
				} else if auth&types.ModeOwner != 0 || anon&types.ModeOwner != 0 {
					log.Println("hub: OWNER default access in topic '" + t.name)
				} else {
					t.accessAuth, t.accessAnon = auth, anon
				}
			}
		}

		// Owner/creator may restrict own access to topic
		if sreg.pkt.Sub == nil || sreg.pkt.Sub.Mode == "" {
			userData.modeWant = types.ModeFull
		} else {
			if err := userData.modeWant.UnmarshalText([]byte(sreg.pkt.Sub.Mode)); err != nil {
				log.Println("hub: invalid access mode for topic '" + t.name + "': '" + sreg.pkt.Sub.Mode + "'")
			}
		}

		t.perUser[t.owner] = userData

		t.created = timestamp
		t.updated = timestamp

		// t.lastId is not set for new topics

		stopic := &types.Topic{
			ObjHeader: types.ObjHeader{CreatedAt: timestamp},
			Name:      sreg.topic,
			Access:    types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon},
			Public:    t.public}
		// store.Topics.Create will add a subscription record for the topic creator
		stopic.GiveAccess(t.owner, userData.modeWant, userData.modeGiven)
		err := store.Topics.Create(sreg.sess.appid, stopic, t.owner, t.perUser[t.owner].private)
		if err != nil {
			log.Println("hub: cannot save new topic '" + t.name + "' (" + err.Error() + ")")
			// Error sent on "new" topic
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		}

		t.original = t.name // keeping 'new' as original has no value to the client
		sreg.created = true

	} else {
		log.Println("hub: existing group topic")

		t.cat = TopicCat_Grp

		// TODO(gene): check and validate topic name
		stopic, err := store.Topics.Get(sreg.sess.appid, t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		} else if stopic == nil {
			log.Println("hub: topic '" + t.name + "' does not exist")
			sreg.sess.QueueOut(ErrTopicNotFound(sreg.pkt.Id, t.original, timestamp))
			return
		}

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.QueueOut(ErrUnknown(sreg.pkt.Id, t.original, timestamp))
			return
		}

		// t.owner is set by loadSubscriptions

		t.accessAuth = stopic.Access.Auth
		t.accessAnon = stopic.Access.Anon

		t.public = stopic.Public

		t.created = stopic.CreatedAt
		t.updated = stopic.UpdatedAt

		t.lastId = stopic.SeqId
	}

	log.Println("hub: topic created or loaded: " + t.name)

	h.topicPut(t.appid, t.name, t)
	h.topicsLive.Add(1)
	go t.run(h)

	sreg.loaded = true
	// Topic will check access rights, send invite to p2p user, send {ctrl} message to the initiator session
	t.reg <- sreg
}

// loadSubscribers loads topic subscribers, sets topic owner
func (t *Topic) loadSubscribers() error {
	subs, err := store.Topics.GetSubs(t.appid, t.name)
	if err != nil {
		return err
	}

	for _, sub := range subs {
		uid := types.ParseUid(sub.User)
		t.perUser[uid] = perUserData{
			readId:  sub.ReadSeqId,
			private: sub.Private,
			//lastSeenTag: sub.LastSeen, // could be nil
			modeWant:  sub.ModeWant,
			modeGiven: sub.ModeGiven}

		if sub.ModeGiven&sub.ModeWant&types.ModeOwner != 0 {
			log.Printf("hub.loadSubscriptions: %s set owner to %s", t.name, uid.String())
			t.owner = uid
		}
	}

	return nil
}

// replyTopicInfoBasic loads minimal topic Info when the requester is not subscribed to the topic
func replyTopicInfoBasic(sess *Session, topic string, get *MsgClientGet) {
	log.Printf("hub.replyTopicInfoBasic: topic %s", topic)
	now := time.Now().UTC().Round(time.Millisecond)
	info := &MsgTopicInfo{}

	if strings.HasPrefix(topic, "grp") {
		stopic, err := store.Topics.Get(sess.appid, topic)
		if err == nil {
			info.CreatedAt = &stopic.CreatedAt
			info.UpdatedAt = &stopic.UpdatedAt
			info.Public = stopic.Public
		} else {
			simpleByteSender(sess.send, ErrUnknown(get.Id, get.Topic, now))
			return
		}
	} else {
		// 'me' and p2p topics
		var uid types.Uid
		if strings.HasPrefix(topic, "usr") {
			// User specified as usrXXX
			uid = types.ParseUserId(topic)
		} else if strings.HasPrefix(topic, "p2p") {
			// User specified as p2pXXXYYY
			uid1, uid2, _ := types.ParseP2P(topic)
			if uid1 == sess.uid {
				uid = uid2
			} else if uid2 == sess.uid {
				uid = uid1
			}
		}

		if uid.IsZero() {
			simpleByteSender(sess.send, ErrMalformed(get.Id, get.Topic, now))
			return
		}

		suser, err := store.Users.Get(sess.appid, uid)
		if err == nil {
			info.CreatedAt = &suser.CreatedAt
			info.UpdatedAt = &suser.UpdatedAt
			info.Public = suser.Public
		} else {
			log.Printf("hub.replyTopicInfoBasic: sending  error 3")
			simpleByteSender(sess.send, ErrUnknown(get.Id, get.Topic, now))
			return
		}
	}

	log.Printf("hub.replyTopicInfoBasic: sending info -- OK")
	simpleByteSender(sess.send, &ServerComMessage{
		Meta: &MsgServerMeta{Id: get.Id, Topic: get.Topic, Timestamp: &now, Info: info}})
}

// Parse topic access parameters
func parseTopicAccess(acs *MsgDefaultAcsMode, defAuth, defAnon types.AccessMode) (auth, anon types.AccessMode, err error) {

	auth, anon = defAuth, defAnon

	if acs.Auth != "" {
		if err = auth.UnmarshalText([]byte(acs.Auth)); err != nil {
			log.Println("hub: invalid default auth access mode '" + acs.Auth + "'")
		}
	}

	if acs.Anon != "" {
		if err = anon.UnmarshalText([]byte(acs.Anon)); err != nil {
			log.Println("hub: invalid default anon access mode '" + acs.Anon + "'")
		}
	}

	return
}

// simpleByteSender attempts to send a JSON to a connection, time out is 1 second
func simpleByteSender(sendto chan<- []byte, msg *ServerComMessage) {
	if sendto == nil {
		return
	}
	data, _ := json.Marshal(msg)
	select {
	case sendto <- data:
	case <-time.After(time.Second):
		log.Println("simpleByteSender: timeout")
	}
}
