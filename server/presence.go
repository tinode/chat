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
 *  File        :  presence.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************/

/******************************************************************************
 *
 *  Description :
 *
 *  Handler of presence notifications
 *
 * User subscribes to topic !pres to receive online/offline notifications
 *   - immediately after subscription get a list of contacts with their current statuses
 *   - receive live status updates
 *   - Params contain "filter" - list of base64-encoded GUIDs to be notified about
 *     -- if params["filter"] == "!contacts", then contacts are loaded from DB
 *
 * Publishing to !pres: updates filter:
 *   - params["filter_add"] == list of ids to add
 *   - params["filter_rem"] == list of ids to remove
 *     -- such changes are persisted to db
 *
 * Online/Offline notifications come from the !pres topic as Data packets
 *
 * User subscribes to !me to announce his online presence
 * User publishes to !me to update his status information ("DND", "Away" or an object)
 *
 *****************************************************************************/
package main

import (
	"github.com/tinode/chat/server/store/types"
	"log"
)

type UserPresence struct {
	id types.Uid
	// Publisher to !pres (subscribed to !me), regardless of the number of readers
	// Equivalent to user being online
	publisher bool
	// Subscriber of !pres channel, regardless of the number of contacts
	subscriber bool
	// Generalized status line, like a string "DND", "Away", or something more complex
	status interface{}

	// List of users who are subscribed to this user, publisher == true
	pushTo map[types.Uid]bool

	// Index (User -> subscribed to), subscribed == true
	attachTo map[types.Uid]bool
}

const (
	ActionOnline       = iota // user subscribed to !me
	ActionOffline             // user unsubscribed from !me
	ActionSubscribed          // user subscribed to !pres
	ActionUnsubscribed        // unsubscribed from !pres
	ActionStatus              // user updated status
	ActionFinish              // the system is shutting down
)

type PresenceRequest struct {
	AppId  uint32
	Id     types.Uid
	Action int

	// Session being subscribed or unsubscribed
	Sess *Session

	// List of UIDs +  to subscribe to or unsubscribe from (or block)
	Contacts []types.TopicAccess

	// Status line like "Away" or "DND"
	Status interface{}
}

// Add another subscriber to user
func (up *UserPresence) attachReader(id types.Uid) {
	up.pushTo[id] = true
}

// Remove subscriber from user
func (up *UserPresence) detachReader(id types.Uid) {
	delete(up.pushTo, id)
}

// Save subscription information
// User is not subscribed or unsubscribed here
func (up *UserPresence) subscribeTo(id types.Uid) {
	up.attachTo[id] = true
}

// Remove subscription information
// User is not subscribed or unsubscribed here
func (up *UserPresence) unsubscribeFrom(id types.Uid) {
	// This is expected to panicif up.attachTo is nil
	delete(up.attachTo, id)
}

// Known status publishers and subscribers, online and offline
// - publisher (online or offline) -> some online users want to know his status
// - subscriber (online only) -> wants to receive status updates
type presenceIndex struct {
	index  map[types.Uid]*UserPresence
	action chan *PresenceRequest
}

// Initialize presence index and return channel for receiving updates
func InitPresenceHandler() chan<- *PresenceRequest {
	pi := presenceIndex{
		index:  make(map[types.Uid]*UserPresence),
		action: make(chan *PresenceRequest, 64)}

	go presenceHandler(pi)

	return pi.action
}

func presenceHandler(pi presenceIndex) {
	for {
		select {
		case msg := <-pi.action:
			me := pi.index[msg.Id]

			switch msg.Action {
			case ActionOnline:
				if me == nil {
					// The user is not subscribed to !pres, no one is subscribed to him
					pi.index[msg.Id] = &UserPresence{id: msg.Id, status: msg.Status, publisher: true}
					log.Println("Presence: user came online but no one cares")
				} else {
					pi.Online(msg.AppId, me, msg.Status)
					log.Println("Presence: user came online, updated listeners")
				}
				log.Println("Presence: Online done")
			case ActionOffline:
				// Will panic if me == nil
				pi.Offline(msg.AppId, me)
				log.Println("Presence: Offline done")
			case ActionSubscribed:
				if me == nil {
					// The user is not subscribed to either !pres or !me yet
					me = &UserPresence{id: msg.Id}
					pi.index[msg.Id] = me
				}
				pi.SubPresence(me, msg.Sess, msg.Contacts)
				log.Println("Presence: Subscribed done")
			case ActionUnsubscribed:
				// Will panic if me == nil
				pi.UnsubPresence(me, msg.Contacts)
				log.Println("Presence: Unsibscribed done")
			case ActionStatus:
				if me != nil {
					// User could be offline, i.e. status updated through REST API
					pi.Status(msg.AppId, me, msg.Status)
				}
				log.Println("Presence: Status done")
			case ActionFinish:
				log.Println("presence: finished")
				return
			}
		}
	}
}

// Attach user to publisher
// Subscriber (user.id==id) will start receiving presence notifications from publisher (user.id==to)
func (pi presenceIndex) attach(to, id types.Uid) {
	pub := pi.index[to]
	if pub == nil {
		// User is not online yet either
		pub = &UserPresence{id: to}
		pi.index[to] = pub
	}

	if pub.pushTo == nil {
		// User is offline and no one was subscribed to him before
		pub.pushTo = make(map[types.Uid]bool)
	}

	// Don't set pub.publisher = true here, the user does not really publish anything unless he is also online

	pub.attachReader(id)
}

// Detach subscriber from publisher (i.e. subscriber went offline)
// Subscriber (id) will no longer receive presence notifications from publisher (from)
// If publisher no longer has subscribers and offline, remove him from index
func (pi presenceIndex) detach(from, id types.Uid) {
	pub := pi.index[from]
	if pub == nil {
		// If it happens, it's a bug.
		log.Panic("PresenceIndex.Detach called for unknown user")
		return
	}

	pub.detachReader(id)
	if len(pub.pushTo) == 0 {
		pub.pushTo = nil

		// Publisher is offline and has no subscribers
		if !pub.publisher && !pub.subscriber {
			delete(pi.index, from)
		}
	}
}

type SimpleOnline struct {
	Who    string      `json:"who"`
	Online bool        `json:"online"`
	Status interface{} `json:"status,omitempty"`
	Params interface{} `json:"params,omitempty"`
}

// User subscribed to "!pres" or updated a list of contacts
//   Attach him to pushTo of other users, even if they are currently offline,
//   so when they come online they start pushing to this user
// - id: current user
// - attachTo: list of users to subscribe to
// SubPresence may be called multiple time for a single user
func (pi presenceIndex) SubPresence(me *UserPresence, sess *Session, attachTo []types.TopicAccess) {

	if len(attachTo) != 0 {
		if me.attachTo == nil {
			me.attachTo = make(map[types.Uid]bool)
		}

		// Attach user to pushTo of other users, send their statuses to the newly subscribed user
		// Read from my subscriptions
		onlineList := make([]SimpleOnline, len(attachTo))
		var i int
		for _, other := range attachTo {
			if (other.Given & other.Want & types.ModePres) == 0 {
				continue
			}
			// pi.attach will create a user to attach to if needed
			uid := types.ParseUid(other.User)
			pi.attach(uid, me.id)
			me.subscribeTo(uid)
			user := pi.index[uid]
			i++
			//
			onlineList[i] = SimpleOnline{Who: user.id.String(), Online: user.publisher, Status: user.status}
		}
		// Sending to subscribed session only, other sessions of this !pres topic don't care
		go simpleByteSender(sess.send, &ServerComMessage{Data: &MsgServerData{Topic: "!pres", Content: onlineList}})
	}

	me.subscriber = true
}

// User subscribed to "!me" announcing online presence
// Publish user's online presence to subscribers
//  - id: user's id
//  - extra: some extra information user wants to publish, like "DND", "AWAY", {emoticon, "I'm going to the movies"}
func (pi presenceIndex) Online(appid uint32, me *UserPresence, extra interface{}) {

	if len(me.pushTo) > 0 {
		if extra == nil && me.status != nil {
			extra = me.status
		} else {
			me.status = extra
		}

		// Publish update to subscribers
		online := SimpleOnline{Who: me.id.String(), Online: true, Status: extra}
		update := &MsgServerData{Topic: "!pres",
			Content: []SimpleOnline{online}}

		for guid, _ := range me.pushTo {
			globals.hub.route <- &ServerComMessage{Data: update, appid: appid, rcptto: "!pres:" + guid.String()}
			log.Println("Sent online status to ", guid.String())
		}
	}

	me.publisher = true
}

// Either !pres topic was deleted because everyone unsubscribed, or user just deleted some contacts
// Remove him from pushTo of other users
// FIXME(gene): handle the following case:
//  1. User has online subscribers
//  2. User goes offline, but because he has subscribers he stays in index
//  3. All subscribers unsub from !pres.
//  4. The user is offline with no subscribers, but record stays in index
func (pi presenceIndex) UnsubPresence(me *UserPresence, detachFrom []types.TopicAccess) {
	if me.subscriber {
		// Remove user from pushTo of other users

		if detachFrom != nil {
			// Handle partial unsubscribe
			for _, other := range detachFrom {
				uid := types.ParseUid(other.User)
				delete(me.attachTo, uid)
				pi.detach(uid, me.id)
			}
		} else if len(me.attachTo) != 0 {
			// unsubscribing from all
			for uid, _ := range me.attachTo {
				pi.detach(uid, me.id)
			}

			me.subscriber = false
		}

		if len(me.attachTo) == 0 {
			me.attachTo = nil
		}
	}

	if !me.publisher && !me.subscriber && len(me.pushTo) == 0 {
		// User is offline with no subscribers
		delete(pi.index, me.id)
	}
}

// User unsubscribed from !me (may still be subscribed to !pres
// Announce his disappearance to subscribers
func (pi presenceIndex) Offline(appid uint32, me *UserPresence) {

	if me.publisher {
		// Let subscribers know that user went offline
		online := SimpleOnline{Who: me.id.String(), Online: false}
		data := &MsgServerData{Topic: "!pres", Content: []SimpleOnline{online}}

		for uid, _ := range me.pushTo {
			globals.hub.route <- &ServerComMessage{Data: data, appid: appid, rcptto: "!pres:" + uid.String()}
		}

		me.publisher = false
	}

	if len(me.pushTo) == 0 && !me.subscriber {
		// Also, user is not subscribed to anyone, remove him from index
		delete(pi.index, me.id)
	}
}

// User updated his status
func (pi presenceIndex) Status(appid uint32, me *UserPresence, status interface{}) {
	me.status = status

	if me.publisher && len(me.pushTo) > 0 {
		data := &MsgServerData{Topic: "!pres",
			Content: map[string]interface{}{"who": me.id.String(), "status": status}}
		for uid, _ := range me.pushTo {
			globals.hub.route <- &ServerComMessage{Data: data, appid: appid, rcptto: "!pres:" + uid.String()}
		}
	}
}
