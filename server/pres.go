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
 *  File        :  lphandler.go
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

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

/*
1. User joined `me`. Tell the user which of his/her topics/users of interest are currently online
2. User came online or went offline (joined/left `me` topic).
	The message is sent to all users who have P2P topics with the first user. Users receive this event on
	the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"on"` or `"off"`:
	`{pres topic="me" src="<user ID>" what="on|off"}`.
3. User updates `public` data. The event is sent to all users who have P2P topics with the first user.
	Users receive `{pres topic="me" src="<user ID>" what="upd"}`.
4. User joins/leaves a topic. This event is sent to other users who currently joined the topic:
	`{pres topic="<topic name>" src="<user ID>" what="on|off"}`.
5. Topic is activated/deactivated. Topic becomes active when at least one user joins it. The topic becomes
	inactive when all users leave it (possibly with some delay). The event is sent to all topic subscribers.
	They will receive it on their `me` topics:
	`{pres topic="me" src="<topic name>" what="on|off"}`.
6. A message was sent to the topic. The event is sent to users who have subscribed to the topic but currently
	not joined `{pres topic="<topic name>" what="msg"}`.
7. Topic's `public` is updated. The event is sent to all topic subscribers.
	Users receive `{pres topic="me" src="<topic name>" what="upd"}`.
*/

// Topic activated or deactivated, announce presence (case 5 above)
func (t *Topic) presPubTopicOnline(online bool) {
	if t.cat == TopicCat_Me {

	} else {
		var count = 0

		// Publish update to subscribers
		update := &MsgServerPres{Topic: "me", What: "on", Src: t.original}
		if !online {
			update.What = "off"
		}
		for uid, pud := range t.perUser {
			if pud.modeGiven&pud.modeWant&types.ModePres != 0 {
				globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
					rcptto: uid.UserId()}
				count++
			}
		}

		log.Printf("Presence: topic '%s' came online, updated %d listeners", t.name, count)
	}
}

// Publish presence announcement
func (t *Topic) presPubChange(src, action string) {
	if t.cat == TopicCat_Me {

	} else {
		update := &MsgServerPres{Topic: t.original, What: action, Src: src}

		for uid, pud := range t.perUser {
			if pud.modeGiven&pud.modeWant&types.ModePres != 0 {
				globals.hub.route <- &ServerComMessage{Pres: update, appid: t.appid,
					rcptto: uid.UserId()}
			}
			log.Println("Presence: sent update to ", uid.String())
		}
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
			Pres:   &MsgServerPres{Topic: "me", What: action, Src: t.name},
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
