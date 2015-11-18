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
	`{pres topic="me" src="<user ID>" what="on"}`
2. User went offline (left `me` topic).
	The message is sent to all users who have P2P topics with the first user. Users receive this event on
	the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"off"`:
	`{pres topic="me" src="<user ID>" what="off"}`.
3. User updates `public` data. The event is sent to all users who have P2P topics with the first user.
	Users receive `{pres topic="me" src="<user ID>" what="upd"}`.
4. User joins/leaves a topic. This event is sent to other users who currently joined the topic:
	`{pres topic="<topic name>" src="<user ID>" what="on|off"}`.
5. Topic is activated/deactivated. Topic becomes active when at least one user joins it. The topic becomes
	inactive when all users leave it (possibly with some delay). The event is sent to all topic subscribers.
	They will receive it on their `me` topics:
	`{pres topic="me" src="<topic name>" what="on|off"}`.
6. A message was sent to the topic. The event is sent to users who have subscribed to the topic but currently
	not joined `{pres topic="me" src="<topic name>" what="msg"}`.
7. Topic's `public` is updated. The event is sent to all topic subscribers.
	Users receive `{pres topic="me" src="<topic name>" what="upd"}`.
*/

// loadContacts initializes topic.perSubs to support presence notifications
// Case 1.a.i, 1.a.ii
func (t *Topic) loadContacts(uid types.Uid) error {
	subs, err := store.Users.GetSubs(t.appid, uid, nil)
	if err != nil {
		return err
	}

	t.perSubs = make(map[string]perSubsData, len(subs))
	for _, sub := range subs {
		topic := sub.Topic
		if strings.HasPrefix(topic, "p2p") {
			if uid1, uid2, err := types.ParseP2P(topic); err != nil {
				if uid1 == uid {
					topic = uid2.UserId()
				} else {
					topic = uid1.UserId()
				}
			} else {
				continue
			}
		}
		t.perSubs[topic] = perSubsData{}
	}
	return nil
}

// Me topic activated, deactivated or updated, push presence to contacts
// Case 1.a.iii, 2, 3
func (t *Topic) presPubMeChange(what string) {
	var count = 0

	// Push update to subscriptions
	update := &MsgServerPres{Topic: "me", What: what, Src: t.name}
	for topic, _ := range t.perSubs {
		globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid, rcptto: topic}
		count++
	}

	log.Printf("Presence: topic '%s' came online, updated %d listeners", t.name, count)
}

// This topic get a request from a 'me' topic to start/stop receiving presence updates from this topic.
// Return value indicates if the message should be forwarded to topic subscribers
// Cases 1.a.iv, 1.a.v
func (t *Topic) presProcReq(fromTopic string, online, isReply bool) bool {

	log.Printf("Presence: topic[%s]: presence request from '%s'", t.name, fromTopic)

	doReply := true
	if t.cat == TopicCat_Me {
		psd := t.perSubs[fromTopic]
		// If requester's online status has not changed, do not reply, otherwise an endless loop will happen
		// Introducing isReply to ensure unnecessary {pres} is not sent:
		// A[online, B:off] to B[online, A:off]: {pres A on}
		// B[online, A:on] to A[online, B:off]: {pres B on}
		// A[online, B:on] to B[online, A:on]: {pres A on} <<-- unnecessary, that's why isReply is needed
		doReply = (psd.online != online)
		psd.online = online
		t.perSubs[fromTopic] = psd
	}

	if online && doReply && !isReply {
		globals.hub.route <- &ServerComMessage{
			Pres:   &MsgServerPres{Topic: "me", What: "on", Src: t.name, isReply: true},
			appid:  t.appid,
			rcptto: fromTopic}
	}

	return ??
}

// Publish presence announcement
// Case 4, 7
func (t *Topic) presPubChange(src, what string) {

	update := &MsgServerPres{Topic: t.original, What: what, Src: src}

	for uid, pud := range t.perUser {
		if pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
				rcptto: uid.UserId()}
		}
		log.Println("Presence: sent update to ", uid.String())
	}
}

// Non-'me' topic activated or deactivated, announce topic presence to its subscribers
// Case 5
func (t *Topic) presPubTopicOnline(online bool) {
	var count = 0
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
			globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
				rcptto: uid.UserId()}
			count++
		}
	}

	log.Printf("Presence: topic '%s' came online, updated %d listeners", t.name, count)
}

// Message sent in the topic, notify topic-offline users
// Cases 6
func (t *Topic) presPubMessageSent() {
	var count = 0
	update := &MsgServerPres{Topic: "me", What: "msg", Src: t.original}

	for uid, pud := range t.perUser {
		if !pud.online && pud.modeGiven&pud.modeWant&types.ModePres != 0 {
			globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
				rcptto: uid.UserId()}
			count++
		}
	}
	log.Printf("Presence: topic %s, new message notification sent to%d listeners ", t.name, count)
}
