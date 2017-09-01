package main

import (
	"log"
	"strings"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

type PresParams struct {
	who       string
	userAgent string
	seqId     int
	seqList   []int
	dWant     string
	dGiven    string
}

func (p PresParams) packAcs() *MsgAccessMode {
	if p.dWant != "" || p.dGiven != "" {
		return &MsgAccessMode{Want: p.dWant, Given: p.dGiven}
	}
	return nil
}

// loadContacts initializes topic.perSubs to support presence notifications
func (t *Topic) loadContacts(uid types.Uid) error {
	subs, err := store.Users.GetSubs(uid)
	if err != nil {
		return err
	}

	t.perSubs = make(map[string]perSubsData, len(subs))
	for _, sub := range subs {
		//log.Printf("Pres loadContacts: topic[%s]: processing sub '%s'", t.name, sub.Topic)
		topic := sub.Topic
		var with types.Uid
		if strings.HasPrefix(topic, "p2p") {
			if uid1, uid2, err := types.ParseP2P(topic); err == nil {
				if uid1 == uid {
					topic = uid2.UserId()
					with = uid2
				} else {
					topic = uid1.UserId()
					with = uid1
				}
			} else {
				continue
			}
		} else if topic == t.name {
			// No need to push updates to self
			continue
		}

		//log.Printf("Pres loadContacts: topic[%s]: caching as '%s'", t.name, topic)
		t.perSubs[topic] = perSubsData{with: with}
	}
	//log.Printf("Pres loadContacts: topic[%s]: total cached %d", t.name, len(t.perSubs))
	return nil
}

// This topic got a request from a 'me' topic to start/stop sending presence updates.
func (t *Topic) presProcReq(fromUserId string, online, wantReply bool) {
	log.Printf("presProcReq: topic[%s]: req from '%s', online: %v, wantReply: %v", t.name,
		fromUserId, online, wantReply)

	doReply := wantReply
	if t.cat == types.TopicCat_Me {
		if psd, ok := t.perSubs[fromUserId]; ok {
			// If requester's online status has not changed, do not reply, otherwise an endless loop will happen.
			// wantReply is needed to ensure unnecessary {pres} is not sent:
			// A[online, B:off] to B[online, A:off]: {pres A on}
			// B[online, A:on] to A[online, B:off]: {pres B on}
			// A[online, B:on] to B[online, A:on]: {pres A on} <<-- unnecessary, that's why wantReply is needed
			doReply = (doReply && (psd.online != online))
			psd.online = online
			t.perSubs[fromUserId] = psd

			log.Printf("presProcReq: topic[%s]: set user %s online to %v", t.name, fromUserId, online)

		} else {
			doReply = false
			//log.Printf("presProcReq: topic[%s]: request from untracked topic %s", t.name, fromTopic)
		}
	}

	if online && doReply {
		globals.hub.route <- &ServerComMessage{
			// Topic is 'me' even for group topics; group topics will use 'me' as a signal to drop the message
			// without forwarding to sessions
			Pres:   &MsgServerPres{Topic: "me", What: "on", Src: t.name},
			rcptto: fromUserId}
	}
}

// Publish user's update to his/her users of interest on their 'me' topic
// Case A: user came online, "on", ua
// Case B: user went offline, "off", ua
// Case C: user agent change, "ua", ua
// Case D: User updated 'public', "upd"
func (t *Topic) presUsersOfInterest(what string, ua string) {
	// Push update to subscriptions
	for topic, _ := range t.perSubs {
		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{
				Topic: "me", What: what, Src: t.name, UserAgent: ua, wantReply: (what == "on")},
			rcptto: topic}

		// log.Printf("Pres A, B, C, D: User'%s' to '%s' what='%s', ua='%s'", t.name, topic, what, ua)
	}
}

// Report change to topic subscribers online, group or p2p
//
// Case I: User joined the topic, "on"
// Case J: User left topic, "off"
// Case K.2: User altered WANT (and maybe got default Given), "acs"
// Case L.1: Admin altered GIVEN, "acs" to affected user
// Case L.3: Admin altered GIVEN (and maybe got assigned default WANT), "acs" to admins
// Case V.2: Messages soft deleted, "del" to one user only
// Case W.2: Messages hard-deleted, "del"
func (t *Topic) presSubsOnline(what, src string, params *PresParams,
	user string, filterPos, filterNeg types.AccessMode, skip string) {

	// If affected user is the same as the user making the change, clear 'who'
	if params.who == src {
		params.who = ""
	}

	globals.hub.route <- &ServerComMessage{
		Pres: &MsgServerPres{Topic: t.x_original, What: what, Src: src,
			Acs: params.packAcs(), Who: params.who,
			SeqId: params.seqId, SeqList: params.seqList,
			filterPos: int(filterPos), filterNeg: int(filterNeg), singleUser: user},
		rcptto: t.name, skipSid: skip}

	// log.Printf("Pres K.2, L.3, W.2: topic'%s' what='%s', who='%s', acs='w:%s/g:%s'", t.name, what,
	// 	params.who, params.dWant, params.dGiven)

}

// Publish to topic subscribers's sessions currently offline in the topic, on their 'me'
// Group and P2P.
// Case E: topic came online, "on"
// Case F: topic went offline, "off"
// Case G: topic updated 'public', "upd", who
// Case H: topic deleted, "gone"
// Case K.3: user altered WANT, "acs" to admins
// Case L.4: Admin altered GIVEN, "acs" to admins
// Case T: message sent, "msg" to all with 'R'
// Case W.1: messages hard-deleted, "del" to all with 'R'
func (t *Topic) presSubsOffline(what string, params *PresParams, filterPos, filterNeg types.AccessMode) {

	for uid, _ := range t.perUser {
		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{Topic: "me", What: what, Src: t.original(uid),
				Acs: params.packAcs(), Who: params.who,
				SeqId: params.seqId, SeqList: params.seqList,
				filterPos: int(filterPos), filterNeg: int(filterNeg), skipTopic: t.name},
			rcptto: uid.UserId()}
	}
	// log.Printf("presSubsOffline: topic'%s' what='%s', who='%s'", t.name, what, params.who)
}

// Announce to a single user on 'me' topic
//
// Case K.1: User altered WANT (includes new subscription, deleted subscription)
// Case L.2: Sharer altered GIVEN (inludes invite, eviction)
// Case U: read/recv notification
// Case V.1: messages soft-deleted
func (t *Topic) presSingleUserOffline(uid types.Uid, what string, params *PresParams, skip string) {
	if _, ok := t.perUser[uid]; ok {
		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{Topic: "me", What: what,
				Src: t.original(uid), SeqId: params.seqId, SeqList: params.seqList,
				Who: params.who, Acs: params.packAcs()},
			rcptto: uid.UserId(), skipSid: skip}
	}

	// log.Printf("Pres J.1, K, M.1, N: topic'%s' what='%s', who='%s'", t.name, what, who.UserId())
}

// Let other sessions of a given user know that what messages are now received/read/deleted
// Cases U, V.1
func (t *Topic) presPubMessageCount(uid types.Uid, list []int, clear, recv, read int, skip string) {
	var what string
	var seq int
	if read > 0 {
		what = "read"
		seq = read
	} else if recv > 0 {
		what = "recv"
		seq = recv
	} else if seq > 0 {
		what = "del"
		seq = clear
	} else if list != nil {
		if len(list) == 1 {
			if list[0] > 0 {
				what = "del"
				seq = list[0]
				list = nil
			}
		} else {
			what = "del"
		}
	}

	if what != "" {
		// Announce to user's other sessions on 'me' regardless of being attached to this topic.
		t.presSingleUserOffline(uid, what, &PresParams{seqId: seq, seqList: list}, skip)
	} else {
		log.Printf("Case U, V1: topic[%s] invalid request - missing payload", t.name)
	}
}
