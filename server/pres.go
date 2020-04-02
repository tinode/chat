package main

import (
	"log"
	"strings"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// presParams defines parameters for creating a presence notification.
type presParams struct {
	userAgent string
	seqID     int
	delID     int
	delSeq    []MsgDelRange

	// Uid who performed the action
	actor string
	// Subject of the action
	target string
	dWant  string
	dGiven string
}

type presFilters struct {
	// Send messages only to users with this access mode being non-zero.
	filterIn types.AccessMode
	// Exclude users with this access mode being non-zero.
	filterOut types.AccessMode
	// Send messages to the sessions of this single user defined by ID as a string 'usrABC'.
	singleUser string
	// Do not send messages to sessions of this user defined by ID as a string 'usrABC'.
	excludeUser string
}

func (p *presParams) packAcs() *MsgAccessMode {
	if p.dWant != "" || p.dGiven != "" {
		return &MsgAccessMode{Want: p.dWant, Given: p.dGiven}
	}
	return nil
}

// Presence: Add another user to the list of contacts to notify of presence and other changes
func (t *Topic) addToPerSubs(topic string, online, enabled bool) {
	if topic == t.name {
		// No need to push updates to self
		return
	}

	if uid1, uid2, err := types.ParseP2P(topic); err == nil {
		// If this is a P2P topic, index it by second user's ID
		if uid1.UserId() == t.name {
			topic = uid2.UserId()
		} else {
			topic = uid1.UserId()
		}
	}

	t.perSubs[topic] = perSubsData{online: online, enabled: enabled}
}

// loadContacts loads topic.perSubs to support presence notifications.
// perSubs contains (a) topics that the user wants to notify of his presence and
// (b) those which want to receive notifications from this user.
func (t *Topic) loadContacts(uid types.Uid) error {
	subs, err := store.Users.GetSubs(uid, nil)
	if err != nil {
		return err
	}

	for i := range subs {
		t.addToPerSubs(subs[i].Topic, false, (subs[i].ModeGiven & subs[i].ModeWant).IsPresencer())
	}
	return nil
}

// This topic got a request from a 'me' topic to start/stop sending presence updates.
// The originating topic reports its own status in 'what' as "on", "off", "gone" or "?unkn".
// 	"on" - requester came online
// 	"off" - requester is offline now
//  "?none" - anchor for "+" command: requester status is unknown, won't generate a response and isn't forwarded to clients.
//  "gone" - topic deleted or otherwise gone - equivalent of "off+remove"
//	"?unkn" - requester wants to initiate online status exchange but it's own status is unknown yet. This
//  notifications is not forwarded to users.
//
// "+" commands:
// "+en": enable subscription, i.e. start accepting incoming notifications from the user2;
// "+rem": terminate and remove the subscription (subscription deleted)
// "+dis" disable subscription withot removing it, the opposite of "en".
// The "+en/rem/dis" command itself is stripped from the notification.
func (t *Topic) presProcReq(fromUserID, what string, wantReply bool) string {
	if t.isInactive() {
		return ""
	}

	var reqReply, onlineUpdate bool

	online := &onlineUpdate
	replyAs := "on"

	parts := strings.Split(what, "+")
	what = parts[0]
	cmd := ""
	if len(parts) > 1 {
		cmd = parts[1]
	}

	switch what {
	case "on":
		// online
		*online = true
	case "off":
		// offline
	case "?none":
		// no change to online status
		online = nil
		what = ""
	case "gone":
		// offline: off+rem
		cmd = "rem"
	case "?unkn":
		// no change in online status
		online = nil
		reqReply = true
		what = ""
	default:
		// All other notifications are not processed here
		return what
	}

	if t.cat == types.TopicCatMe {

		// Find if the contact is listed.
		if psd, ok := t.perSubs[fromUserID]; ok {

			if cmd == "rem" {
				replyAs = "off+rem"
				if !psd.enabled && what == "off" {
					// If it was disabled before, don't send a redundant update.
					what = ""
				}
				delete(t.perSubs, fromUserID)

			} else {

				switch cmd {
				case "":
					// No change in being enabled or disabled and not being added or removed.
					if !psd.enabled || online == nil || psd.online == *online {
						// Not enabled or no change in online status - remove unnecessary notification.
						what = ""
					}
				case "en":
					if !psd.enabled {
						psd.enabled = true
					} else if online == nil || psd.online == *online {
						// Was active and no change or online before: skip unnecessary update.
						what = ""
					}
				case "dis":
					if psd.enabled {
						psd.enabled = false
						if !psd.online {
							what = ""
						}
					} else {
						// Was disabled and consequently offline before, still offline - skip the update.
						what = ""
					}
				default:
					panic("presProcReq: unknown command '" + cmd + "'")
				}

				if !psd.enabled {
					// If we don't care about updates, keep the other user off
					psd.online = false
				} else if online != nil {
					psd.online = *online
				}

				t.perSubs[fromUserID] = psd
			}

		} else if cmd != "rem" {
			// Got request from a new topic. This must be a new subscription. Record it.
			// If it's unknown, recording it as offline.
			t.addToPerSubs(fromUserID, onlineUpdate, cmd == "en")

			if cmd != "en" {
				// If the connection is not enabled, ignore the update.
				what = ""
			}

		} else {
			// Not in list and asked to be removed from the list - ignore
			what = ""
		}
	}

	// log.Println("out-what", what, "from:", fromUserID, "to:", t.name, "reply:", replyAs)

	// If requester's online status has not changed, do not reply, otherwise an endless loop will happen.
	// wantReply is needed to ensure unnecessary {pres} is not sent:
	// A[online, B:off] to B[online, A:off]: {pres A on}
	// B[online, A:on] to A[online, B:off]: {pres B on}
	// A[online, B:on] to B[online, A:on]: {pres A on} <<-- unnecessary, that's why wantReply is needed
	if (onlineUpdate || reqReply) && wantReply {
		globals.hub.route <- &ServerComMessage{
			// Topic is 'me' even for group topics; group topics will use 'me' as a signal to drop the message
			// without forwarding to sessions
			Pres:   &MsgServerPres{Topic: "me", What: replyAs, Src: t.name, WantReply: reqReply},
			rcptto: fromUserID}
	}

	return what
}

// Publish user's update to his/her users of interest on their 'me' topic
// Case A: user came online, "on", ua
// Case B: user went offline, "off", ua
// Case C: user agent change, "ua", ua
// Case D: User updated 'public', "upd"
func (t *Topic) presUsersOfInterest(what, ua string) {
	parts := strings.Split(what, "+")
	wantReply := parts[0] == "on"
	goOffline := len(parts) > 1 && parts[1] == "dis"

	// Push update to subscriptions
	for topic, psd := range t.perSubs {
		globals.hub.route <- &ServerComMessage{
			Pres: &MsgServerPres{
				Topic:     "me",
				What:      what,
				Src:       t.name,
				UserAgent: ua,
				WantReply: wantReply},
			rcptto: topic}

		if psd.online && goOffline {
			psd.online = false
			t.perSubs[topic] = psd
		}
	}
}

// Publish user's update to his/her users of interest on their 'me' topic while user's 'me' topic is offline
// Case A: user is being deleted, "gone"
func presUsersOfInterestOffline(uid types.Uid, subs []types.Subscription, what string) {
	// Push update to subscriptions
	for _, sub := range subs {
		globals.hub.route <- &ServerComMessage{
			Pres:   &MsgServerPres{Topic: "me", What: what, Src: uid.UserId(), WantReply: false},
			rcptto: sub.Topic}
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
func (t *Topic) presSubsOnline(what, src string, params *presParams,
	pf *presFilters, skipSid string) {

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
		Pres: &MsgServerPres{Topic: t.xoriginal, What: what, Src: src,
			Acs: params.packAcs(), AcsActor: actor, AcsTarget: target,
			SeqId: params.seqID, DelId: params.delID, DelSeq: params.delSeq,
			FilterIn: int(pf.filterIn), FilterOut: int(pf.filterOut),
			SingleUser: pf.singleUser, ExcludeUser: pf.excludeUser},
		rcptto: t.name, skipSid: skipSid}
}

// Send presence notification to attached sessions directly, without routing though topic.
func (t *Topic) presSubsOnlineDirect(what string) {
	msg := &ServerComMessage{Pres: &MsgServerPres{Topic: t.xoriginal, What: what}}

	for sess := range t.sessions {
		// Check presence filters
		pud := t.perUser[sess.uid]
		if pud.deleted || (!(pud.modeGiven & pud.modeWant).IsPresencer() && what != "gone" && what != "acs") {
			continue
		}

		if t.cat == types.TopicCatP2P {
			// For p2p topics topic name is dependent on receiver.
			// It's OK to change the pointer here because the message will be serialized in queueOut
			// before being placed into channel.
			msg.Pres.Topic = t.original(sess.uid)
		}
		sess.queueOut(msg)
	}
}

// Communicates "topic unaccessible (cluster rehashing or node connection lost)" event
// to a list of topics promting the client to resubscribe to the topics.
func (s *Session) presTermDirect(subs []string) {
	log.Printf("sid '%s', uid '%s', terminating %s", s.sid, s.uid, subs)
	msg := &ServerComMessage{Pres: &MsgServerPres{Topic: "me", What: "term"}}
	for _, topic := range subs {
		msg.Pres.Src = topic
		s.queueOut(msg)
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
func (t *Topic) presSubsOffline(what string, params *presParams, filter *presFilters,
	skipSid string, offlineOnly bool) {

	var skipTopic string
	if offlineOnly {
		skipTopic = t.name
	}

	for uid := range t.perUser {
		if t.perUser[uid].deleted || (what != "acs" && what != "gone" &&
			!presOfflineFilter(t.perUser[uid].modeGiven&t.perUser[uid].modeWant, filter)) {
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
				SeqId: params.seqID, DelId: params.delID,
				SkipTopic: skipTopic},
			rcptto: user, skipSid: skipSid}
	}
}

// Same as presSubsOffline, but the topic has not been loaded/initialized first: offline topic, offline subscribers
func presSubsOfflineOffline(topic string, cat types.TopicCat, subs []types.Subscription, what string,
	params *presParams, skipSid string) {

	var count = 0
	original := topic
	for i := range subs {
		sub := &subs[i]
		// Let "acs" and "gone" through regardless of 'P'. Don't check for deleted subscriptions:
		// they are not passed here.
		if what != "acs" && what != "gone" && !presOfflineFilter(sub.ModeWant&sub.ModeGiven, nil) {
			continue
		}

		if cat == types.TopicCatP2P {
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
				SeqId: params.seqID, DelId: params.delID},
			rcptto: user, skipSid: skipSid}
	}
}

// Announce to a single user on 'me' topic
//
// Case K.1: User altered WANT (includes new subscription, deleted subscription)
// Case L.2: Sharer altered GIVEN (inludes invite, eviction)
// Case U: read/recv notification
// Case V.1: messages soft-deleted
func (t *Topic) presSingleUserOffline(uid types.Uid, what string, params *presParams, skipSid string, offlineOnly bool) {
	var skipTopic string
	if offlineOnly {
		skipTopic = t.name
	}

	if pud, ok := t.perUser[uid]; ok && !pud.deleted &&
		// Send access change notification regardless of P permission.
		(what == "acs" || what == "gone" || presOfflineFilter(pud.modeGiven&pud.modeWant, nil)) {

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
				Src: t.original(uid), SeqId: params.seqID, DelId: params.delID,
				Acs: params.packAcs(), AcsActor: actor, AcsTarget: target, UserAgent: params.userAgent,
				WantReply: strings.HasPrefix(what, "?unkn"), SkipTopic: skipTopic},
			rcptto: user, skipSid: skipSid}
	}
}

// Announce to a single user on 'me' topic. The originating topic is not used (not loaded or user
// already unsubscribed).
func presSingleUserOfflineOffline(uid types.Uid, original, what string, params *presParams, skipSid string) {

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
			Src: original, SeqId: params.seqID, DelId: params.delID,
			Acs: params.packAcs(), AcsActor: actor, AcsTarget: target},
		rcptto: uid.UserId(), skipSid: skipSid}
}

// Let other sessions of a given user know what messages are now received/read
// Cases U
func (t *Topic) presPubMessageCount(uid types.Uid, recv, read int, skip string) {
	var what string
	var seq int
	if read > 0 {
		what = "read"
		seq = read
	} else if recv > 0 {
		what = "recv"
		seq = recv
	}

	if what != "" {
		// Announce to user's other sessions on 'me' only if they are not attached to this topic.
		// Attached topics will receive an {info}

		t.presSingleUserOffline(uid, what, &presParams{seqID: seq}, skip, true)
	}
}

// Let other sessions of a given user know that messages are now deleted
// Cases V.1, V.2
func (t *Topic) presPubMessageDelete(uid types.Uid, delID int, list []MsgDelRange, skip string) {
	if len(list) == 0 && delID <= 0 {
		log.Printf("Case V.1, V.2: topic[%s] invalid request - missing payload", t.name)
		return
	}

	// This check is only needed for V.1, but it does not hurt V.2. Let's do it here for both.
	pud := t.perUser[uid]
	if !(pud.modeGiven & pud.modeWant).IsPresencer() || pud.deleted {
		return
	}

	params := &presParams{delID: delID, delSeq: list}

	// Case V.2
	user := uid.UserId()
	t.presSubsOnline("del", user, params, &presFilters{singleUser: user}, skip)

	// Case V.1
	t.presSingleUserOffline(uid, "del", params, skip, true)
}

// Filter by permissions: mode.IsPresencer() AND mode has at least some
// bits specified in 'filter' (or filter is ModeNone).
//
// Filtering must happen here because receiving 'me' has no access to sender's permissions.
func presOfflineFilter(mode types.AccessMode, pf *presFilters) bool {
	return mode.IsPresencer() &&
		(pf == nil ||
			((pf.filterIn == types.ModeNone || mode&pf.filterIn != 0) &&
				(pf.filterOut == types.ModeNone || mode&pf.filterOut == 0)))
}
