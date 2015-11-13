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
 *  File        :  topic.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
 *
 *  Description :
 *    An isolated communication channel (chat room, 1:1 conversation, control
 *    connection) for usualy multiple users. There is no communication across topics
 *
 *
 *****************************************************************************/
package main

import (
	"encoding/json"
	"errors"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
	"log"
	"time"
)

// Topic: an isolated communication channel
type Topic struct {
	// expanded name of the topic
	name string
	// session-specific topic name, such as 'me'
	original string

	// AppID
	appid uint32

	// topic category
	cat TopicCat

	// Time when the topic was first created
	created time.Time
	// Time when the topic was last updated
	updated time.Time

	// Time of the last message
	lastMessage time.Time

	// User ID of the topic owner/creator
	owner types.Uid

	// Default access mode
	accessAuth types.AccessMode
	accessAnon types.AccessMode

	// Topic's public data
	public interface{}
	// Topic's per-subscriber data
	perUser map[types.Uid]perUserData

	// Sessions attached to this topic
	sessions map[*Session]bool

	// Send presence/new message notifications to these !usr:Uid topics (all topics when they have subscribers)
	pushTo map[types.Uid]bool

	// Inbound {data} and {pres} messages from sessions or other topics, already converted to SCM. Buffered = 256
	broadcast chan *ServerComMessage

	// Channel for receiving {get}/{set} requests, buffered = 32
	meta chan *metaReq

	// Subscribe requests from sessions, buffered = 32
	reg chan *sessionJoin

	// Unsubscribe requests from sessions, buffered = 32
	unreg chan *sessionLeave

	// Presence subscriptions requests -- Request to start/stop receiving presence updates, buffered = ?
	pres chan *presSubsReq
}

type TopicCat int

const (
	TopicCat_Me TopicCat = iota
	TopicCat_P2P
	TopicCat_Grp
)

// Struct ho hold topic's per-user data
type perUserData struct {
	private     interface{}
	lastSeenTag map[string]time.Time
	// cleared     time.Time // time, when the topic was cleared by the user
	modeWant  types.AccessMode
	modeGiven types.AccessMode
	// P2p only:
	public interface{}
}

func (t *Topic) run(hub *Hub) {

	log.Printf("Topic started: '%s'", t.name)

	keepAlive := time.Second * 5 // TODO(gene): read keepalive value from the command line
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	for {
		select {
		case sreg := <-t.reg:
			// Request to add a conection to this topic

			// The topic is alive, so stop the kill timer, if it's ticking. We don't want the topic to die
			// while processing the call
			killTimer.Stop()

			if err := t.handleSubscription(hub, sreg); err == nil {
				// give a broadcast channel to the connection (.read)
				// give channel to use when shutting down (.done)
				sreg.sess.subs[t.name] = &Subscription{broadcast: t.broadcast, done: t.unreg, meta: t.meta}
				t.sessions[sreg.sess] = true

			} else if len(t.sessions) == 0 {
				// Failed to subscribe, the topic is still inactive
				killTimer.Reset(keepAlive)
			}
		case leave := <-t.unreg:
			// Remove connection from topic; session may continue to function

			now := time.Now().UTC().Round(time.Millisecond)

			if leave.unsub {
				// User wants to unsubscribe.

				// Delete user's subscription from the database
				if err := store.Subs.Delete(t.appid, t.name, leave.sess.uid); err != nil {
					if leave.pkt != nil {
						leave.sess.QueueOut(ErrUnknown(leave.pkt.Leave.Id, leave.pkt.Leave.Topic, now))
					}
					log.Println(err)
					continue
				}

				// evict all user's sessions and clear cached data
				t.evictUser(leave.sess.uid, true, leave.sess)

			} else {
				// Just leaving the topic without unsubscribing
				delete(t.sessions, leave.sess)

				pud := t.perUser[leave.sess.uid]
				if pud.lastSeenTag == nil {
					pud.lastSeenTag = map[string]time.Time{}
				}
				pud.lastSeenTag[leave.sess.tag] = now
				t.perUser[leave.sess.uid] = pud
				if err := store.Topics.UpdateLastSeen(t.appid, t.name, leave.sess.uid, leave.sess.tag, now); err != nil {
					log.Println(err)
				}
			}

			// If there are no more subscriptions to this topic, start a kill timer
			if len(t.sessions) == 0 {
				killTimer.Reset(keepAlive)
			}

			if leave.pkt != nil {
				leave.sess.QueueOut(NoErr(leave.pkt.Leave.Id, leave.pkt.Leave.Topic, now))
			}

		case msg := <-t.broadcast:
			// Message intended for broadcasting to recepients

			log.Printf("topic[%s].run: got message '%v'", t.name, msg)

			// Record last message timestamp
			if msg.Data != nil {
				from := types.ParseUserId(msg.Data.From)

				// msg.akn is not nil when the message originated at the client.
				// for internally generated messages, like invites, the akn is nil
				if msg.akn != nil {
					userData := t.perUser[from]
					if userData.modeWant&userData.modeGiven&types.ModePub == 0 {
						simpleByteSender(msg.akn, ErrPermissionDenied(msg.id, t.original, msg.timestamp))
						continue
					}
				}

				if err := store.Messages.Save(t.appid, &types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: msg.Data.Timestamp},
					Topic:     t.name,
					From:      from.String(),
					Content:   msg.Data.Content}); err != nil {

					simpleByteSender(msg.akn, ErrUnknown(msg.id, t.original, msg.timestamp))
					continue
				}

				if msg.id != "" {
					simpleByteSender(msg.akn, NoErrAccepted(msg.id, t.original, msg.timestamp))
				}

				t.lastMessage = msg.timestamp
			}

			// Broadcast the message. Only {data} and {pres} are broadcastable.
			// {meta} and {ctrl} are sent to the session only
			if msg.Data != nil || msg.Pres != nil {
				var packet, _ = json.Marshal(msg)
				for sess := range t.sessions {
					select {
					case sess.send <- packet:
					default:
						log.Printf("topic[%s].run: connection stuck, detach it", t.name)
						t.unreg <- &sessionLeave{sess: sess, unsub: false}
					}
				}
			} else {
				log.Printf("topic[%s].run: wrong message type for broadcasting", t.name)
			}

		case meta := <-t.meta:
			log.Printf("topic[%s].run: got meta message '%v'", t.name, meta)
			// Request to get/set topic metadata
			if meta.pkt.Get != nil {
				// Get request
				if meta.what&constMsgMetaInfo != 0 {
					t.replyGetInfo(meta.sess, meta.pkt.Get.Id, false)
				}
				if meta.what&constMsgMetaSub != 0 {
					t.replyGetSub(meta.sess, meta.pkt.Get.Id)
				}
				if meta.what&constMsgMetaData != 0 {
					t.replyGetData(meta.sess, meta.pkt.Get.Id, meta.pkt.Get.Browse)
				}
			} else {
				// Set request
				if meta.what&constMsgMetaInfo != 0 {
					t.replySetInfo(meta.sess, meta.pkt.Set)
				}
				if meta.what&constMsgMetaSub != 0 {
					//t.replySetSub(meta.sess, meta.pkt.Set)
				}
				if meta.what&constMsgMetaData != 0 {
					//t.replySetData(meta.sess, meta.pkt.Set)
				}
			}

		case req := <-t.pres:
			// Request to start/stop receiving presence updates from this topic
			t.presProcReq(hub, req)

		case <-killTimer.C:
			log.Println("Topic timeout: ", t.name)
			// Ensure that the messages are no longer routed to this topic
			hub.unreg <- topicUnreg{appid: t.appid, topic: t.name}
			// TODO(gene): save lastMessage value;
			t.presPubChange("off")
			return
		}
	}
}

// Session subscribed to a topic, created == true if topic was just created and {pres} needs to be announced
func (t *Topic) handleSubscription(h *Hub, sreg *sessionJoin) error {

	getWhat := parseMsgClientMeta(sreg.pkt.Get)

	if err := t.subCommonReply(h, sreg.sess, sreg.pkt, (getWhat&constMsgMetaInfo != 0), sreg.created); err != nil {
		return err
	}

	// New P2P topic is a special case. It requires an invite/notification for the second person
	if sreg.created && t.cat == TopicCat_P2P {
		log.Println("about to generate invite for ", t.name)
		for uid, pud := range t.perUser {
			if uid != sreg.sess.uid {
				if pud.modeWant&types.ModeBanned != 0 {
					break
				}

				var action types.InviteAction
				if pud.modeWant == types.ModeNone {
					action = types.InvJoin
				} else {
					action = types.InvInfo
				}
				log.Println("sending invite to ", uid.UserId())
				h.route <- t.makeInvite(uid, uid, sreg.sess.uid, action, pud.modeWant, pud.modeGiven, nil /* Public */)
				break
			}
		}
	}

	if sreg.loaded {
		t.presPubTopicOnline()
	}

	if t.cat == TopicCat_Me {
		// User attached to !me topic, get the currect state of topics he is interested in
		t.presSnapshot()
	}

	if getWhat&constMsgMetaSub != 0 {
		// Send get.sub response as a separate {meta} packet
		t.replyGetSub(sreg.sess, sreg.pkt.Id)
	}

	if getWhat&constMsgMetaData != 0 {
		// Send get.data response as {data} packets
		t.replyGetData(sreg.sess, sreg.pkt.Id, sreg.pkt.Browse)
	}
	return nil
}

// subCommonReply generates a response to a subscription request
func (t *Topic) subCommonReply(h *Hub, sess *Session, pkt *MsgClientSub, sendInfo bool, created bool) error {
	log.Println("subCommonReply ", t.name)
	now := time.Now().UTC().Round(time.Millisecond)

	// The topic t is already initialized by the Hub

	var info, private interface{}
	var mode string

	if pkt.Sub != nil {
		if pkt.Sub.User != "" {
			log.Println("subCommonReply: msg.Sub.Sub.User is ", pkt.Sub.User)
			simpleByteSender(sess.send, ErrMalformed(pkt.Id, t.original, now))
			return errors.New("user id must not be specified")
		}

		info = pkt.Sub.Info
		mode = pkt.Sub.Mode
	}

	if pkt.Init != nil && !isNullValue(pkt.Init.Private) {
		private = pkt.Init.Private
	}

	if err := t.requestSub(h, sess, pkt.Id, mode, info, private); err != nil {
		return err
	}

	simpleByteSender(sess.send, NoErr(pkt.Id, t.original, now))

	if sendInfo {
		t.replyGetInfo(sess, pkt.Id, created)
	}

	return nil
}

// User requests or updates a self-subscription to a topic
//	h 		- hub
//	sess 	- originating session
//  pktId 	- originating packet Id
//	want	- requested access mode
//	info 	- explanation info given by the requester
//	private	- private value to assign to the subscription
// Handle these cases:
// A. User is trying to subscribe for the first time (no subscription)
// B. User is responsing to an earlier invite (modeWant was "N" in subscription)
// C. User is already subscribed, changing modeWant
// D. User is accepting ownership transfer (requesting ownership transfer is not permitted)
func (t *Topic) requestSub(h *Hub, sess *Session, pktId string, want string, info, private interface{}) error {
	log.Println("requestSub", t.name)

	now := time.Now().UTC().Round(time.Millisecond)

	// Parse the acess mode requested by the user
	var modeWant types.AccessMode
	if want != "" {
		modeWant.UnmarshalText([]byte(want))
	}

	// If the user wants a self-ban, make sure it's the only change
	if modeWant.IsBanned() {
		modeWant = types.ModeBanned
	}

	// Vars for saving changes to access mode
	var updWant *types.AccessMode
	var updGiven *types.AccessMode

	// Check if it's an attempt at a new subscription to the topic. If so, save it to database
	userData, ok := t.perUser[sess.uid]
	if !ok {
		// User requested default access mode.
		// modeWant could still be ModeNone if the owner wants to manually approve every request
		if modeWant == types.ModeNone {
			modeWant = t.accessAuth
		}

		userData = perUserData{
			private:     private,
			modeGiven:   t.accessAuth,
			modeWant:    modeWant,
			lastSeenTag: make(map[string]time.Time),
		}

		// Add subscription to database
		sub := types.Subscription{
			User:      sess.uid.String(),
			Topic:     t.name,
			ModeWant:  userData.modeWant,
			ModeGiven: userData.modeGiven,
			LastSeen:  userData.lastSeenTag,
			Private:   userData.private,
		}

		if err := store.Subs.Create(t.appid, &sub); err != nil {
			log.Println(err.Error())
			simpleByteSender(sess.send, ErrUnknown(pktId, t.original, now))
			return err
		}

	} else {
		var ownerChange bool

		// If user did not request a new access mode, copy one from cache
		if modeWant == types.ModeNone {
			modeWant = userData.modeWant
		}

		if userData.modeGiven.IsOwner() {
			// Handle two cases:
			// 1. Owner changing own settings
			// 2. Acceptance or rejection of the ownership transfer

			// Make sure the owner cannot unset the owner flag or ban himself
			if t.owner == sess.uid && (!modeWant.IsOwner() || modeWant.IsBanned()) {
				log.Println("requestSub: owner attempts to unset the owner flag")
				simpleByteSender(sess.send, ErrMalformed(pktId, t.original, now))
				return errors.New("cannot unset ownership or ban the owner")
			} else if modeWant.IsOwner() {
				// Ownership transfer
				ownerChange = true
			}

			// The owner should be able to grant himself any access permissions
			if !userData.modeGiven.Check(modeWant) {
				userData.modeGiven |= modeWant
				updGiven = &userData.modeGiven
			}
		} else if modeWant.IsOwner() {
			// Ownership transfer can only be initiated by the owner
			simpleByteSender(sess.send, ErrMalformed(pktId, t.original, now))
			return errors.New("non-owner cannot request ownership transfer")
		} else if userData.modeGiven.IsManager() {
			// The sharer should be able to grant any permissions except ownership
			if !userData.modeGiven.Check(modeWant & ^types.ModeBanned) {
				userData.modeGiven |= (modeWant & ^types.ModeBanned)
				updGiven = &userData.modeGiven
			}
		}

		// Check if t.accessAuth is different now than at the last attempt to subscribe
		if userData.modeGiven == types.ModeNone && t.accessAuth != types.ModeNone {
			userData.modeGiven = t.accessAuth
			updGiven = &userData.modeGiven
		}

		// This is a request to change access to whatever is currently available
		if modeWant == types.ModeNone {
			modeWant = userData.modeGiven
		}

		// Access actually changed
		if userData.modeWant != modeWant {
			userData.modeWant = modeWant
			updWant = &modeWant
		}

		// Save changes to DB
		if updWant != nil || updGiven != nil {
			update := map[string]interface{}{}
			if updWant != nil {
				update["ModeWant"] = *updWant
			}
			if updGiven != nil {
				update["ModeGiven"] = *updGiven
			}
			if err := store.Subs.Update(t.appid, t.name, sess.uid, update); err != nil {
				simpleByteSender(sess.send, ErrUnknown(pktId, t.original, now))
				return err
			}
		}

		// No transactions in RethinkDB, but two owners are better than none
		if ownerChange {
			userData = t.perUser[t.owner]
			userData.modeGiven = (userData.modeGiven & ^types.ModeOwner)
			userData.modeWant = (userData.modeWant & ^types.ModeOwner)
			if err := store.Subs.Update(t.appid, t.name, t.owner,
				map[string]interface{}{"ModeWant": userData.modeWant, "ModeGiven": userData.modeGiven}); err != nil {
				return err
			}
			t.perUser[t.owner] = userData
			t.owner = sess.uid
		}
	}

	t.perUser[sess.uid] = userData

	// If the user is (self)banned from topic, no further action is needed
	if modeWant.IsBanned() {
		t.evictUser(sess.uid, false, sess)
		return errors.New("self-banned access to topic")
	} else if userData.modeGiven.IsBanned() {
		simpleByteSender(sess.send, ErrPermissionDenied(pktId, t.original, now))
		return errors.New("topic access denied")
	}

	// If requested access mode different from given:
	if !userData.modeGiven.Check(modeWant) {
		// Send req to approve to topic managers
		for uid, pud := range t.perUser {
			if pud.modeGiven&pud.modeWant&types.ModeShare != 0 {
				h.route <- t.makeInvite(uid, sess.uid, sess.uid, types.InvAppr,
					modeWant, userData.modeGiven, info)
			}
		}
		// Send info to requester
		h.route <- t.makeInvite(sess.uid, sess.uid, sess.uid, types.InvInfo, modeWant, userData.modeGiven, t.public)
	}

	return nil
}

// approveSub processes a request to initiate an invite or approve a subscription request from another user:
// Handle these cases:
// A. Manager is inviting another user for the first time (no prior subscription)
// B. Manager is re-inviting another user (adjusting modeGiven, modeWant is still "N")
// C. Manager is changing modeGiven for another user, modeWant != "N"
func (t *Topic) approveSub(h *Hub, sess *Session, target types.Uid, set *MsgClientSet) error {
	now := time.Now().UTC().Round(time.Millisecond)

	// Check if requester actually has permission to manage sharing
	if userData, ok := t.perUser[sess.uid]; !ok || !userData.modeGiven.IsManager() || !userData.modeWant.IsManager() {
		simpleByteSender(sess.send, ErrPermissionDenied(set.Id, t.original, now))
		return errors.New("topic access denied")
	}

	// Parse the access mode granted
	var modeGiven types.AccessMode
	if set.Sub.Mode != "" {
		modeGiven.UnmarshalText([]byte(set.Sub.Mode))
	}

	// If the user is banned from topic, make sute it's the only change
	if modeGiven.IsBanned() {
		modeGiven = types.ModeBanned
	}

	// Make sure no one but the owner can do an ownership transfer
	if modeGiven.IsOwner() && t.owner != sess.uid {
		simpleByteSender(sess.send, ErrPermissionDenied(set.Id, t.original, now))
		return errors.New("attempt to transfer ownership by non-owner")
	}

	var givenBefore types.AccessMode
	// Check if it's a new invite. If so, save it to database as a subscription.
	// Saved subscription does not mean the user is allowed to post/read
	userData, ok := t.perUser[target]
	if !ok {
		if modeGiven == types.ModeNone {
			if t.accessAuth != types.ModeNone {
				// Request to use default access mode for the new subscriptions.
				modeGiven = t.accessAuth
			} else {
				simpleByteSender(sess.send, ErrMalformed(set.Id, t.original, now))
				return errors.New("cannot invite without giving any access rights")
			}
		}

		// Add subscription to database
		sub := types.Subscription{
			User:      target.String(),
			Topic:     t.name,
			ModeWant:  types.ModeNone,
			ModeGiven: modeGiven,
			LastSeen:  make(map[string]time.Time),
		}

		if err := store.Subs.Create(t.appid, &sub); err != nil {
			simpleByteSender(sess.send, ErrUnknown(set.Id, t.original, now))
			return err
		}

		userData = perUserData{
			modeGiven:   sub.ModeGiven,
			modeWant:    sub.ModeWant,
			private:     nil,
			lastSeenTag: sub.LastSeen,
		}

		t.perUser[target] = userData

	} else {
		// Action on an existing subscription (re-invite or confirm/decline)
		givenBefore = userData.modeGiven

		// Request to re-send invite without changing the access mode
		if modeGiven == types.ModeNone {
			modeGiven = userData.modeGiven
		} else if modeGiven != userData.modeGiven {
			userData.modeGiven = modeGiven

			// Save changed value to database
			if err := store.Subs.Update(t.appid, t.name, sess.uid,
				map[string]interface{}{"ModeGiven": modeGiven}); err != nil {
				return err
			}

			t.perUser[target] = userData
		}
	}

	// The user does not want to be bothered, no further action is needed
	if userData.modeWant.IsBanned() {
		simpleByteSender(sess.send, ErrPermissionDenied(set.Id, t.original, now))
		return errors.New("topic access denied")
	}

	// Handle the following cases:
	// * a ban of the user, modeGive.IsBanned = true (if user is banned no need to invite anyone)
	// * regular invite: modeWant = "N", modeGiven > 0
	// * access rights update: old modeGiven != new modeGiven
	if !modeGiven.IsBanned() {
		if userData.modeWant == types.ModeNone {
			// (re-)Send the invite to target
			h.route <- t.makeInvite(target, target, sess.uid, types.InvJoin, userData.modeWant, modeGiven,
				set.Sub.Info)
		} else if givenBefore != modeGiven {
			// Inform target that the access has changed
			h.route <- t.makeInvite(target, target, sess.uid, types.InvInfo, userData.modeWant, modeGiven,
				set.Sub.Info)
		}
	}

	// Has anything actually changed?
	if givenBefore != modeGiven {
		// inform requester of the change made
		h.route <- t.makeInvite(sess.uid, target, sess.uid, types.InvInfo, userData.modeWant, modeGiven,
			map[string]string{"before": givenBefore.String()})
	}

	return nil
}

// replyGetInfo is a response to a get.info request on a topic, sent to just the session as a {meta} packet
func (t *Topic) replyGetInfo(sess *Session, id string, created bool) error {
	now := time.Now().UTC().Round(time.Millisecond)

	pud, full := t.perUser[sess.uid]

	info := &MsgTopicInfo{
		CreatedAt: &t.created,
		UpdatedAt: &t.updated}

	if t.public != nil {
		info.Public = t.public
	} else if full {
		// p2p topic
		info.Public = pud.public
	}

	// Request may come from a subscriber or a stranger. Give a stranger a lot less info than a subscriber
	if full {
		if pud.modeGiven&pud.modeWant&types.ModeShare != 0 {
			info.DefaultAcs = &MsgDefaultAcsMode{
				Auth: t.accessAuth.String(),
				Anon: t.accessAnon.String()}
		}

		info.Acs = &MsgAccessMode{
			Want:  pud.modeWant.String(),
			Given: pud.modeGiven.String()}

		info.Private = pud.private

		if !t.lastMessage.IsZero() {
			info.LastMessage = &t.lastMessage
		}

		if when, ok := pud.lastSeenTag[sess.tag]; ok {
			info.LastSeenTag = &when
		}

		if len(pud.lastSeenTag) > 0 {
			tag, when := mostRecent(pud.lastSeenTag)
			if !when.IsZero() {
				info.LastSeen = &MsgLastSeenInfo{When: when, Tag: tag}
			}
		}

		// When a topic is first created, its name may change. Report the new name
		if created {
			info.Name = t.name
		}
	}

	simpleByteSender(sess.send, &ServerComMessage{
		Meta: &MsgServerMeta{Id: id, Topic: t.original, Info: info, Timestamp: &now}})

	return nil
}

// replySetInfo updates topic metadata, saves it to DB,
// replies to the caller as {ctrl} message, generates {pres} update if necessary
func (t *Topic) replySetInfo(sess *Session, set *MsgClientSet) error {
	now := time.Now().UTC().Round(time.Millisecond)

	assignAccess := func(upd map[string]interface{}, mode *MsgDefaultAcsMode) error {
		if auth, anon, err := parseTopicAccess(mode, types.ModeInvalid, types.ModeInvalid); err != nil {
			return err
		} else if auth&types.ModeOwner != 0 || anon&types.ModeOwner != 0 {
			return errors.New("default 'O' access is not permitted")
		} else {
			access := make(map[string]interface{})
			if auth != types.ModeInvalid {
				access["Auth"] = auth
			}
			if anon != types.ModeInvalid {
				access["Anon"] = anon
			}
			if len(access) > 0 {
				upd["Access"] = access
			}
		}
		return nil
	}

	assignPubPriv := func(upd map[string]interface{}, what string, p interface{}) {
		if isNullValue(p) {
			upd[what] = nil
		} else {
			upd[what] = p
		}
	}

	updateCached := func(upd map[string]interface{}) {
		if tmp, ok := upd["Access"]; ok {
			access := tmp.(map[string]interface{})
			if auth, ok := access["Auth"]; ok {
				t.accessAuth = auth.(types.AccessMode)
			}
			if anon, ok := access["Anon"]; ok {
				t.accessAnon = anon.(types.AccessMode)
			}
		}
		if public, ok := upd["Public"]; ok {
			t.public = public
		}
	}

	var err error

	user := make(map[string]interface{})
	topic := make(map[string]interface{})
	sub := make(map[string]interface{})
	if t.cat == TopicCat_Me {
		// Update current user
		if set.Info.DefaultAcs != nil {
			err = assignAccess(user, set.Info.DefaultAcs)
		}
		if set.Info.Public != nil {
			assignPubPriv(user, "Public", set.Info.Public)
		}
	} else if t.cat == TopicCat_Grp && t.owner == sess.uid {
		// Update current topic
		if set.Info.DefaultAcs != nil {
			err = assignAccess(topic, set.Info.DefaultAcs)
		}
		if set.Info.Public != nil {
			assignPubPriv(topic, "Public", set.Info.Public)
		}
	}

	if err != nil {
		simpleByteSender(sess.send, ErrMalformed(set.Id, set.Topic, now))
		return err
	}

	if set.Info.Private != nil {
		assignPubPriv(sub, "Private", set.Info.Private)
	}

	var change int
	if len(user) > 0 {
		err = store.Users.Update(t.appid, sess.uid, user)
		change++
	}
	if err == nil && len(topic) > 0 {
		err = store.Topics.Update(t.appid, t.name, topic)
		change++
	}
	if err == nil && len(sub) > 0 {
		err = store.Subs.Update(t.appid, t.name, sess.uid, sub)
		change++
	}

	if err != nil {
		simpleByteSender(sess.send, ErrUnknown(set.Id, set.Topic, now))
		return err
	} else if change == 0 {
		simpleByteSender(sess.send, ErrMalformed(set.Id, set.Topic, now))
		return errors.New("set generated no update to DB")
	}

	// Update values cached in the topic object
	if private, ok := sub["Private"]; ok {
		pud := t.perUser[sess.uid]
		pud.private = private
		t.perUser[sess.uid] = pud
	}
	if t.cat == TopicCat_Me {
		updateCached(user)
	} else if t.cat == TopicCat_Grp && t.owner == sess.uid {
		updateCached(topic)
	}

	simpleByteSender(sess.send, NoErr(set.Id, set.Topic, now))

	// TODO(gene) send pres update

	return nil
}

// replyGetSub is a response to a get.sub request on a topic - load a list of subscriptions/subscribers,
// send it just to the session as a {meta} packet
func (t *Topic) replyGetSub(sess *Session, id string) error {
	now := time.Now().UTC().Round(time.Millisecond)

	var subs []types.Subscription
	var err error

	if t.cat == TopicCat_Me {
		// Fetch user's subscriptions, with Topic.Public denormalized into subscription
		//log.Println("replyGetSub: loading me list of subscriptions")
		subs, err = store.Users.GetTopics(sess.appid, sess.uid, nil)
	} else if t.cat == TopicCat_P2P {
		// Fetch subscriptions for two users, User.Public denormalized into other user's subscription
		subs, err = store.Topics.GetUsers(sess.appid, t.name, nil)
	} else {
		// Fetch subscriptions for a topic, with User.Public denormalized into subscription
		subs, err = store.Topics.GetUsers(sess.appid, t.name, nil)
	}

	if err != nil {
		log.Printf("topic(%s): error loading subscriptions %s\n", t.original, err.Error())
		reply := ErrUnknown(id, t.original, now)
		simpleByteSender(sess.send, reply)
		return err
	}

	meta := &MsgServerMeta{Id: id, Topic: t.original, Timestamp: &now}
	if len(subs) > 0 {
		meta.Sub = make([]MsgTopicSub, 0, len(subs))
		for _, sub := range subs {
			var mts MsgTopicSub
			uid := types.ParseUid(sub.User)
			if t.cat == TopicCat_Me {
				mts.Topic = sub.Topic
				mts.With = sub.GetWith()
				mts.LastMsg = sub.LastMessageAt
				if when, ok := sub.LastSeen[sess.tag]; ok && !when.IsZero() {
					mts.LastSeenTag = &when
				}
				if len(sub.LastSeen) > 0 {
					tag, when := mostRecent(sub.LastSeen)
					if !when.IsZero() {
						mts.LastSeen = &MsgLastSeenInfo{When: when, Tag: tag}
					}
				}
			} else {
				mts.User = uid.UserId()
			}
			mts.UpdatedAt = sub.UpdatedAt
			mts.AcsMode = (sub.ModeGiven & sub.ModeWant).String()
			mts.Public = sub.GetPublic()
			if uid == sess.uid {
				mts.Private = sub.Private
			}

			meta.Sub = append(meta.Sub, mts)
		}
	}

	simpleByteSender(sess.send, &ServerComMessage{Meta: meta})

	return nil
}

// replySetSub is a response to new subscription request or an update to a subscription {set.sub}:
// update topic metadata cache, save/update subs, reply to the caller as {ctrl} message, generate an invite
func (t *Topic) replySetSub(h *Hub, sess *Session, set *MsgClientSet) error {
	now := time.Now().UTC().Round(time.Millisecond)

	var uid types.Uid
	if uid = types.ParseUserId(set.Sub.User); uid.IsZero() && set.Sub.User != "" {
		// Invalid user ID
		simpleByteSender(sess.send, ErrMalformed(set.Id, set.Topic, now))
		return errors.New("invalid user id")
	}

	// if set.User is not set, request is for the current user
	if !uid.IsZero() {
		uid = sess.uid
	}

	if uid == sess.uid {
		return t.requestSub(h, sess, set.Id, set.Sub.Mode, set.Sub.Info, nil)
	} else {
		return t.approveSub(h, sess, uid, set)
	}
}

// replyGetData is a response to a get.data request - load a list of stored messages, send them to session as {data}
// response goes to a single session rather than all sessions in a topic
func (t *Topic) replyGetData(sess *Session, id string, req *MsgBrowseOpts) error {
	now := time.Now().UTC().Round(time.Millisecond)

	opts := msgOpts2storeOpts(req, sess.tag, t.perUser[sess.uid].lastSeenTag[sess.tag])

	messages, err := store.Messages.GetAll(sess.appid, t.name, opts)
	if err != nil {
		log.Println("topic: error loading topics ", err)
		reply := ErrUnknown(id, t.original, now)
		simpleByteSender(sess.send, reply)
		return err
	}
	log.Println("Loaded messages ", len(messages))

	// Push the list of updated topics to the client as data messages
	if messages != nil {
		for _, mm := range messages {
			from := types.ParseUid(mm.From)
			msg := &ServerComMessage{Data: &MsgServerData{
				Topic:     t.original,
				From:      from.UserId(),
				Timestamp: mm.CreatedAt,
				Content:   mm.Content}}
			simpleByteSender(sess.send, msg)
		}
	}

	return nil
}

func (t *Topic) makeInvite(notify, target, from types.Uid, act types.InviteAction, modeWant,
	modeGiven types.AccessMode, info interface{}) *ServerComMessage {

	// FIXME(gene): this is a workaround for gorethink's broken way of marshalling json
	inv, err := json.Marshal(MsgInvitation{
		Topic:  t.name,
		User:   target.UserId(),
		Action: act.String(),
		Acs:    MsgAccessMode{modeWant.String(), modeGiven.String()},
		Info:   info})
	if err != nil {
		log.Println(err)
	}
	converted := map[string]interface{}{}
	err = json.Unmarshal(inv, &converted)
	if err != nil {
		log.Println(err)
	}
	// endof workaround

	msg := &ServerComMessage{Data: &MsgServerData{
		Topic:     "me",
		From:      from.UserId(),
		Timestamp: time.Now().UTC().Round(time.Millisecond),
		Content:   converted},
		rcptto: notify.UserId(),
		appid:  t.appid}
	log.Printf("Invite generated: %#+v", msg.Data)
	return msg
}

// This is a newly created topic (!me or generic), announce topic presence
func (t *Topic) presPubTopicOnline() error {

	// Load the list of users interested in this topic
	// Blocking call
	subs, err := store.Topics.GetSubs(t.appid, t.name, nil)
	if err != nil {
		log.Println("Presence: topic: error loading topic sharing ", err)
		return err
	}

	var count = 0
	if len(subs) > 0 {
		// Publish update to subscribers
		t.pushTo = make(map[types.Uid]bool)
		update := &MsgServerPres{Topic: "me", What: "on", User: t.name}
		var uid types.Uid

		for _, sub := range subs {
			if (sub.ModeGiven & sub.ModeWant & types.ModePres) != 0 {
				uid.UnmarshalText([]byte(sub.User))
				t.pushTo[uid] = true
				globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
					rcptto: "usr" + sub.User}
				count++
			}
		}

		log.Printf("Presence: topic '%s' came online, updated %d listeners", t.name, count)

	} else {
		log.Println("Presence: orphaned topic:", t.name)
	}

	return nil
}

// Publish presence announcement
func (t *Topic) presPubChange(action string) {
	if t.pushTo == nil {
		return
	}

	update := &MsgServerPres{Topic: "me", What: action, User: t.name}

	for usr := range t.pushTo {
		globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
			rcptto: usr.UserId()}

		log.Println("Presence: sent online status to ", usr.String())
	}
}

// Process request to start/stop receiving presence updates from this topic
func (t *Topic) presProcReq(hub *Hub, req *presSubsReq) {

	log.Printf("Presence: topic[%s]: presence request from '%s'", t.name, req.id.String())

	if req.subscribe {
		if t.pushTo == nil {
			t.pushTo = make(map[types.Uid]bool)
		}
		t.pushTo[req.id] = true
		var action string
		if len(t.sessions) > 0 {
			action = "on"
		} else {
			action = "off"
		}
		hub.route <- &ServerComMessage{
			Pres:   &MsgServerPres{Topic: "me", What: action, User: t.name},
			appid:  t.appid,
			rcptto: req.id.UserId()}
	} else if t.pushTo != nil {
		delete(t.pushTo, req.id)
	}
}

// User attached to !me topic, get the current state of topics he is interested in
func (me *Topic) presSnapshot() error {
	subs, err := store.Users.GetSubs(me.appid, me.owner, nil)
	if err != nil {
		log.Println("Presence: topic me: error loading user's subscriptions ", err)
		return err
	}

	var count = 0
	if len(subs) > 0 {
		list := make([]string, len(subs))

		for _, sub := range subs {
			if (sub.ModeGiven & sub.ModeWant & types.ModePres) != 0 {
				list = append(list, sub.Topic)
				count++
			}
		}

		if count > 0 {

		}
	}

	return nil
}

// evictUser evicts all user's sessions from the topic and clear's user's cached data, if requested
func (t *Topic) evictUser(uid types.Uid, clear bool, ignore *Session) {
	now := time.Now().UTC().Round(time.Millisecond)
	note := NoErrEvicted("", t.original, now)

	if clear {
		// Delete per-user data
		delete(t.perUser, uid)
	}

	// Detach all user's sessions
	for sess, _ := range t.sessions {
		if sess.uid == uid {
			delete(t.sessions, sess)
			sess.detach <- t.name
			if sess != ignore {
				sess.QueueOut(note)
			}
		}
	}
}

func msgOpts2storeOpts(req *MsgBrowseOpts, tag string, lastSeen time.Time) *types.BrowseOpt {
	var opts *types.BrowseOpt
	if req != nil {
		opts = &types.BrowseOpt{AscOrder: req.Ascnd, Limit: req.Limit}
		if req.Since != nil {
			opts.Since = *req.Since
		}
		if req.Before != nil {
			opts.Before = *req.Before
		}
	} else if tag != TAG_UNDEF && !lastSeen.IsZero() {
		opts = &types.BrowseOpt{Since: lastSeen}
	}
	return opts
}

func mostRecent(vals map[string]time.Time) (tag string, max time.Time) {
	for key, val := range vals {
		if val.After(max) {
			max = val
			tag = key
		}
	}
	return
}

func isNullValue(i interface{}) bool {
	// Del control character
	const CLEAR_VALUE = "\u2421"
	if str, ok := i.(string); ok {
		return str == CLEAR_VALUE
	}
	return false
}
