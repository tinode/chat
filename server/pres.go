package main

import (
	"encoding/json"
	"log"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

type PresParams struct {
	userAgent string
	seqId     int
	seqList   []int

	// Uid who performed the action
	actor string
	// Subject of the action
	target string
	dWant  string
	dGiven string
}

func (p PresParams) packAcs() *MsgAccessMode {
	if p.dWant != "" || p.dGiven != "" {
		return &MsgAccessMode{Want: p.dWant, Given: p.dGiven}
	}
	return nil
}

func (t *Topic) addToPerSubs(topic string, online bool) {
	if uid1, uid2, err := types.ParseP2P(topic); err == nil {
		// If this is a P2P topic, index it by second user's ID
		if uid1.UserId() == t.name {
			topic = uid2.UserId()
		} else {
			topic = uid1.UserId()
		}
	} else if topic == t.name {
		// No need to push updates to self
		return
	}

	t.perSubs[topic] = perSubsData{online: online}
}

// loadContacts initializes topic.perSubs to support presence notifications.
// perSubs contains (a) topics that the user wants to notify of his presence and
// (b) those which want to receive notifications from this user.
func (t *Topic) loadContacts(uid types.Uid) error {
	subs, err := store.Users.GetSubs(uid)
	if err != nil {
		return err
	}

	t.perSubs = make(map[string]perSubsData, len(subs))
	for _, sub := range subs {
		//log.Printf("Pres loadContacts: topic[%s]: processing sub '%s'", t.name, sub.Topic)
		t.addToPerSubs(sub.Topic, false)
	}
	//log.Printf("Pres loadContacts: topic[%s]: total cached %d", t.name, len(t.perSubs))
	return nil
}

// This topic got a request from a 'me' topic to start/stop sending presence updates. The
// originating topic reports its own status as "on", "off", "?unkn", or "?gone".
// 	"on" - requester came online
// 	"off" - requester is offline now
//	"?unkn" - requester wants to initiate online status exchange but it's own status is unknown yet
func (t *Topic) presProcReq(fromUserId string, what string, wantReply bool) {

	var online, unknown bool

	switch what {
	case "on":
		online = true
	case "off":
	case "?unkn":
		unknown = true
	default:
		return
	}

	// log.Printf("presProcReq: topic[%s]: req from '%s', online: %s, wantReply: %v", t.name,
	//	fromUserId, what, wantReply)

	doReply := wantReply
	if t.cat == types.TopicCat_Me {
		if psd, ok := t.perSubs[fromUserId]; ok {
			// If requester's online status has not changed, do not reply, otherwise an endless loop will happen.
			// wantReply is needed to ensure unnecessary {pres} is not sent:
			// A[online, B:off] to B[online, A:off]: {pres A on}
			// B[online, A:on] to A[online, B:off]: {pres B on}
			// A[online, B:on] to B[online, A:on]: {pres A on} <<-- unnecessary, that's why wantReply is needed
			doReply = (doReply && ((psd.online != online) || unknown))
			psd.online = online
			t.perSubs[fromUserId] = psd

			// log.Printf("presProcReq: topic[%s]: set user %s online to %v", t.name, fromUserId, online)

		} else {
			// doReply is unchanged

			// Got request from a new topic. This must be a new subscription. Record it.
			t.addToPerSubs(fromUserId, online)
		}
	}

	if (online || unknown) && doReply {
		globals.hub.route <- &ServerComMessage{
			// Topic is 'me' even for group topics; group topics will use 'me' as a signal to drop the message
			// without forwarding to sessions
			Pres:   &MsgServerPres{Topic: "me", What: "on", Src: t.name, wantReply: unknown},
			rcptto: fromUserId}

		// log.Printf("presProcReq: topic[%s]: replying to %s with own status '%s', wantReply", t.name, fromUserId, "on", unknown)
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
// Case M: Topic unaccessible (cluster failure), "left" to everyone currently online
// Case V.2: Messages soft deleted, "del" to one user only
// Case W.2: Messages hard-deleted, "del"
func (t *Topic) presSubsOnline(what, src string, params *PresParams, filter types.AccessMode, skipSid string) {

	// If affected user is the same as the user making the change, clear 'who'
	actor := params.actor
	target := params.target
	if actor == src {
		actor = ""
	}

	if target == src {
		target = ""
	}

	globals.hub.route <- &ServerComMessage{
		Pres: &MsgServerPres{Topic: t.x_original, What: what, Src: src,
			Acs: params.packAcs(), AcsActor: actor, AcsTarget: target,
			SeqId: params.seqId, SeqList: params.seqList, filter: int(filter)},
		rcptto: t.name, skipSid: skipSid}

	// log.Printf("Pres K.2, L.3, W.2: topic'%s' what='%s', who='%s', acs='w:%s/g:%s'", t.name, what,
	// 	params.who, params.dWant, params.dGiven)

}

// Send presence notification to attached sessions directly, without routing though topic.
func (t *Topic) presSubsOnlineDirect(what string) {
	msg := &ServerComMessage{Pres: &MsgServerPres{Topic: t.x_original, What: what}}

	var packet []byte
	if t.cat != types.TopicCat_P2P {
		packet, _ = json.Marshal(msg)
	}

	for sess := range t.sessions {
		// Check presence filters
		pud, _ := t.perUser[sess.uid]
		if !(pud.modeGiven & pud.modeWant).IsPresencer() {
			continue
		}

		if t.cat == types.TopicCat_P2P {
			// For p2p topics topic name is dependent on receiver
			msg.Pres.Topic = t.original(sess.uid)
			packet, _ = json.Marshal(msg)
		}

		select {
		case sess.send <- packet:
		default:
		}
	}
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
func (t *Topic) presSubsOffline(what string, params *PresParams, filter types.AccessMode,
	skipSid string, offlineOnly bool) {

	var skipTopic string
	if offlineOnly {
		skipTopic = t.name
	}

	for uid, pud := range t.perUser {
		if !presOfflineFilter(pud.modeGiven&pud.modeWant, filter) {
			continue
		}

		user := uid.UserId()
		actor := params.actor
		target := params.target
		if actor == user {
			actor = ""
		}

		if target == user {
			target = ""
		}

		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{Topic: "me", What: what, Src: t.original(uid),
				Acs: params.packAcs(), AcsActor: actor, AcsTarget: target,
				SeqId: params.seqId, SeqList: params.seqList,
				skipTopic: skipTopic},
			rcptto: user, skipSid: skipSid}
	}
	// log.Printf("presSubsOffline: topic'%s' what='%s', who='%s'", t.name, what, params.who)
}

// Same as presSubsOffline, but the topic has not been loaded/initialized first: offline topic, offline subscribers
func presSubsOfflineOffline(topic string, cat types.TopicCat, subs []types.Subscription, what string,
	params *PresParams, skipSid string) {

	var count = 0
	original := topic
	for _, sub := range subs {
		if !presOfflineFilter(sub.ModeWant&sub.ModeGiven, types.ModeNone) {
			continue
		}

		if cat == types.TopicCat_P2P {
			original = types.ParseUid(subs[(count+1)%2].User).UserId()
			count++
		}

		user := types.ParseUid(sub.User).UserId()
		actor := params.actor
		target := params.target
		if actor == user {
			actor = ""
		}

		if target == user {
			target = ""
		}

		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{Topic: "me", What: what, Src: original,
				Acs: params.packAcs(), AcsActor: actor, AcsTarget: target,
				SeqId: params.seqId, SeqList: params.seqList},
			rcptto: user, skipSid: skipSid}
	}
}

// Announce to a single user on 'me' topic
//
// Case K.1: User altered WANT (includes new subscription, deleted subscription)
// Case L.2: Sharer altered GIVEN (inludes invite, eviction)
// Case U: read/recv notification
// Case V.1: messages soft-deleted
func (t *Topic) presSingleUserOffline(uid types.Uid, what string, params *PresParams, skipSid string, offlineOnly bool) {
	var skipTopic string
	if offlineOnly {
		skipTopic = t.name
	}

	if pud, ok := t.perUser[uid]; ok && presOfflineFilter(pud.modeGiven&pud.modeWant, types.ModeNone) {
		user := uid.UserId()
		actor := params.actor
		target := params.target
		if actor == user {
			actor = ""
		}

		if target == user {
			target = ""
		}

		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{Topic: "me", What: what,
				Src: t.original(uid), SeqId: params.seqId, SeqList: params.seqList,
				Acs: params.packAcs(), AcsActor: actor, AcsTarget: target, UserAgent: params.userAgent,
				wantReply: (what == "?unkn"), skipTopic: skipTopic},
			rcptto: user, skipSid: skipSid}
	}

	// log.Printf("Pres J.1, K, M.1, N: topic'%s' what='%s', who='%s'", t.name, what, who.UserId())
}

// Same as above, but the topic is offline (not loaded from the DB)
func presSingleUserOfflineOffline(uid types.Uid, original string, what string,
	mode types.AccessMode, params *PresParams, skipSid string) {

	user := uid.UserId()
	actor := params.actor
	target := params.target
	if actor == user {
		actor = ""
	}

	if target == user {
		target = ""
	}

	globals.hub.route <- &ServerComMessage{
		Pres: &MsgServerPres{Topic: "me", What: what,
			Src: original, SeqId: params.seqId, SeqList: params.seqList,
			Acs: params.packAcs(), AcsActor: actor, AcsTarget: target},
		rcptto: uid.UserId(), skipSid: skipSid}
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
		// Announce to user's other sessions on 'me' only if they are not attached to this topic.
		// Attached topics will receive an {info}
		t.presSingleUserOffline(uid, what, &PresParams{seqId: seq, seqList: list}, skip, true)
	} else {
		log.Printf("Case U, V1: topic[%s] invalid request - missing payload", t.name)
	}
}

// Must apply filter here. When sending offline to 'me' topic, 'me' does not have access to
// original topic's access parameters
func presOfflineFilter(mode, filter types.AccessMode) bool {
	return mode.IsPresencer() &&
		(filter == types.ModeNone || mode&filter != 0)
}
