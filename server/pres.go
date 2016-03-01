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
 *  File        :  pres.go
 *  Author      :  Gene Sokolov
 *  Created     :  13-Nov-2015
 *
 ******************************************************************************
 *
 *  Description :
 *
 *  Handling of presence notifications
 *
 *****************************************************************************/

package main

import (
	"log"
	"strings"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

/*
1. User joined `me`. Tell the user which of his/her topics/users of interest are currently online:
	a. if the topic is loaded at this time
		i. load the list of user's subscriptions
		ii. store just the names in the t.perSubs with online set to false
	  	iii. send a {pres} to t.perSub that the user is online
		iv. Other 'me's will cache user's status in t.perSubs; grp & p2p won't cache
		v. Other topics reply with their own online status
	b. if the topic is already loaded, do nothing.
	c. when the user subscribes (grp or p2p), add new subscription to t.perSub
	`{pres topic="me" src="<user ID>" what="on" ua="<user agent>"}`
2. User went offline (left `me` topic).
	The message is sent to all users who have P2P topics with the first user. Users receive this event on
	the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"off"`:
	`{pres topic="me" src="<user ID>" what="off" ua="..."}`.
3. User updates `public` data. The event is sent to all users who have P2P topics with the first user.
	Users receive `{pres topic="me" src="<user ID>" what="upd"}`.
4. User joins/leaves a topic. This event is sent to other users who currently joined the topic:
	`{pres topic="<topic name>" src="<user ID>" what="on|off"}`.
5. Topic is activated/deactivated. Topic becomes active when at least one user joins it. The topic becomes
	inactive when all users leave it (possibly with some delay). The event is sent to all topic subscribers.
	They will receive it on their `me` topics:
	`{pres topic="me" src="<topic name>" what="on|off"}`.
6. A message published in the topic. The event is sent to users who have subscribed to the topic but currently
	not joined (those who have joined will receive the {data}):
	`{pres topic="me" src="<topic name>" what="msg" seq=123}`.
7. Topic's `public` is updated. The event is sent to all topic subscribers.
	Users receive `{pres topic="me" src="<topic name>" what="upd"}`.
8. User is has multiple sessions attached to 'me'. Sessions have different User Agents. Notify of UA change:
	the message is sent to all users who have P2P topics with the first user. Users receive this event on
	the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"ua"`:
	`{pres topic="me" src="<user ID>" what="ua" ua="<user agent>"}`.
9. User sent a {note} packet indicating that some or all of the messages in the topic as received or read, OR sent
	a {del} message soft-deleting some messages. Sent only to other user's sessions (not the one that sent the request).
	a. read/received: `{pres topic="me" src="<topic name>" what="recv|read" seq=123}`.
	b. deleted: `{pres topic="me" src="<topic name>" what="del" seq=123}`
10. Messages were hard-deleted. The event is sent to all topic subscribers, joined and not joined:
	a. joined: `{pres topic="<topic name>" src="<user id>" what="del" seq=123}`.
	b. not joined: `{pres topic="me" src="<topic name>" what="del" seq=123}`.
*/

// loadContacts initializes topic.perSubs to support presence notifications
// Case 1.a.i, 1.a.ii
func (t *Topic) loadContacts(uid types.Uid) error {
	subs, err := store.Users.GetSubs(uid)
	if err != nil {
		return err
	}

	t.perSubs = make(map[string]perSubsData, len(subs))
	for _, sub := range subs {
		//log.Printf("Pres 1.a.i-ii: topic[%s]: processing sub '%s'", t.name, sub.Topic)
		topic := sub.Topic
		if strings.HasPrefix(topic, "p2p") {
			if uid1, uid2, err := types.ParseP2P(topic); err == nil {
				if uid1 == uid {
					topic = uid2.UserId()
				} else {
					topic = uid1.UserId()
				}
			} else {
				continue
			}
		} else if topic == t.name {
			// No need to push updates to self
			continue
		}
		//log.Printf("Pres 1.a.i-ii: topic[%s]: caching as '%s'", t.name, topic)
		t.perSubs[topic] = perSubsData{}
	}
	//log.Printf("Pres 1.a.i-ii: topic[%s]: total cached %d", t.name, len(t.perSubs))
	return nil
}

// Me topic activated, deactivated or updated, push presence to contacts
// Case 1.a.iii, 2, 3
func (t *Topic) presPubMeChange(what string, ua string) {
	// Push update to subscriptions

	update := &MsgServerPres{Topic: "me", What: what, Src: t.name, UserAgent: ua,
		wantReply: (what == "on")}
	for topic, _ := range t.perSubs {
		globals.hub.route <- &ServerComMessage{Pres: update, rcptto: topic}

		//log.Printf("Pres 1.a.iii, 2, 3: from '%s' (src: %s) to %s [%s], ua: '%s'", t.name, update.Src, topic, what, ua)
	}
}

// This topic got a request from a 'me' topic to start/stop sending presence updates.
// Cases 1.a.iv, 1.a.v
func (t *Topic) presProcReq(fromTopic string, online, wantReply bool) {
	//log.Printf("Pres 1.a.iv, 1.a.v: topic[%s]: req from '%s', online: %v, wantReply: %v",
	//	t.name, fromTopic, online, wantReply)

	doReply := wantReply
	if t.cat == types.TopicCat_Me {
		if psd, ok := t.perSubs[fromTopic]; ok {
			// If requester's online status has not changed, do not reply, otherwise an endless loop will happen
			// Introducing isReply to ensure unnecessary {pres} is not sent:
			// A[online, B:off] to B[online, A:off]: {pres A on}
			// B[online, A:on] to A[online, B:off]: {pres B on}
			// A[online, B:on] to B[online, A:on]: {pres A on} <<-- unnecessary, that's why wantReply is needed
			doReply = (doReply && (psd.online != online))
			psd.online = online
			t.perSubs[fromTopic] = psd
		} else {
			doReply = false
			//log.Printf("Pres 1.a.iv, 1.a.v: topic[%s]: request from untracked topic %s", t.name, fromTopic)
		}
	}

	if online && doReply {
		globals.hub.route <- &ServerComMessage{
			// Topic is 'me' even for group topics; group topics will use 'me' as a signal to drop the message
			// without forwarding to sessions
			Pres: &MsgServerPres{Topic: "me", What: "on", Src: t.name}, rcptto: fromTopic}
	}
}

// Publish announcement to topic
// Cases 4, 7
func (t *Topic) presPubChange(src, what string) {

	globals.hub.route <- &ServerComMessage{
		Pres: &MsgServerPres{Topic: t.original, What: what, Src: src}, rcptto: t.name}

	//log.Printf("Pres 4,7: from '%s' (src: %s) [%s]", t.name, src, what)
}

// Non-'me' topic activated or deactivated, announce topic presence to its subscribers
// Case 5
func (t *Topic) presPubTopicOnline(online bool) {
	var what string
	if online {
		what = "on"
	} else {
		what = "off"
	}

	// Publish update to topic subscribers
	update := &MsgServerPres{Topic: "me", What: what, Src: t.original}
	for uid, pud := range t.perUser {
		if pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: update, rcptto: uid.UserId()}

			// log.Printf("Pres 5: from '%s' (src %s) to '%s' [%s]", t.name, update.Src, uid.UserId(), what)
		}
	}
}

// Message sent in the topic, notify topic-offline users
// Case 6
func (t *Topic) presPubMessageSent(seq int) {
	log.Printf("Pres 6: from %s [msg=%d]", t.name, seq)

	update := &MsgServerPres{Topic: "me", What: "msg", Src: t.original, SeqId: seq}

	for uid, pud := range t.perUser {
		if pud.online == 0 && pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: update, rcptto: uid.UserId()}

			log.Printf("Pres 6: src: %s to %s", update.Src, uid.UserId())
		} else {
			log.Printf("Pres 6: SKIPPED src: %s to %s due to online=%d or mode %v", update.Src, uid.UserId(),
				pud.online, (pud.modeGiven&pud.modeWant&types.ModePres != 0))
		}
	}
}

// User Agent has changed
// Case 8
func (t *Topic) presPubUAChange(ua string) {
	if ua == "" || ua == t.userAgent {
		return
	}
	t.userAgent = ua

	// Push update to subscriptions
	update := &MsgServerPres{Topic: "me", What: "ua", Src: t.name, UserAgent: ua}
	for topic, _ := range t.perSubs {
		globals.hub.route <- &ServerComMessage{Pres: update, rcptto: topic}

		// log.Printf("Case 8: from '%s' to %s [%s]", t.name, topic, ua)
	}
}

// Let other sessions of a given user know that what messages are now received/read
// Cases 9.a, 9.b
func (t *Topic) presPubMessageCount(skip *Session, clear, recv, read int) {
	if pud, ok := t.perUser[skip.uid]; ok {
		var what string
		var seq int
		if read > 0 {
			what = "read"
			seq = read
		} else {
			what = "recv"
			seq = recv
		}
		update := &MsgServerPres{Topic: "me", What: what, Src: t.original, SeqId: seq}

		if pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: update, rcptto: skip.uid.UserId(), skipSession: skip}

			// log.Printf("Case 9: from '%s' to %s [read]", t.name, skip.uid.UserId())
		}
	}
}

// Messages deleted in the topic, notify online users and topic-offline users
// Case 10
func (t *Topic) presPubMessageDel(sess *Session, clear int) {
	offline := &MsgServerPres{Topic: "me", What: "del", Src: t.original, SeqId: clear}

	// Broadcast to topic
	globals.hub.route <- &ServerComMessage{
		Pres:   &MsgServerPres{Topic: t.original, What: "del", Src: sess.uid.UserId(), SeqId: clear},
		rcptto: t.name, skipSession: sess}

	// Broadcast to topic-offline users
	for uid, pud := range t.perUser {
		if pud.online == 0 && pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: offline, rcptto: uid.UserId()}

			// log.Printf("Pres 10: from %s (src: %s), to %s [msg=%d]", t.name, update.Src, uid.UserId(), seq)
		}
	}
}
