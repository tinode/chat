/******************************************************************************
 *
 *  Description :
 *    An isolated communication channel (chat room, 1:1 conversation) for
 *    usually multiple users. There is no communication across topics.
 *
 *****************************************************************************/

package main

import (
	"container/list"
	"errors"
	"log"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Time between subscription of a background session and when the notifications are sent.
// If session unsubscribes in this time frame notifications are not sent at all.
const deferredNotificationsTimeout = time.Second * 5

// Topic is an isolated communication channel
type Topic struct {
	// Еxpanded/unique name of the topic.
	name string
	// For single-user topics session-specific topic name, such as 'me',
	// otherwise the same as 'name'.
	xoriginal string

	// Topic category
	cat types.TopicCat

	// TODO(gene): currently unused
	// If isProxy == true, the actual topic is hosted by another cluster member.
	// The topic should:
	// 1. forward all messages to master
	// 2. route replies from the master to sessions.
	// 3. disconnect sessions at master's request.
	// 4. shut down the topic at master's request.
	// 5. aggregate access permissions on behalf of attached sessions.
	isProxy bool
	// Name of the master node for this topic if isProxy is true.
	masterNode string

	// Time when the topic was first created.
	created time.Time
	// Time when the topic was last updated.
	updated time.Time
	// Time of the last outgoing message.
	touched time.Time

	// Server-side ID of the last data message
	lastID int
	// ID of the deletion operation. Not an ID of the message.
	delID int

	// Last published userAgent ('me' topic only)
	userAgent string

	// User ID of the topic owner/creator. Could be zero.
	owner types.Uid

	// Default access mode
	accessAuth types.AccessMode
	accessAnon types.AccessMode

	// Topic discovery tags
	tags []string

	// Topic's public data
	public interface{}

	// Topic's per-subscriber data
	perUser map[types.Uid]perUserData
	// Union of permissions across all users (used by proxy sessions with uid = 0).
	// These are used by master topics only (in the proxy-master topic context)
	// as a coarse-grained attempt to perform acs checks since proxy sessions "impersonate"
	// multiple normal sessions (uids) which may have different uids.
	modeWantUnion  types.AccessMode
	modeGivenUnion types.AccessMode

	// User's contact list (not nil for 'me' topic only).
	// The map keys are UserIds for P2P topics and grpXXX for group topics.
	perSubs map[string]perSubsData

	// Sessions attached to this topic. The UID kept here may not match Session.uid if session is
	// subscribed on behalf of another user.
	sessions map[*Session]perSessionData
	// Queue of delayed presence updates from successful service (background) subscriptions.
	defrNotif *list.List

	// Inbound {data} and {pres} messages from sessions or other topics, already converted to SCM. Buffered = 256
	broadcast chan *ServerComMessage
	// Channel for receiving {get}/{set} requests, buffered = 32
	meta chan *metaReq
	// Subscribe requests from sessions, buffered = 32
	reg chan *sessionJoin
	// Unsubscribe requests from sessions, buffered = 32
	unreg chan *sessionLeave
	// Track the most active sessions to report User Agent changes. Buffered = 32
	uaChange chan string
	// Channel to terminate topic  -- either the topic is deleted or system is being shut down. Buffered = 1.
	exit chan *shutDown
	// Channel to receive topic master responses (used only by proxy topics).
	proxy chan *ClusterResp

	// Flag which tells topic lifecycle status: new, ready, paused, marked for deletion.
	status int32
}

// perUserData holds topic's cache of per-subscriber data
type perUserData struct {
	// Timestamps when the subscription was created and updated
	created time.Time
	updated time.Time

	// Count of subscription online and announced (presence not deferred).
	online int

	// Last t.lastId reported by user through {pres} as received or read
	recvID int
	readID int
	// ID of the latest Delete operation
	delID int

	private interface{}

	modeWant  types.AccessMode
	modeGiven types.AccessMode

	// P2P only:
	public    interface{}
	topicName string
	deleted   bool
}

// perSubsData holds user's (on 'me' topic) cache of subscription data
type perSubsData struct {
	// The other user's/topic's online status as seen by this user.
	online bool
	// True if we care about the updates from the other user/topic: (want&given).IsPresencer().
	// Does not affect sending notifications from this user to other users.
	enabled bool
}

// Data related to a subscription of a session to a topic.
type perSessionData struct {
	// ID of the subscribed user (asUid); not necessarily the session owner.
	uid types.Uid
	// Reference to a list bucket with deferred notification or nil if no notifications are deferred.
	ref *list.Element
}

// Reasons why topic is being shut down.
const (
	// StopNone no reason given/default.
	StopNone = iota
	// StopShutdown terminated due to system shutdown.
	StopShutdown
	// StopDeleted terminated due to being deleted.
	StopDeleted
	// StopRehashing terminated due to cluster rehashing (moved to a different node).
	StopRehashing
)

// Topic shutdown
type shutDown struct {
	// Channel to report back completion of topic shutdown. Could be nil
	done chan<- bool
	// Topic is being deleted as opposite to total system shutdown
	reason int
}

var nilPresParams = &presParams{}
var nilPresFilters = &presFilters{}

func (t *Topic) run(hub *Hub) {
	if !t.isProxy {
		t.runLocal(hub)
	} else {
		t.runProxy(hub)
	}
}

func (t *Topic) runProxy(hub *Hub) {
	// Kills topic after a period of inactivity.
	keepAlive := idleTopicTimeout
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	// Ticker for deferred presence notifications.
	defrNotifTimer := time.NewTimer(time.Millisecond * 500)

	for {
		select {
		case sreg := <-t.reg:
			// Request to add a connection to this topic
			if t.isInactive() {
				asUid := types.ParseUserId(sreg.pkt.asUser)
				sreg.sess.queueOut(ErrLocked(sreg.pkt.id, t.original(asUid), types.TimeNow()))
			} else {
				log.Printf("topic[%s] reg %+v", t.name, sreg)
				msg := &ProxyTopicData{
					JoinReq: &ProxyJoin{
						Created:  sreg.created,
						Newsub:   sreg.newsub,
						Internal: sreg.internal,
					},
				}
				log.Println("sessionJoin pkt = ", sreg.pkt, sreg.topic)
				// Response (ctrl message) will be handled when it's received via the proxy channel.
				if err := globals.cluster.routeToTopicMaster(sreg.pkt, nil, msg, t.name, sreg.sess); err != nil {
					log.Println("proxy topic: route join request from proxy to master failed:", err)
				}
			}

		case leave := <-t.unreg:
			// Remove connection from topic; session may continue to function
			log.Printf("t[%s] leave %+v", t.name, leave)
			asUid := leave.userId
			// Explicitly specify user id because the proxy session hosts multiple client sessions.
			if asUid.IsZero() {
				if pssd, ok := t.sessions[leave.sess]; ok {
					asUid = pssd.uid
				} else {
					log.Println("proxy topic: leave request sent for unknown session")
					continue
				}
			}
			// Remove the session from the topic without waiting for a response from the master node
			// because by the time the response arrives this session may be already gone from the session store
			// and we won't be able to find and remove it by its sid.
			t.remSession(leave.sess, asUid)
			msg := &ClientComMessage{}
			proxyLeave := &ProxyTopicData{
				LeaveReq: &ProxyLeave{
					Id:     leave.id,
					UserId: asUid,
					Unsub:  leave.unsub,
					// Terminate connection to master topic if explicitly asked to do so or all sessions are gone.
					TerminateProxyConnection: leave.terminateProxyConnection || len(t.sessions) == 0,
				},
			}
			if err := globals.cluster.routeToTopicMaster(msg, nil, proxyLeave, t.name, leave.sess); err != nil {
				log.Println("proxy topic: route broadcast request from proxy to master failed:", err)
			}

		case msg := <-t.broadcast:
			// Content message intended for broadcasting to recipients
			brdc := &ProxyTopicData{
				BroadcastReq: &ProxyBroadcast{
					Id:        msg.id,
					From:      msg.asUser,
					Timestamp: msg.timestamp,
					SkipSid:   msg.skipSid,
				},
			}
			log.Printf("t[%s] broadcast %+v %+v", t.name, msg, brdc.BroadcastReq)
			if err := globals.cluster.routeToTopicMaster(nil, msg, brdc, t.name, msg.sess); err != nil {
				log.Println("proxy topic: route broadcast request from proxy to master failed:", err)
			}

		case meta := <-t.meta:
			// Request to get/set topic metadata
			log.Printf("t[%s] meta %+v", t.name, meta)
			req := &ProxyTopicData{
				MetaReq: &ProxyMeta{
					What: meta.what,
				},
			}
			if err := globals.cluster.routeToTopicMaster(meta.pkt, nil, req, t.name, meta.sess); err != nil {
				log.Println("proxy topic: route meta request from proxy to master failed:", err)
			}

		case ua := <-t.uaChange:
			// Process an update to user agent from one of the sessions
			log.Printf("t[%s] uaChange %+v", t.name, ua)
			req := &ProxyTopicData{
				UAChangeReq: &ProxyUAChange{
					UserAgent: ua,
				},
			}
			if err := globals.cluster.routeToTopicMaster(nil, nil, req, t.name, nil); err != nil {
				log.Println("proxy topic: route ua change request from proxy to master failed:", err)
			}

		case msg := <-t.proxy:
			log.Printf("proxy topic [%s] msg: sid[%s] = %+v | proxyresp = %+v", t.name, msg.FromSID, msg.SrvMsg, msg.ProxyResp)

			if msg.SrvMsg.Pres != nil && msg.SrvMsg.Pres.What == "acs" && msg.SrvMsg.Pres.Acs != nil {
				// If the server changed acs on this topic, update the internal state.
				t.updateAcsFromPresMsg(msg.SrvMsg.Pres)
			}
			if msg.FromSID == "*" {
				// It is a broadcast.
				msg.SrvMsg.skipSid = msg.ProxyResp.SkipSid
				msg.SrvMsg.uid = msg.ProxyResp.Uid
				switch {
				case msg.SrvMsg.Pres != nil || msg.SrvMsg.Data != nil || msg.SrvMsg.Info != nil:
					// Regular broadcast.
					t.proxyFanoutBroadcast(msg.SrvMsg)
				case msg.SrvMsg.Ctrl != nil:
					// Ctrl broadcast. E.g. for user eviction.
					t.handleCtrlBroadcast(msg.SrvMsg)
				default:
				}
			} else {
				sess := globals.sessionStore.Get(msg.FromSID)
				switch msg.ProxyResp.OrigRequestType {
				case ProxyRequestJoin:
					if sess != nil && msg.SrvMsg != nil && msg.SrvMsg.Ctrl != nil {
						if msg.SrvMsg.Ctrl.Code < 300 {
							if t.addSession(sess, msg.ProxyResp.Uid) {
								sess.addSub(t.name, &Subscription{
									broadcast: t.broadcast,
									done:      t.unreg,
									meta:      t.meta,
									uaChange:  t.uaChange})
								if msg.ProxyResp.IsBackground {
									// It's a background session.
									// Make a fake sessionJoin packet and add it to deferred notification list.
									// We only need a timestamp and a pointer to the session
									// in order for deferred notification processing to work correctly.
									sreg := &sessionJoin{
										sess: sess,
										pkt: &ClientComMessage{
											asUser:    msg.ProxyResp.Uid.UserId(),
											timestamp: types.TimeNow(),
										},
									}
									pssd, _ := t.sessions[sess]
									pssd.ref = t.defrNotif.PushFront(sreg)
									t.sessions[sreg.sess] = pssd
								}
							}
							killTimer.Stop()
						} else {
							if len(t.sessions) == 0 {
								killTimer.Reset(keepAlive)
							}
						}
					}
				case ProxyRequestBroadcast:
				case ProxyRequestMeta:
				case ProxyRequestLeave:
					log.Printf("proxy topic [%s]: session %p unsubscribed", t.name, sess)
					if msg.SrvMsg != nil && msg.SrvMsg.Ctrl != nil {
						log.Printf("proxy topic [%s]: ctrl msg = %+v", t.name, msg.SrvMsg.Ctrl)
						if msg.SrvMsg.Ctrl.Code < 300 {
							if sess != nil {
								t.remSession(sess, sess.uid)
							}
							// All sessions are gone. Start the kill timer.
							if len(t.sessions) == 0 {
								killTimer.Reset(keepAlive)
							}
						}
					}
				default:
					log.Printf("proxy topic [%s] received response referencing unknown request type %d", t.name, msg.ProxyResp.OrigRequestType)
				}
				if !sess.queueOut(msg.SrvMsg) {
					log.Println("topic proxy: timeout")
				}
			}

		case sd := <-t.exit:
			log.Printf("t[%s] exit %+v", t.name, sd)
			// Tell sessions to remove the topic
			for s := range t.sessions {
				s.detach <- t.name
			}
			if t.isProxy {
				if err := globals.cluster.topicProxyGone(t.name); err != nil {
					log.Printf("topic proxy shutdown [%s]: failed to notify master - %s", t.name, err)
				}
			}
			// Report completion back to sender, if 'done' is not nil.
			if sd.done != nil {
				sd.done <- true
			}
			return

		case <-killTimer.C:
			// Topic timeout
			hub.unreg <- &topicUnreg{topic: t.name}

		case <-defrNotifTimer.C:
			t.onDeferredNotificationTimer()
		}
	}
}

// proxyFanoutBroadcast broadcasts msg to all sessions attached to this topic.
func (t *Topic) proxyFanoutBroadcast(msg *ServerComMessage) {
	for sess, pssd := range t.sessions {
		if sess.sid == msg.skipSid {
			continue
		}
		if msg.Pres != nil {
			if !t.passesPresenceFilters(msg, pssd.uid) {
				continue
			}
		} else if msg.Data != nil {
			if !t.userIsReader(pssd.uid) {
				continue
			}
		}
		t.maybeFixTopicName(msg, pssd.uid)
		log.Printf("broadcast fanout [%s] to %s", t.name, sess.sid)
		if !sess.queueOut(msg) {
			log.Printf("topic[%s]: connection stuck, detaching", t.name)
			t.unreg <- &sessionLeave{sess: sess}
		}
	}
}

// handleCtrlBroadcast broadcasts a ctrl command to certain sessions attached to this topic.
func (t *Topic) handleCtrlBroadcast(msg *ServerComMessage) {
	if msg.Ctrl.Code == http.StatusResetContent && msg.Ctrl.Text == "evicted" {
		// We received a ctrl command for evicting a user.
		if msg.uid.IsZero() {
			log.Panicf("topic[%s]: proxy received evict message with empty uid", t.name)
		}
		for sess := range t.sessions {
			if t.remSession(sess, msg.uid) != nil {
				sess.detach <- t.name
				if sess.sid != msg.skipSid {
					sess.queueOut(msg)
				}
			}
		}
	}
}

// updateAcsFromPresMsg modifies user acs in Topic's perUser struct based on the data in `pres`.
func (t *Topic) updateAcsFromPresMsg(pres *MsgServerPres) {
	uid := types.ParseUserId(pres.Src)
	dacs := pres.Acs
	if uid.IsZero() {
		log.Printf("proxy topic[%s]: received acs change for invalid user id '%s'", t.name, pres.Src)
		return
	}

	// If t.perUser[uid] does not exist, pud is initialized with blanks, otherwise it gets existing values.
	pud := t.perUser[uid]
	if err := pud.modeWant.ApplyMutation(dacs.Want); err != nil {
		log.Printf("proxy topic[%s]: could not process acs change - want: %+v", t.name, err)
		return
	}
	if err := pud.modeGiven.ApplyMutation(dacs.Given); err != nil {
		log.Printf("proxy topic[%s]: could not process acs change - given: %+v", t.name, err)
		return
	}
	// Update existing or add new.
	t.perUser[uid] = pud
}

// getPerUserAcs returns `want` and `given` permissions for the given user id.
func (t *Topic) getPerUserAcs(uid types.Uid) (types.AccessMode, types.AccessMode) {
	if uid.IsZero() {
		// For zero uids (typically for proxy sessions), return the union of all permissions.
		return t.modeWantUnion, t.modeGivenUnion
	} else {
		pud := t.perUser[uid]
		return pud.modeWant, pud.modeGiven
	}
}

// passesPresenceFilters applies presence filters to `msg`
// depending on per-user want and given acls for the provided `uid`.
func (t *Topic) passesPresenceFilters(msg *ServerComMessage, uid types.Uid) bool {
	modeWant, modeGiven := t.getPerUserAcs(uid)
	// "gone" and "acs" notifications are sent even if the topic is muted.
	return ((modeGiven & modeWant).IsPresencer() || msg.Pres.What == "gone" || msg.Pres.What == "acs") &&
		(msg.Pres.FilterIn == 0 || int(modeGiven&modeWant)&msg.Pres.FilterIn != 0) &&
		(msg.Pres.FilterOut == 0 || int(modeGiven&modeWant)&msg.Pres.FilterOut == 0)
}

// userIsReader returns true if the user (specified by `uid`) may read the given topic.
func (t *Topic) userIsReader(uid types.Uid) bool {
	modeWant, modeGiven := t.getPerUserAcs(uid)
	return (modeGiven & modeWant).IsReader()
}

// maybeFixTopicName sets the topic field in `msg` depending on the uid.
func (t *Topic) maybeFixTopicName(msg *ServerComMessage, uid types.Uid) {
	if !uid.IsZero() && t.cat == types.TopicCatP2P {
		// For p2p topics topic name is dependent on receiver.
		// For zero uids we don't know the proper topic name, though.
		switch {
		case msg.Data != nil:
			msg.Data.Topic = t.original(uid)
		case msg.Pres != nil:
			msg.Pres.Topic = t.original(uid)
		case msg.Info != nil:
			msg.Info.Topic = t.original(uid)
		}
	}
}

// computePerUserAcsUnion computes want and given permissions unions over all topic's subscribers.
func (t *Topic) computePerUserAcsUnion() {
	wantUnion := types.ModeNone
	givenUnion := types.ModeNone
	for _, pud := range t.perUser {
		wantUnion = wantUnion | pud.modeWant
		givenUnion = givenUnion | pud.modeGiven
	}
	t.modeWantUnion = wantUnion
	t.modeGivenUnion = givenUnion
}

func (t *Topic) runLocal(hub *Hub) {
	// Kills topic after a period of inactivity.
	keepAlive := idleTopicTimeout
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	// Notifies about user agent change. 'me' only
	uaTimer := time.NewTimer(time.Minute)
	var currentUA string
	uaTimer.Stop()

	// Ticker for deferred presence notifications.
	defrNotifTimer := time.NewTimer(time.Millisecond * 500)

	for {
		select {
		case sreg := <-t.reg:
			// Request to add a connection to this topic

			if t.isInactive() {
				asUid := types.ParseUserId(sreg.pkt.asUser)
				sreg.sess.queueOutWithOverrides(
					ErrLocked(sreg.pkt.id, t.original(asUid), types.TimeNow()), sreg.sessOverrides)
			} else {
				// The topic is alive, so stop the kill timer, if it's ticking. We don't want the topic to die
				// while processing the call
				killTimer.Stop()
				if err := t.handleSubscription(hub, sreg); err == nil {
					if sreg.created {
						// Call plugins with the new topic
						pluginTopic(t, plgActCreate)
					}
				} else {
					if len(t.sessions) == 0 && t.cat != types.TopicCatSys {
						// Failed to subscribe, the topic is still inactive
						killTimer.Reset(keepAlive)
					}
					log.Printf("topic[%s] subscription failed %v, sid=%s", t.name, err, sreg.sess.sid)
				}
			}
		case leave := <-t.unreg:
			// Remove connection from topic; session may continue to function
			now := types.TimeNow()

			// userId.IsZero() == true when the entire session is being dropped.
			asUid := leave.userId

			if t.isInactive() {
				if !asUid.IsZero() && leave.id != "" {
					leave.sess.queueOutWithOverrides(ErrLocked(leave.id, t.original(asUid), now), leave.sessOverrides)
				}
				continue

			} else if leave.unsub {
				// User wants to leave and unsubscribe.
				// asUid must not be Zero.
				if err := t.replyLeaveUnsub(hub, leave.sess, asUid, leave.id, leave.sessOverrides); err != nil {
					log.Println("failed to unsub", err, leave.sess.sid)
					continue
				}

			} else if pssd := t.maybeRemoveSession(leave.sess, asUid /*doRemove=*/, !leave.sess.isProxy() || leave.terminateProxyConnection); pssd != nil || leave.sess.isProxy() {
				// Just leaving the topic without unsubscribing if user is subscribed.

				var uid types.Uid
				if leave.sess.isProxy() {
					uid = asUid
				} else {
					uid = pssd.uid
				}
				// uid may be zero when a proxy session is trying to terminate (it called unsubAll).
				proxyTerminating := uid.IsZero()
				var pud perUserData
				if !proxyTerminating {
					pud = t.perUser[uid]
					if pssd == nil || pssd.ref == nil {
						pud.online--
					}
				} else if !leave.sess.isProxy() {
					log.Panic("cannot determine uid: leave req = ", leave)
				}

				switch t.cat {
				case types.TopicCatMe:
					mrs := t.mostRecentSession()
					if mrs == nil {
						// Last session
						mrs = leave.sess
					} else {
						// Change UA to the most recent live session and announce it. Don't block.
						select {
						case t.uaChange <- mrs.userAgent:
						default:
						}
					}
					// Update user's last online timestamp & user agent
					if !proxyTerminating {
						if err := store.Users.UpdateLastSeen(uid, mrs.userAgent, now); err != nil {
							log.Println(err)
						}
					}
				case types.TopicCatFnd:
					// Remove ephemeral query.
					t.fndRemovePublic(leave.sess)
				case types.TopicCatGrp:
					if !proxyTerminating && pud.online == 0 {
						// User is going offline: notify online subscribers on 'me'
						t.presSubsOnline("off", pssd.uid.UserId(), nilPresParams,
							&presFilters{filterIn: types.ModeRead}, "")
					}
				}

				if !proxyTerminating {
					t.perUser[uid] = pud
				}

				// Respond if either the request contains an id
				// or a proxy session is responding to a client request without an id (!proxyTerminating).
				if leave.id != "" || (leave.sess.isProxy() && !proxyTerminating) {
					leave.sess.queueOutWithOverrides(NoErr(leave.id, t.original(asUid), now), leave.sessOverrides)
				}
			}

			// If there are no more subscriptions to this topic, start a kill timer
			if len(t.sessions) == 0 && t.cat != types.TopicCatSys {
				killTimer.Reset(keepAlive)
			}

		case msg := <-t.broadcast:
			// Content message intended for broadcasting to recipients

			var pushRcpt *push.Receipt
			asUid := types.ParseUserId(msg.asUser)
			if msg.Data != nil {
				if t.isInactive() {
					msg.sess.queueOutWithOverrides(ErrLocked(msg.id, t.original(asUid), msg.timestamp), msg.sessOverrides)
					continue
				}
				if t.isReadOnly() {
					msg.sess.queueOutWithOverrides(ErrPermissionDenied(msg.id, t.original(asUid), msg.timestamp),
						msg.sessOverrides)
					continue
				}

				asUser := types.ParseUserId(msg.Data.From)
				userData, userFound := t.perUser[asUser]
				// Anyone is allowed to post to 'sys' topic.
				if t.cat != types.TopicCatSys {
					// If it's not 'sys' check write permission.
					if !(userData.modeWant & userData.modeGiven).IsWriter() {
						msg.sess.queueOutWithOverrides(ErrPermissionDenied(msg.id, t.original(asUid),
							msg.timestamp), msg.sessOverrides)
						continue
					}
				}

				if err := store.Messages.Save(&types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: msg.Data.Timestamp},
					SeqId:     t.lastID + 1,
					Topic:     t.name,
					From:      asUser.String(),
					Head:      msg.Data.Head,
					Content:   msg.Data.Content}, (userData.modeGiven & userData.modeWant).IsReader()); err != nil {

					log.Printf("topic[%s]: failed to save message: %v", t.name, err)
					msg.sess.queueOutWithOverrides(ErrUnknown(msg.id, t.original(asUid), msg.timestamp), msg.sessOverrides)

					continue
				}

				t.lastID++
				t.touched = msg.Data.Timestamp
				msg.Data.SeqId = t.lastID
				if userFound {
					userData.readID = t.lastID
					userData.readID = t.lastID
					t.perUser[asUser] = userData
				}
				if msg.id != "" {
					reply := NoErrAccepted(msg.id, t.original(asUid), msg.timestamp)
					reply.Ctrl.Params = map[string]int{"seq": t.lastID}
					msg.sess.queueOutWithOverrides(reply, msg.sessOverrides)
				}

				pushRcpt = t.pushForData(asUser, msg.Data)

				// Message sent: notify offline 'R' subscrbers on 'me'
				t.presSubsOffline("msg", &presParams{seqID: t.lastID, actor: msg.Data.From},
					&presFilters{filterIn: types.ModeRead}, "", true)

				// Tell the plugins that a message was accepted for delivery
				pluginMessage(msg.Data, plgActCreate)

			} else if msg.Pres != nil {
				if t.isInactive() {
					// Ignore presence update - topic is paused or being deleted
					continue
				}

				what := t.presProcReq(msg.Pres.Src, msg.Pres.What, msg.Pres.WantReply)
				if t.xoriginal != msg.Pres.Topic || what == "" {
					// This is just a request for status, don't forward it to sessions
					continue
				}

				// "what" may have changed, i.e. unset or "+command" removed ("on+en" -> "on")
				msg.Pres.What = what
			} else if msg.Info != nil {
				if t.isInactive() {
					// Ignore info messages - topic is paused or being deleted
					continue
				}

				if msg.Info.SeqId > t.lastID {
					// Drop bogus read notification
					continue
				}

				asUser := types.ParseUserId(msg.Info.From)
				pud := t.perUser[asUser]

				// Filter out "kp" from users with no 'W' permission (or people without a subscription)
				if msg.Info.What == "kp" && (!(pud.modeGiven & pud.modeWant).IsWriter() || t.isReadOnly()) {
					continue
				}

				if msg.Info.What == "read" || msg.Info.What == "recv" {
					// Filter out "read/recv" from users with no 'R' permission (or people without a subscription)
					if !(pud.modeGiven & pud.modeWant).IsReader() {
						continue
					}

					var read, recv, unread int
					if msg.Info.What == "read" {
						if msg.Info.SeqId > pud.readID {
							// The number of unread messages has decreased, negative value
							unread = pud.readID - msg.Info.SeqId
							pud.readID = msg.Info.SeqId
							read = pud.readID
						} else {
							// No need to report stale or bogus read status
							continue
						}
					} else if msg.Info.What == "recv" {
						if msg.Info.SeqId > pud.recvID {
							pud.recvID = msg.Info.SeqId
							recv = pud.recvID
						} else {
							continue
						}
					}

					if pud.readID > pud.recvID {
						pud.recvID = pud.readID
						recv = pud.recvID
					}

					if err := store.Subs.Update(t.name, asUser,
						map[string]interface{}{
							"RecvSeqId": pud.recvID,
							"ReadSeqId": pud.readID},
						false); err != nil {

						log.Printf("topic[%s]: failed to update SeqRead/Recv counter: %v", t.name, err)
						continue
					}

					// Read/recv updated: notify user's other sessions of the change
					t.presPubMessageCount(asUser, recv, read, msg.skipSid)

					// Update cached count of unread messages
					usersUpdateUnread(asUser, unread, true)

					t.perUser[asUser] = pud
				}
			}

			var broadcastSessOverrides *sessionOverrides
			if msg.sessOverrides != nil {
				// Broadcast is not a reply to a specific session.
				// Hence, we do not set session specific params.
				broadcastSessOverrides = &sessionOverrides{
					origReq: msg.sessOverrides.origReq,
				}
			}
			// Broadcast the message. Only {data}, {pres}, {info} are broadcastable.
			// {meta} and {ctrl} are sent to the session only
			if msg.Data != nil || msg.Pres != nil || msg.Info != nil {
				for sess, pssd := range t.sessions {
					if sess.sid == msg.skipSid {
						continue
					}

					if msg.Pres != nil {
						// Skip notifying - already notified on topic.
						if msg.Pres.SkipTopic != "" && sess.getSub(msg.Pres.SkipTopic) != nil {
							continue
						}

						// Notification addressed to a single user only.
						if msg.Pres.SingleUser != "" && pssd.uid.UserId() != msg.Pres.SingleUser {
							continue
						}
						// Notification should skip a single user.
						if msg.Pres.ExcludeUser != "" && pssd.uid.UserId() == msg.Pres.ExcludeUser {
							continue
						}

						// Check presence filters
						if !t.passesPresenceFilters(msg, pssd.uid) {
							continue
						}

					} else {
						// Check if the user has Read permission
						if !t.userIsReader(pssd.uid) {
							continue
						}

						// Don't send key presses from one user's session to the other sessions of the same user.
						if msg.Info != nil && msg.Info.What == "kp" && msg.Info.From == pssd.uid.UserId() {
							continue
						}
					}

					// Topic name may be different depending on the user to which the `sess` belongs.
					t.maybeFixTopicName(msg, pssd.uid)

					if !sess.queueOutWithOverrides(msg, broadcastSessOverrides) {
						log.Printf("topic[%s]: connection stuck, detaching", t.name)
						// The whole session is being dropped, so sessionLeave.userId is not set.
						t.unreg <- &sessionLeave{sess: sess}
					}
				}

				if pushRcpt != nil {
					// usersPush will update unread message count and send push notification.
					usersPush(pushRcpt)
				}

			} else {
				// TODO(gene): remove this
				log.Panic("topic: wrong message type for broadcasting", t.name)
			}

		case meta := <-t.meta:
			// Request to get/set topic metadata
			asUid := types.ParseUserId(meta.pkt.asUser)
			authLevel := auth.Level(meta.pkt.authLvl)
			switch {
			case meta.pkt.Get != nil:
				// Get request
				if meta.what&constMsgMetaDesc != 0 {
					if err := t.replyGetDesc(meta.sess, asUid, meta.pkt.Get.Id, meta.pkt.Get.Desc, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Desc failed: %s", t.name, err)
					}
				}
				if meta.what&constMsgMetaSub != 0 {
					if err := t.replyGetSub(meta.sess, asUid, authLevel, meta.pkt.Get.Id, meta.pkt.Get.Sub, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Sub failed: %s", t.name, err)
					}
				}
				if meta.what&constMsgMetaData != 0 {
					if err := t.replyGetData(meta.sess, asUid, meta.pkt.Get.Id, meta.pkt.Get.Data, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Data failed: %s", t.name, err)
					}
				}
				if meta.what&constMsgMetaDel != 0 {
					if err := t.replyGetDel(meta.sess, asUid, meta.pkt.Get.Id, meta.pkt.Get.Del, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Del failed: %s", t.name, err)
					}
				}
				if meta.what&constMsgMetaTags != 0 {
					if err := t.replyGetTags(meta.sess, asUid, meta.pkt.Get.Id, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Tags failed: %s", t.name, err)
					}
				}
				if meta.what&constMsgMetaCred != 0 {
					log.Printf("topic[%s] handle getCred", t.name)
					if err := t.replyGetCreds(meta.sess, asUid, meta.pkt.Get.Id, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Get.Creds failed: %s", t.name, err)
					}
				}

			case meta.pkt.Set != nil:
				// Set request
				if meta.what&constMsgMetaDesc != 0 {
					if err := t.replySetDesc(meta.sess, asUid, meta.pkt.Set, meta.sessOverrides); err == nil {
						// Notify plugins of the update
						pluginTopic(t, plgActUpd)
					} else {
						log.Printf("topic[%s] meta.Set.Desc failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaSub != 0 {
					if err := t.replySetSub(hub, meta.sess, meta.pkt, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Set.Sub failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaTags != 0 {
					if err := t.replySetTags(meta.sess, asUid, meta.pkt.Set, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Set.Tags failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaCred != 0 {
					if err := t.replySetCred(meta.sess, asUid, authLevel, meta.pkt.Set, meta.sessOverrides); err != nil {
						log.Printf("topic[%s] meta.Set.Cred failed: %v", t.name, err)
					}
				}

			case meta.pkt.Del != nil:
				// Del request
				var err error
				switch meta.what {
				case constMsgDelMsg:
					err = t.replyDelMsg(meta.sess, asUid, meta.pkt.Del, meta.sessOverrides)
				case constMsgDelSub:
					err = t.replyDelSub(hub, meta.sess, asUid, meta.pkt.Del, meta.sessOverrides)
				case constMsgDelTopic:
					err = t.replyDelTopic(hub, meta.sess, asUid, meta.pkt.Del, meta.sessOverrides)
				case constMsgDelCred:
					err = t.replyDelCred(hub, meta.sess, asUid, authLevel, meta.pkt.Del, meta.sessOverrides)
				}

				if err != nil {
					log.Printf("topic[%s] meta.Del failed: %v", t.name, err)
				}
			}
		case ua := <-t.uaChange:
			// Process an update to user agent from one of the sessions
			currentUA = ua
			uaTimer.Reset(uaTimerDelay)

		case <-defrNotifTimer.C:
			t.onDeferredNotificationTimer()

		case <-uaTimer.C:
			// Publish user agent changes after a delay
			if currentUA == "" || currentUA == t.userAgent {
				continue
			}
			t.userAgent = currentUA
			t.presUsersOfInterest("ua", t.userAgent)

		case <-killTimer.C:
			// Topic timeout
			hub.unreg <- &topicUnreg{topic: t.name}
			defrNotifTimer.Stop()
			if t.cat == types.TopicCatMe {
				uaTimer.Stop()
				t.presUsersOfInterest("off", currentUA)
			} else if t.cat == types.TopicCatGrp {
				t.presSubsOffline("off", nilPresParams, nilPresFilters, "", false)
			}

		case sd := <-t.exit:
			// Handle four cases:
			// 1. Topic is shutting down by timer due to inactivity (reason == StopNone)
			// 2. Topic is being deleted (reason == StopDeleted)
			// 3. System shutdown (reason == StopShutdown, done != nil).
			// 4. Cluster rehashing (reason == StopRehashing)

			if sd.reason == StopDeleted {
				if t.cat == types.TopicCatGrp {
					t.presSubsOffline("gone", nilPresParams, nilPresFilters, "", false)
				}
				// P2P users get "off+remove" earlier in the process

				// Inform plugins that the topic is deleted
				pluginTopic(t, plgActDel)

			} else if sd.reason == StopRehashing {
				// Must send individual messages to sessions because normal sending through the topic's
				// broadcast channel won't work - it will be shut down too soon.
				t.presSubsOnlineDirect("term", nilPresParams, nilPresFilters, "")
			}
			// In case of a system shutdown don't bother with notifications. They won't be delivered anyway.

			// Tell sessions to remove the topic
			for s := range t.sessions {
				s.detach <- t.name
			}

			usersRegisterTopic(t, false)

			// Report completion back to sender, if 'done' is not nil.
			if sd.done != nil {
				sd.done <- true
			}
			return
		}
	}
}

// sendDeferredNotifications updates perUser accounting and fires due
// deferred notifications for the provided sessions.
func (t *Topic) sendDeferredNotifications(joinReqs []*sessionJoin) {
	for _, sreg := range joinReqs {
		uid := types.ParseUserId(sreg.pkt.asUser)
		if !t.isProxy {
			pud := t.perUser[uid]
			pud.online++
			t.perUser[uid] = pud
		}
		t.sendSubNotifications(uid, sreg)
	}
}

// routeDeferredNotificationsToMaster forwards deferred notification requests to the master topic.
func (t *Topic) routeDeferredNotificationsToMaster(joinReqs []*sessionJoin) {
	var sendReqs []*ProxyDeferredSession
	for _, sreg := range joinReqs {
		s := &ProxyDeferredSession{
			AsUser: sreg.pkt.asUser,
		}
		sendReqs = append(sendReqs, s)
	}
	msg := &ProxyTopicData{
		DefrNotifReq: &ProxyDeferredNotifications{
			SendNotificationRequests: sendReqs,
		},
	}
	if err := globals.cluster.routeToTopicMaster(nil, nil, msg, t.name, nil); err != nil {
		log.Println("proxy topic: deferred notifications request from proxy to master failed:", err)
	}
}

// onDeferredNotificationTimer removes all due deferred notifications sessions
// from the notifications queue and for each of these sessions:
// * For regular topics, sends all due notifications.
// * For proxy topics, routes the deferred notification requests to the master topic.
func (t *Topic) onDeferredNotificationTimer() {
	// Handle deferred presence notifications from a successful service (background) subscription.
	if t.isInactive() {
		return
	}

	// Process events older than this timestamp.
	expiration := time.Now().Add(-deferredNotificationsTimeout)
	var joinReqs []*sessionJoin
	// Iterate through the list until all sufficiently old events are processed.
	for elem := t.defrNotif.Back(); elem != nil; elem = t.defrNotif.Back() {
		sreg := elem.Value.(*sessionJoin)
		if expiration.Before(sreg.pkt.timestamp) {
			// All done. Remaining events are newer.
			break
		}
		t.defrNotif.Remove(elem)
		if pssd, ok := t.sessions[sreg.sess]; ok && pssd.ref != nil {
			pssd.ref = nil
			t.sessions[sreg.sess] = pssd
			joinReqs = append(joinReqs, sreg)
		}
	}
	if t.isProxy {
		// TODO: route expired background sessions to the master topic.
		// t.routeToMasterTopic(joinReqs)
		t.routeDeferredNotificationsToMaster(joinReqs)
	} else {
		t.sendDeferredNotifications(joinReqs)
	}
}

// sidFromSessionOrOverrides returns sesssion id from session overrides (if provided)
// otherwise from session.
func sidFromSessionOrOverrides(sess *Session, sessOverrides *sessionOverrides) string {
	if sessOverrides != nil {
		return sessOverrides.sid
	} else {
		return sess.sid
	}
}

// Session subscribed to a topic, created == true if topic was just created and {pres} needs to be announced
func (t *Topic) handleSubscription(h *Hub, sreg *sessionJoin) error {
	asUid := types.ParseUserId(sreg.pkt.asUser)
	authLevel := auth.Level(sreg.pkt.authLvl)

	msgsub := sreg.pkt.Sub
	getWhat := 0
	if msgsub.Get != nil {
		getWhat = parseMsgClientMeta(msgsub.Get.What)
	}

	if err := t.subCommonReply(h, sreg); err != nil {
		return err
	}

	// Send notifications.

	// Some notifications are always sent immediately.
	t.sendImmediateSubNotifications(asUid, sreg)

	pssd, ok := t.sessions[sreg.sess]
	if msgsub.Background && ok {
		// Notifications are delayed.
		if !sreg.sess.isProxy() {
			pssd.ref = t.defrNotif.PushFront(sreg)
		}
		t.sessions[sreg.sess] = pssd
	} else {
		// Remaining notifications are also sent immediately.
		t.sendSubNotifications(asUid, sreg)
	}

	if getWhat&constMsgMetaDesc != 0 {
		// Send get.desc as a {meta} packet.
		if err := t.replyGetDesc(sreg.sess, asUid, sreg.pkt.id, msgsub.Get.Desc, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Desc failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	if getWhat&constMsgMetaSub != 0 {
		// Send get.sub response as a separate {meta} packet
		if err := t.replyGetSub(sreg.sess, asUid, authLevel, sreg.pkt.id, msgsub.Get.Sub, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Sub failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	if getWhat&constMsgMetaTags != 0 {
		// Send get.tags response as a separate {meta} packet
		if err := t.replyGetTags(sreg.sess, asUid, sreg.pkt.id, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Tags failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	if getWhat&constMsgMetaCred != 0 {
		// Send get.tags response as a separate {meta} packet
		if err := t.replyGetCreds(sreg.sess, asUid, sreg.pkt.id, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Cred failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	if getWhat&constMsgMetaData != 0 {
		// Send get.data response as {data} packets
		if err := t.replyGetData(sreg.sess, asUid, sreg.pkt.id, msgsub.Get.Data, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Data failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	if getWhat&constMsgMetaDel != 0 {
		// Send get.del response as a separate {meta} packet
		if err := t.replyGetDel(sreg.sess, asUid, sreg.pkt.id, msgsub.Get.Del, sreg.sessOverrides); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Del failed: %v sid=%s", t.name, err, sreg.sess.sid)
		}
	}

	return nil
}

// Send immediate presence notification in response to a subscription.
// Send push notification to the P2P counterpart.
// These notifications are always sent immediately even if background is requested.
func (t *Topic) sendImmediateSubNotifications(asUid types.Uid, sreg *sessionJoin) {
	pud := t.perUser[asUid]

	if t.cat == types.TopicCatP2P {
		uid2 := t.p2pOtherUser(asUid)
		pud2 := t.perUser[uid2]

		// Inform the other user that the topic was just created.
		if sreg.created {
			t.presSingleUserOffline(uid2, "acs", &presParams{
				dWant:  pud2.modeWant.String(),
				dGiven: pud2.modeGiven.String(),
				actor:  asUid.UserId()}, "", false)
		}

		if sreg.newsub {
			// Notify current user's 'me' topic to accept notifications from user2
			t.presSingleUserOffline(asUid, "?none+en", nilPresParams, "", false)

			// Initiate exchange of 'online' status with the other user.
			// We don't know if the current user is online in the 'me' topic,
			// so sending an '?unkn' status to user2. His 'me' topic
			// will reply with user2's status and request an actual status from user1.
			status := "?unkn"
			if (pud2.modeGiven & pud2.modeWant).IsPresencer() {
				// If user2 should receive notifications, enable it.
				status += "+en"
			}
			t.presSingleUserOffline(uid2, status, nilPresParams, "", false)

			// Also send a push notification to the other user.
			if pushRcpt := t.pushForSub(asUid, uid2, pud2.modeWant, pud2.modeGiven, types.TimeNow()); pushRcpt != nil {
				usersPush(pushRcpt)
			}
		}
	}

	// newsub could be true only for p2p and group topics, no need to check topic category explicitly.
	if sreg.newsub {
		// Notify creator's other sessions that the subscription (or the entire topic) was created.
		t.presSingleUserOffline(asUid, "acs",
			&presParams{
				dWant:  pud.modeWant.String(),
				dGiven: pud.modeGiven.String(),
				actor:  asUid.UserId()},
			sreg.sess.sid, false)
	}
}

// Send immediate or deferred presence notification in response to a subscription.
func (t *Topic) sendSubNotifications(asUid types.Uid, sreg *sessionJoin) {
	pud := t.perUser[asUid]

	switch t.cat {
	case types.TopicCatMe:
		// Notify user's contact that the given user is online now.
		if !t.isLoaded() {
			t.markLoaded()
			if err := t.loadContacts(asUid); err != nil {
				log.Println("topic: failed to load contacts", t.name, err.Error())
			}
			// User online: notify users of interest without forcing response (no +en here).
			t.presUsersOfInterest("on", sreg.sess.userAgent)
		}

	case types.TopicCatGrp:
		// Enable notifications for a new group topic, if appropriate.
		if !t.isLoaded() {
			t.markLoaded()
			status := "on"
			if (pud.modeGiven & pud.modeWant).IsPresencer() {
				status += "+en"
			}

			// Notify topic subscribers that the topic is online now.
			t.presSubsOffline(status, nilPresParams, nilPresFilters, "", false)
		} else if pud.online == 1 {
			// If this is the first session of the user in the topic.
			// Notify other online group members that the user is online now.
			t.presSubsOnline("on", asUid.UserId(), nilPresParams,
				&presFilters{filterIn: types.ModeRead},
				sidFromSessionOrOverrides(sreg.sess, sreg.sessOverrides))
		}
	}
}

// subCommonReply generates a response to a subscription request
func (t *Topic) subCommonReply(h *Hub, sreg *sessionJoin) error {
	// The topic is already initialized by the Hub
	var now time.Time
	// For newly created topics report topic creation time.
	if sreg.created {
		now = t.updated
	} else {
		now = types.TimeNow()
	}

	msgsub := sreg.pkt.Sub
	asUid := types.ParseUserId(sreg.pkt.asUser)
	asLvl := auth.Level(sreg.pkt.authLvl)
	toriginal := t.original(asUid)

	if !sreg.newsub && (t.cat == types.TopicCatP2P || t.cat == types.TopicCatGrp || t.cat == types.TopicCatSys) {
		// Check if this is a new subscription.
		pud, found := t.perUser[asUid]
		sreg.newsub = !found || pud.deleted
	}

	var private interface{}
	var mode string
	if msgsub.Set != nil {
		if msgsub.Set.Sub != nil {
			if msgsub.Set.Sub.User != "" {
				sreg.sess.queueOutWithOverrides(ErrMalformed(sreg.pkt.id, toriginal, now), sreg.sessOverrides)
				return errors.New("user id must not be specified")
			}

			mode = msgsub.Set.Sub.Mode
		}

		if msgsub.Set.Desc != nil {
			private = msgsub.Set.Desc.Private
		}
	}

	var err error
	var changed bool
	// Create new subscription or modify an existing one.
	if changed, err = t.thisUserSub(
		h, sreg.sess, asUid, asLvl, sreg.pkt.id, mode, private, msgsub.Background, sreg.sessOverrides); err != nil {
		return err
	}

	params := map[string]interface{}{}

	if changed {
		pud := t.perUser[asUid]
		// Report back the assigned access mode.
		params["acs"] = &MsgAccessMode{
			Given: pud.modeGiven.String(),
			Want:  pud.modeWant.String(),
			Mode:  (pud.modeGiven & pud.modeWant).String()}
	}

	// When a group topic is created, it's given a temporary name by the client.
	// Then this name changes. Report back the original name here.
	if sreg.created && sreg.pkt.topic != toriginal {
		params["tmpname"] = sreg.pkt.topic
	}

	if len(params) == 0 {
		// Don't send empty params '{}'
		params = nil
	}

	sreg.sess.queueOutWithOverrides(
		NoErrParams(sreg.pkt.id, toriginal, now, params), sreg.sessOverrides)

	return nil
}

// User requests or updates a self-subscription to a topic. Called as a
// result of {sub} or {meta set=sub}.
// Returns changed == true if user's accessmode has changed.
//
//	h							- hub
//	sess					- originating session
//	asUid					- id of the user making the request
//	asLvl					- access level of the user making the request
//	pktID					- id of {sub} or {set} packet
//	want					- requested access mode
//	private				- private value to assign to the subscription
//	background   	- presence notifications are deferred
//	sessOverrides - session param overrides
//
// Handle these cases:
// A. User is trying to subscribe for the first time (no subscription)
// B. User is already subscribed, just joining without changing anything
// C. User is responding to an earlier invite (modeWant was "N" in subscription)
// D. User is already subscribed, changing modeWant
// E. User is accepting ownership transfer (requesting ownership transfer is not permitted)
func (t *Topic) thisUserSub(h *Hub, sess *Session, asUid types.Uid, asLvl auth.Level,
	pktID, want string, private interface{}, background bool, sessOverrides *sessionOverrides) (bool, error) {

	now := types.TimeNow()
	toriginal := t.original(asUid)

	var changed bool

	// Access mode values as they were before this request was processed.
	oldWant := types.ModeNone
	oldGiven := types.ModeNone

	// Parse access mode requested by the user
	modeWant := types.ModeUnset
	if want != "" {
		if err := modeWant.UnmarshalText([]byte(want)); err != nil {
			sess.queueOutWithOverrides(ErrMalformed(pktID, toriginal, now), sessOverrides)
			return changed, err
		}
	}

	// Check if it's an attempt at a new subscription to the topic.
	// It could be an actual subscription (IsJoiner() == true) or a ban (IsJoiner() == false)
	userData, existingSub := t.perUser[asUid]
	if !existingSub || userData.deleted {
		// Check if the max number of subscriptions is already reached.
		if t.cat == types.TopicCatGrp && t.subsCount() >= globals.maxSubscriberCount {
			sess.queueOutWithOverrides(ErrPolicy(pktID, toriginal, now), sessOverrides)
			return changed, errors.New("max subscription count exceeded")
		}

		if t.cat == types.TopicCatP2P {
			// P2P could be here only if it was previously deleted. I.e. existingSub is always true for P2P.
			if modeWant != types.ModeUnset {
				userData.modeWant = modeWant
			}
			// If no modeWant is provided, leave existing one unchanged.

			// Make sure the user is not asking for unreasonable permissions
			userData.modeWant = (userData.modeWant & types.ModeCP2P) | types.ModeApprove
		} else if t.cat == types.TopicCatSys {
			if asLvl != auth.LevelRoot {
				sess.queueOutWithOverrides(ErrPermissionDenied(pktID, toriginal, now), sessOverrides)
				return changed, errors.New("subscription to 'sys' topic requires root access level")
			}

			// Assign default access levels
			userData.modeWant = types.ModeCSys
			userData.modeGiven = types.ModeCSys
			if modeWant != types.ModeUnset {
				userData.modeWant = (modeWant & types.ModeCSys) | types.ModeWrite
			}
		} else {
			// For non-p2p & non-sys topics access is given as default access
			userData.modeGiven = t.accessFor(asLvl)

			if modeWant == types.ModeUnset {
				// User wants default access mode.
				userData.modeWant = userData.modeGiven
			} else {
				userData.modeWant = modeWant
			}
		}

		// Undelete
		userData.deleted = false

		if isNullValue(private) {
			private = nil
		}
		userData.private = private

		// Add subscription to database
		sub := &types.Subscription{
			User:      asUid.String(),
			Topic:     t.name,
			ModeWant:  userData.modeWant,
			ModeGiven: userData.modeGiven,
			Private:   userData.private,
		}

		if err := store.Subs.Create(sub); err != nil {
			sess.queueOutWithOverrides(ErrUnknown(pktID, toriginal, now), sessOverrides)
			return changed, err
		}

		changed = true

		// Add user to cache.
		usersRegisterUser(asUid, true)

		// Notify plugins of a new subscription
		pluginSubscription(sub, plgActCreate)

	} else {
		// Process update to existing subscription. It could be an incomplete subscription for a new topic.

		var ownerChange bool

		// Save old access values

		oldWant = userData.modeWant
		oldGiven = userData.modeGiven

		if modeWant != types.ModeUnset {
			// Explicit modeWant is provided

			// Perform sanity checks
			if userData.modeGiven.IsOwner() {
				// Check for possible ownership transfer. Handle the following cases:
				// 1. Owner joining the topic without any changes
				// 2. Owner changing own settings
				// 3. Acceptance or rejection of the ownership transfer

				// Make sure the current owner cannot unset the owner flag or ban himself
				if t.owner == asUid && (!modeWant.IsOwner() || !modeWant.IsJoiner()) {
					sess.queueOutWithOverrides(ErrPermissionDenied(pktID, toriginal, now), sessOverrides)
					return changed, errors.New("cannot unset ownership or self-ban the owner")
				}

				// Ownership transfer
				ownerChange = modeWant.IsOwner() && !userData.modeWant.IsOwner()

				// The owner should be able to grant himself any access permissions.
				// If ownership transfer is rejected don't upgrade.
				if modeWant.IsOwner() && !userData.modeGiven.BetterEqual(modeWant) {
					userData.modeGiven |= modeWant
				}
			} else if modeWant.IsOwner() {
				// Ownership transfer can only be initiated by the owner.
				sess.queueOut(ErrPermissionDenied(pktID, toriginal, now))
				return changed, errors.New("non-owner cannot request ownership transfer")
			} else if t.cat == types.TopicCatGrp && userData.modeGiven.IsAdmin() && modeWant.IsAdmin() {
				// A group topic Admin should be able to grant himself any permissions except
				// ownership (checked previously) & hard-deleting messages.
				if !userData.modeGiven.BetterEqual(modeWant & ^types.ModeDelete) {
					userData.modeGiven |= (modeWant & ^types.ModeDelete)
				}
			}

			if t.cat == types.TopicCatP2P {
				// For P2P topics ignore requests for 'D'. Otherwise it will generate a useless announcement.
				modeWant = (modeWant & types.ModeCP2P) | types.ModeApprove
			} else if t.cat == types.TopicCatSys {
				// Anyone can always write to Sys topic.
				modeWant &= (modeWant & types.ModeCSys) | types.ModeWrite
			}
		}

		// If user has not requested a new access mode, provide one by default.
		if modeWant == types.ModeUnset {
			// If the user has self-banned before, un-self-ban. Otherwise do not make a change.
			if !oldWant.IsJoiner() {
				log.Println("No J permissions before")
				// Set permissions NO WORSE than default, but possibly better (admin or owner banned himself).
				userData.modeWant = userData.modeGiven | t.accessFor(asLvl)
			}
		} else if userData.modeWant != modeWant {
			// The user has provided a new modeWant and it' different from the one before
			userData.modeWant = modeWant
		}

		// Save changes to DB
		update := map[string]interface{}{}
		if isNullValue(private) {
			update["Private"] = nil
			userData.private = nil
		} else if private != nil {
			update["Private"] = private
			userData.private = private
		}
		if userData.modeWant != oldWant {
			update["ModeWant"] = userData.modeWant
		}
		if userData.modeGiven != oldGiven {
			update["ModeGiven"] = userData.modeGiven
		}
		if len(update) > 0 {
			if err := store.Subs.Update(t.name, asUid, update, true); err != nil {
				sess.queueOutWithOverrides(ErrUnknown(pktID, toriginal, now), sessOverrides)
				return false, err
			}
			changed = true
		}

		// No transactions in RethinkDB, but two owners are better than none
		if ownerChange {
			oldOwnerData := t.perUser[t.owner]
			oldOwnerData.modeGiven = (oldOwnerData.modeGiven & ^types.ModeOwner)
			oldOwnerData.modeWant = (oldOwnerData.modeWant & ^types.ModeOwner)
			if err := store.Subs.Update(t.name, t.owner,
				map[string]interface{}{
					"ModeWant":  oldOwnerData.modeWant,
					"ModeGiven": oldOwnerData.modeGiven}, false); err != nil {
				return changed, err
			}
			if err := store.Topics.OwnerChange(t.name, asUid); err != nil {
				return changed, err
			}
			t.perUser[t.owner] = oldOwnerData
			t.owner = asUid
		}
	}

	// If topic is being muted, send "off" notification and disable updates.
	// Do it before applying the new permissions.
	if (oldWant & oldGiven).IsPresencer() && !(userData.modeWant & userData.modeGiven).IsPresencer() {
		if t.cat == types.TopicCatMe {
			t.presUsersOfInterest("off+dis", t.userAgent)
		} else {
			t.presSingleUserOffline(asUid, "off+dis", nilPresParams, "", false)
		}
	}

	// Apply changes.
	t.perUser[asUid] = userData

	// Send presence notifications and update cached unread count.
	if oldWant != userData.modeWant || oldGiven != userData.modeGiven {
		oldReader := (oldWant & oldGiven).IsReader()
		newReader := (userData.modeWant & userData.modeGiven).IsReader()
		if oldReader && !newReader {
			// Decrement unread count
			usersUpdateUnread(asUid, userData.readID-t.lastID, true)
		} else if !oldReader && newReader {
			// Increment unread count
			usersUpdateUnread(asUid, t.lastID-userData.readID, true)
		}
		t.notifySubChange(asUid, asUid, oldWant, oldGiven, userData.modeWant, userData.modeGiven, sidFromSessionOrOverrides(sess, sessOverrides))
	}

	if !userData.modeWant.IsJoiner() {
		// The user is self-banning from the topic. Re-subscription will unban.
		t.evictUser(asUid, false, "")
		// The callee will send NoErrOK
		return changed, nil

	} else if !userData.modeGiven.IsJoiner() {
		// User was banned
		sess.queueOutWithOverrides(ErrPermissionDenied(pktID, toriginal, now), sessOverrides)
		return changed, errors.New("topic access denied; user is banned")
	}

	// Subscription successfully created. Link topic to session.
	// Note that sessions that serve as an interface between proxy topics and their masters (proxy sessions)
	// may have only one subscription, that is, to its master topic.
	if !sess.isProxy() || sess.countSub() == 0 {
		sess.addSub(t.name, &Subscription{
			broadcast: t.broadcast,
			done:      t.unreg,
			meta:      t.meta,
			uaChange:  t.uaChange})
		if sess.isProxy() {
			t.addSession(sess, types.ZeroUid)
		} else {
			t.addSession(sess, asUid)
		}
	}

	// The user is online in the topic. Increment the counter if notifications are not deferred.
	if !background {
		userData.online++
		t.perUser[asUid] = userData
	}

	return changed, nil
}

// anotherUserSub processes a request to initiate an invite or approve a subscription request from another user.
// Returns changed == true if user's access mode has changed.
// Handle these cases:
// A. Sharer or Approver is inviting another user for the first time (no prior subscription)
// B. Sharer or Approver is re-inviting another user (adjusting modeGiven, modeWant is still Unset)
// C. Approver is changing modeGiven for another user, modeWant != Unset
func (t *Topic) anotherUserSub(h *Hub, sess *Session, asUid, target types.Uid, set *MsgClientSet, sessOverrides *sessionOverrides) (bool, error) {

	now := types.TimeNow()
	toriginal := t.original(asUid)

	// Access mode values as they were before this request was processed.
	oldWant := types.ModeUnset
	oldGiven := types.ModeUnset

	// Access mode of the person who is executing this approval process
	var hostMode types.AccessMode

	// Check if approver actually has permission to manage sharing
	userData, ok := t.perUser[asUid]
	if !ok || !(userData.modeGiven & userData.modeWant).IsSharer() {
		sess.queueOut(ErrPermissionDenied(set.Id, toriginal, now))
		return false, errors.New("topic access denied; approver has no permission")
	}

	// Check if topic is suspended.
	if t.isReadOnly() {
		sess.queueOut(ErrPermissionDenied(set.Id, toriginal, now))
		return false, errors.New("topic is suspended")
	}

	hostMode = userData.modeGiven & userData.modeWant

	// Parse the access mode granted
	modeGiven := types.ModeUnset
	if set.Sub.Mode != "" {
		if err := modeGiven.UnmarshalText([]byte(set.Sub.Mode)); err != nil {
			sess.queueOut(ErrMalformed(set.Id, toriginal, now))
			return false, err
		}

		// Make sure the new permissions are reasonable in P2P topics: permissions no greater than default,
		// approver permission cannot be removed.
		if t.cat == types.TopicCatP2P {
			modeGiven = (modeGiven & types.ModeCP2P) | types.ModeApprove
		}
	}

	// Make sure only the owner & approvers can set non-default access mode
	if modeGiven != types.ModeUnset && !hostMode.IsAdmin() {
		sess.queueOut(ErrPermissionDenied(set.Id, toriginal, now))
		return false, errors.New("sharer cannot set explicit modeGiven")
	}

	// Make sure no one but the owner can do an ownership transfer
	if modeGiven.IsOwner() && t.owner != asUid {
		sess.queueOut(ErrPermissionDenied(set.Id, toriginal, now))
		return false, errors.New("attempt to transfer ownership by non-owner")
	}

	// Check if it's a new invite. If so, save it to database as a subscription.
	// Saved subscription does not mean the user is allowed to post/read
	userData, existingSub := t.perUser[target]
	if !existingSub {

		// Check if the max number of subscriptions is already reached.
		if t.cat == types.TopicCatGrp && t.subsCount() >= globals.maxSubscriberCount {
			sess.queueOut(ErrPolicy(set.Id, toriginal, now))
			return false, errors.New("max subscription count exceeded")
		}

		if modeGiven == types.ModeUnset {
			// Request to use default access mode for the new subscriptions.
			// Assuming LevelAuth. Approver should use non-default access if that is not suitable.
			modeGiven = t.accessFor(auth.LevelAuth)
		}

		// Get user's default access mode to be used as modeWant
		var modeWant types.AccessMode
		if user, err := store.Users.Get(target); err != nil {
			sess.queueOut(ErrUnknown(set.Id, toriginal, now))
			return false, err
		} else if user == nil {
			sess.queueOut(ErrUserNotFound(set.Id, toriginal, now))
			return false, errors.New("user not found")
		} else if user.State != types.StateOK {
			sess.queueOut(ErrPermissionDenied(set.Id, toriginal, now))
			return false, errors.New("user is suspended")
		} else {
			// Don't ask by default for more permissions than the granted ones.
			modeWant = user.Access.Auth & modeGiven
		}

		// Add subscription to database
		sub := &types.Subscription{
			User:      target.String(),
			Topic:     t.name,
			ModeWant:  modeWant,
			ModeGiven: modeGiven,
		}

		if err := store.Subs.Create(sub); err != nil {
			sess.queueOut(ErrUnknown(set.Id, toriginal, now))
			return false, err
		}

		userData = perUserData{
			modeGiven: sub.ModeGiven,
			modeWant:  sub.ModeWant,
			private:   nil,
		}
		t.perUser[target] = userData
		t.computePerUserAcsUnion()

		// Cache user's record
		usersRegisterUser(target, true)

		// Send push notification for the new subscription.
		if pushRcpt := t.pushForSub(asUid, target, userData.modeWant, userData.modeGiven, now); pushRcpt != nil {
			// TODO: maybe skip user's devices which were online when this event has happened.
			usersPush(pushRcpt)
		}
	} else {
		// Action on an existing subscription: re-invite, change existing permission, confirm/decline request.
		oldGiven = userData.modeGiven
		oldWant = userData.modeWant

		if modeGiven == types.ModeUnset {
			// Request to re-send invite without changing the access mode
			modeGiven = userData.modeGiven
		} else if modeGiven != userData.modeGiven {
			// Changing the previously assigned value
			userData.modeGiven = modeGiven

			// Save changed value to database
			if err := store.Subs.Update(t.name, target,
				map[string]interface{}{"ModeGiven": modeGiven}, false); err != nil {
				return false, err
			}

			t.perUser[target] = userData
		}
	}

	// Access mode has changed.
	var changed bool
	if oldGiven != userData.modeGiven {
		changed = true

		oldReader := (oldWant & oldGiven).IsReader()
		newReader := (userData.modeWant & userData.modeGiven).IsReader()
		if oldReader && !newReader {
			// Decrement unread count
			usersUpdateUnread(target, userData.readID-t.lastID, true)
		} else if !oldReader && newReader {
			// Increment unread count
			usersUpdateUnread(target, t.lastID-userData.readID, true)
		}
		t.notifySubChange(target, asUid, oldWant, oldGiven, userData.modeWant, userData.modeGiven, sidFromSessionOrOverrides(sess, sessOverrides))
	}

	if !userData.modeGiven.IsJoiner() {
		// The user is banned from the topic.
		t.evictUser(target, false, "")
	}

	return changed, nil
}

// replyGetDesc is a response to a get.desc request on a topic, sent to just the session as a {meta} packet
func (t *Topic) replyGetDesc(sess *Session, asUid types.Uid, id string, opts *MsgGetOpts, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if opts != nil && (opts.User != "" || opts.Limit != 0) {
		sess.queueOutWithOverrides(ErrMalformed(id, t.original(asUid), now), sessOverrides)
		return errors.New("invalid GetDesc query")
	}

	// Check if user requested modified data
	ifUpdated := opts == nil || opts.IfModifiedSince == nil || opts.IfModifiedSince.Before(t.updated)

	desc := &MsgTopicDesc{}
	if !ifUpdated {
		desc.CreatedAt = &t.created
	}
	if !t.updated.IsZero() {
		desc.UpdatedAt = &t.updated
	}

	pud, full := t.perUser[asUid]
	if t.cat == types.TopicCatP2P && !full {
		log.Println("missing p2p subscription in getDesc")
	}
	if t.cat == types.TopicCatMe {
		full = true
	}

	if ifUpdated {
		if t.public != nil {
			desc.Public = t.public
		} else if full && t.cat == types.TopicCatP2P {
			desc.Public = pud.public
		}
	}

	// Request may come from a subscriber (full == true) or a stranger.
	// Give subscriber a fuller description than to a stranger
	if full {
		if t.cat == types.TopicCatP2P {
			// For p2p topics default access mode makes no sense.
			// Don't report it.
		} else if t.cat == types.TopicCatMe || (pud.modeGiven & pud.modeWant).IsSharer() {
			desc.DefaultAcs = &MsgDefaultAcsMode{
				Auth: t.accessAuth.String(),
				Anon: t.accessAnon.String()}
		}

		desc.Acs = &MsgAccessMode{
			Want:  pud.modeWant.String(),
			Given: pud.modeGiven.String(),
			Mode:  (pud.modeGiven & pud.modeWant).String()}

		if t.cat == types.TopicCatMe && sess.authLvl == auth.LevelRoot {
			// If 'me' is in memory then user account is invariably not suspended.
			desc.State = types.StateOK.String()
		}

		if t.cat == types.TopicCatGrp && (pud.modeGiven & pud.modeWant).IsPresencer() {
			desc.Online = t.isOnline()
		}
		if ifUpdated {
			desc.Private = pud.private
		}

		// Don't report message IDs to users without Read access.
		if (pud.modeGiven & pud.modeWant).IsReader() {
			desc.SeqId = t.lastID
			if !t.touched.IsZero() {
				desc.TouchedAt = &t.touched
			}

			// Make sure reported values are sane:
			// t.delID <= pud.delID; t.readID <= t.recvID <= t.lastID
			desc.DelId = max(pud.delID, t.delID)
			desc.ReadSeqId = pud.readID
			desc.RecvSeqId = max(pud.recvID, pud.readID)
		} else {
			// Send some sane value of touched.
			desc.TouchedAt = &t.updated
		}
	}

	sess.queueOutWithOverrides(&ServerComMessage{
		Meta: &MsgServerMeta{
			Id:        id,
			Topic:     t.original(asUid),
			Desc:      desc,
			Timestamp: &now}}, sessOverrides)

	return nil
}

// replySetDesc updates topic metadata, saves it to DB,
// replies to the caller as {ctrl} message, generates {pres} update if necessary
func (t *Topic) replySetDesc(sess *Session, asUid types.Uid, set *MsgClientSet, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	assignAccess := func(upd map[string]interface{}, mode *MsgDefaultAcsMode) error {
		if mode == nil {
			return nil
		}
		if auth, anon, err := parseTopicAccess(mode, types.ModeUnset, types.ModeUnset); err != nil {
			return err
		} else if auth.IsOwner() || anon.IsOwner() {
			return errors.New("default 'owner' access is not permitted")
		} else {
			access := types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon}
			if auth != types.ModeUnset {
				if t.cat == types.TopicCatMe {
					auth &= types.ModeCAuth
					if auth != types.ModeNone {
						// This is the default access mode for P2P topics.
						// It must be either an N or must include an A permission.
						auth |= types.ModeApprove
					}
				}
				access.Auth = auth
			}
			if anon != types.ModeUnset {
				if t.cat == types.TopicCatMe {
					anon &= types.ModeCP2P
					if anon != types.ModeNone {
						anon |= types.ModeApprove
					}
				}
				access.Anon = anon
			}
			if access.Auth != t.accessAuth || access.Anon != t.accessAnon {
				upd["Access"] = access
			}
		}
		return nil
	}

	assignGenericValues := func(upd map[string]interface{}, what string, dst, src interface{}) (changed bool) {
		if dst, changed = mergeInterfaces(dst, src); changed {
			upd[what] = dst
		}
		return
	}

	var err error
	// DefaultAccess and/or Public have chanegd
	var sendCommon bool
	// Private has changed
	var sendPriv bool

	// Change to the main object
	core := make(map[string]interface{})
	// Change to subscription
	sub := make(map[string]interface{})
	if set.Desc != nil {
		switch t.cat {
		case types.TopicCatMe:
			// Update current user
			err = assignAccess(core, set.Desc.DefaultAcs)
			sendCommon = assignGenericValues(core, "Public", t.public, set.Desc.Public)
		case types.TopicCatFnd:
			// set.Desc.DefaultAcs is ignored.
			// Do not send presence if fnd.Public has changed.
			assignGenericValues(core, "Public", t.fndGetPublic(sess), set.Desc.Public)
		case types.TopicCatP2P:
			// Reject direct changes to P2P topics.
			if set.Desc.Public != nil || set.Desc.DefaultAcs != nil {
				sess.queueOutWithOverrides(ErrPermissionDenied(set.Id, set.Topic, now), sessOverrides)
				return errors.New("incorrect attempt to change metadata of a p2p topic")
			}
		case types.TopicCatGrp:
			// Update group topic
			if t.owner == asUid {
				err = assignAccess(core, set.Desc.DefaultAcs)
				sendCommon = assignGenericValues(core, "Public", t.public, set.Desc.Public)
			} else if set.Desc.DefaultAcs != nil || set.Desc.Public != nil {
				// This is a request from non-owner
				sess.queueOutWithOverrides(ErrPermissionDenied(set.Id, set.Topic, now), sessOverrides)
				return errors.New("attempt to change public or permissions by non-owner")
			}
		}

		if err != nil {
			sess.queueOutWithOverrides(ErrMalformed(set.Id, set.Topic, now), sessOverrides)
			return err
		}

		sendPriv = assignGenericValues(sub, "Private", t.perUser[asUid].private, set.Desc.Private)
	}

	if len(core) > 0 {
		core["UpdatedAt"] = now
		switch t.cat {
		case types.TopicCatMe:
			err = store.Users.Update(asUid, core)
		case types.TopicCatFnd:
			// The only value to be stored in topic is Public, and Public for fnd is not saved according to specs.
		default:
			err = store.Topics.Update(t.name, core)
		}
	}
	if err == nil && len(sub) > 0 {
		err = store.Subs.Update(t.name, asUid, sub, true)
	}

	if err != nil {
		sess.queueOutWithOverrides(ErrUnknown(set.Id, set.Topic, now), sessOverrides)
		return err
	} else if len(core)+len(sub) == 0 {
		sess.queueOutWithOverrides(InfoNotModified(set.Id, set.Topic, now), sessOverrides)
		return errors.New("{set} generated no update to DB")
	}

	// Update values cached in the topic object
	if t.cat == types.TopicCatMe || t.cat == types.TopicCatGrp {
		if tmp, ok := core["Access"]; ok {
			access := tmp.(types.DefaultAccess)
			t.accessAuth = access.Auth
			t.accessAnon = access.Anon
		}
		if public, ok := core["Public"]; ok {
			t.public = public
		}
	} else if t.cat == types.TopicCatFnd {
		// Assign per-session fnd.Public.
		t.fndSetPublic(sess, core["Public"])
	}
	if private, ok := sub["Private"]; ok {
		pud := t.perUser[asUid]
		pud.private = private
		pud.updated = now
		t.perUser[asUid] = pud
	}

	if sendCommon || sendPriv {
		// t.public, t.accessAuth/Anon have changed, make an announcement
		if sendCommon {
			if t.cat == types.TopicCatMe {
				t.presUsersOfInterest("upd", "")
			} else {
				// Notify all subscribers on 'me' except the user who made the change.
				// He will be notified separately (see below).
				t.presSubsOffline("upd", nilPresParams, &presFilters{excludeUser: asUid.UserId()}, sess.sid, false)
			}

			t.updated = now
		}
		// Notify user's other sessions.
		t.presSingleUserOffline(asUid, "upd", nilPresParams, sess.sid, false)
	}

	sess.queueOutWithOverrides(NoErr(set.Id, set.Topic, now), sessOverrides)

	return nil
}

// replyGetSub is a response to a get.sub request on a topic - load a list of subscriptions/subscribers,
// send it just to the session as a {meta} packet
func (t *Topic) replyGetSub(sess *Session, asUid types.Uid, authLevel auth.Level, id string, req *MsgGetOpts, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if req != nil && (req.SinceId != 0 || req.BeforeId != 0) {
		sess.queueOutWithOverrides(ErrMalformed(id, t.original(asUid), now), sessOverrides)
		return errors.New("invalid MsgGetOpts query")
	}

	userData := t.perUser[asUid]
	if !(userData.modeGiven & userData.modeWant).IsSharer() {
		sess.queueOutWithOverrides(ErrPermissionDenied(id, t.original(asUid), now), sessOverrides)
		return errors.New("user does not have S permission")
	}

	var ifModified time.Time
	if req != nil && req.IfModifiedSince != nil {
		ifModified = *req.IfModifiedSince
	}

	var subs []types.Subscription
	var err error

	switch t.cat {
	case types.TopicCatMe:
		if req != nil {
			// If topic is provided, it could be in the form of user ID 'usrAbCd'.
			// Convert it to P2P topic name.
			if uid2 := types.ParseUserId(req.Topic); !uid2.IsZero() {
				req.Topic = uid2.P2PName(asUid)
			}
		}
		// Fetch user's subscriptions, with Topic.Public denormalized into subscription.
		if ifModified.IsZero() {
			// No cache management. Skip deleted subscriptions.
			subs, err = store.Users.GetTopics(asUid, msgOpts2storeOpts(req))
		} else {
			// User manages cache. Include deleted subscriptions too.
			subs, err = store.Users.GetTopicsAny(asUid, msgOpts2storeOpts(req))
		}
	case types.TopicCatFnd:
		// Select public or private query. Public has priority.
		raw := t.fndGetPublic(sess)
		if raw == nil {
			raw = userData.private
		}

		if query, ok := raw.(string); ok && len(query) > 0 {
			query, subs, err = pluginFind(asUid, query)
			if err == nil && subs == nil && query != "" {
				var req, opt []string
				if req, opt, err = parseSearchQuery(query); err == nil {
					if len(req) > 0 || len(opt) > 0 {
						// Check if the query contains terms that the user is not allowed to use.
						restr, _ := stringSliceDelta(t.tags, filterRestrictedTags(append(req, opt...),
							globals.maskedTagNS))

						if len(restr) > 0 {
							sess.queueOutWithOverrides(ErrPermissionDenied(id, t.original(asUid), now), sessOverrides)
							return errors.New("attempt to search by restricted tags")
						}

						// FIXME: allow root to find suspended users and topics.
						subs, err = store.Users.FindSubs(asUid, req, opt)
						if err != nil {
							sess.queueOutWithOverrides(decodeStoreError(err, id, t.original(asUid), now, nil), sessOverrides)
							return err
						}

					} else {
						// Query string is empty.
						sess.queueOutWithOverrides(ErrMalformed(id, t.original(asUid), now), sessOverrides)
						return errors.New("empty search query")
					}
				} else {
					// Query parsing error. Report it externally as a generic ErrMalformed.
					sess.queueOutWithOverrides(ErrMalformed(id, t.original(asUid), now), sessOverrides)
					return errors.New("failed to parse search query; " + err.Error())
				}
			}
		}
	case types.TopicCatP2P:
		// FIXME(gene): don't load subs from DB, use perUserData - it already contains subscriptions.
		// No need to load Public for p2p topics.
		if ifModified.IsZero() {
			// No cache management. Skip deleted subscriptions.
			subs, err = store.Topics.GetSubs(t.name, msgOpts2storeOpts(req))
		} else {
			// User manages cache. Include deleted subscriptions too.
			subs, err = store.Topics.GetSubsAny(t.name, msgOpts2storeOpts(req))
		}
	case types.TopicCatGrp:
		// Include sub.Public.
		if ifModified.IsZero() {
			// No cache management. Skip deleted subscriptions.
			subs, err = store.Topics.GetUsers(t.name, msgOpts2storeOpts(req))
		} else {
			// User manages cache. Include deleted subscriptions too.
			subs, err = store.Topics.GetUsersAny(t.name, msgOpts2storeOpts(req))
		}
	}

	if err != nil {
		sess.queueOutWithOverrides(decodeStoreError(err, id, t.original(asUid), now, nil), sessOverrides)
		return err
	}

	if len(subs) > 0 {
		meta := &MsgServerMeta{Id: id, Topic: t.original(asUid), Timestamp: &now}
		meta.Sub = make([]MsgTopicSub, 0, len(subs))
		presencer := (userData.modeGiven & userData.modeWant).IsPresencer()

		for i := range subs {
			sub := &subs[i]
			// Indicator if the requester has provided a cut off date for ts of pub & priv updates.
			var sendPubPriv bool
			var banned bool
			var mts MsgTopicSub
			deleted := sub.DeletedAt != nil

			if ifModified.IsZero() {
				sendPubPriv = true
			} else {
				// Skip sending deleted subscriptions if they were deleted before the cut off date.
				// If they are freshly deleted send minimum info
				if deleted {
					if !sub.DeletedAt.After(ifModified) {
						continue
					}
					mts.DeletedAt = sub.DeletedAt
				}
				sendPubPriv = !deleted && sub.UpdatedAt.After(ifModified)
			}

			uid := types.ParseUid(sub.User)
			isReader := (sub.ModeGiven & sub.ModeWant).IsReader()
			if t.cat == types.TopicCatMe {
				// Mark subscriptions that the user does not care about.
				if !(sub.ModeWant & sub.ModeGiven).IsJoiner() {
					banned = true
				}

				// Reporting user's subscriptions to other topics. P2P topic name is the
				// UID of the other user.
				with := sub.GetWith()
				if with != "" {
					mts.Topic = with
					mts.Online = t.perSubs[with].online && !deleted && presencer
				} else {
					mts.Topic = sub.Topic
					mts.Online = t.perSubs[sub.Topic].online && !deleted && presencer
				}

				if !deleted && !banned {
					if isReader {
						if sub.GetTouchedAt().IsZero() {
							mts.TouchedAt = nil
						} else {
							touchedAt := sub.GetTouchedAt()
							mts.TouchedAt = &touchedAt
						}
						mts.SeqId = sub.GetSeqId()
						mts.DelId = sub.DelId
					} else {
						mts.TouchedAt = &sub.UpdatedAt
					}

					lastSeen := sub.GetLastSeen()
					if !lastSeen.IsZero() && !mts.Online {
						mts.LastSeen = &MsgLastSeenInfo{
							When:      &lastSeen,
							UserAgent: sub.GetUserAgent()}
					}
				}
			} else {
				// Mark subscriptions that the user does not care about.
				if t.cat == types.TopicCatGrp && !(sub.ModeWant & sub.ModeGiven).IsJoiner() {
					banned = true
				}

				// Reporting subscribers to fnd, a group or a p2p topic
				mts.User = uid.UserId()
				if t.cat == types.TopicCatFnd {
					mts.Topic = sub.Topic
				}

				if !deleted {
					if uid == asUid && isReader && !banned {
						// Report deleted ID for own subscriptions only
						mts.DelId = sub.DelId
					}

					if t.cat == types.TopicCatGrp {
						pud := t.perUser[uid]
						mts.Online = pud.online > 0 && presencer
					}
				}
			}

			if !deleted {
				mts.UpdatedAt = &sub.UpdatedAt
				if isReader && !banned {
					mts.ReadSeqId = sub.ReadSeqId
					mts.RecvSeqId = sub.RecvSeqId
				}

				if t.cat != types.TopicCatFnd {
					mts.Acs.Mode = (sub.ModeGiven & sub.ModeWant).String()
					mts.Acs.Want = sub.ModeWant.String()
					mts.Acs.Given = sub.ModeGiven.String()
				} else {
					// Topic 'fnd'
					// sub.ModeXXX may be defined by the plugin.
					if sub.ModeGiven.IsDefined() && sub.ModeWant.IsDefined() {
						mts.Acs.Mode = (sub.ModeGiven & sub.ModeWant).String()
						mts.Acs.Want = sub.ModeWant.String()
						mts.Acs.Given = sub.ModeGiven.String()
					} else if defacs := sub.GetDefaultAccess(); defacs != nil {
						switch authLevel {
						case auth.LevelAnon:
							mts.Acs.Mode = defacs.Anon.String()
						case auth.LevelAuth, auth.LevelRoot:
							mts.Acs.Mode = defacs.Auth.String()
						}
					}
				}

				// Returning public and private only if they have changed since ifModified
				if sendPubPriv {
					// 'sub' has nil 'public' in p2p topics which is OK.
					mts.Public = sub.GetPublic()
					// Reporting 'private' only if it's user's own subscription.
					if uid == asUid {
						mts.Private = sub.Private
					}
				}

				// Always reporting 'private' for fnd topic.
				if t.cat == types.TopicCatFnd {
					mts.Private = sub.Private
				}
			}

			meta.Sub = append(meta.Sub, mts)
		}
		sess.queueOutWithOverrides(&ServerComMessage{Meta: meta}, sessOverrides)
	} else {
		// Inform the client that there are no subscriptions.
		sess.queueOutWithOverrides(NoErrParams(id, t.original(asUid), now, map[string]interface{}{"what": "sub"}), sessOverrides)
	}

	return nil
}

// replySetSub is a response to new subscription request or an update to a subscription {set.sub}:
// update topic metadata cache, save/update subs, reply to the caller as {ctrl} message,
// generate a presence notification, if appropriate.
func (t *Topic) replySetSub(h *Hub, sess *Session, pkt *ClientComMessage, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	asUid := types.ParseUserId(pkt.asUser)
	asLvl := auth.Level(pkt.authLvl)
	set := pkt.Set
	toriginal := t.original(asUid)

	var target types.Uid
	if target = types.ParseUserId(set.Sub.User); target.IsZero() && set.Sub.User != "" {
		// Invalid user ID
		sess.queueOutWithOverrides(ErrMalformed(pkt.id, toriginal, now), sessOverrides)
		return errors.New("invalid user id")
	}

	// if set.User is not set, request is for the current user
	if target.IsZero() {
		target = asUid
	}

	var err error
	var changed bool
	if target == asUid {
		// Request new subscription or modify own subscription
		changed, err = t.thisUserSub(h, sess, asUid, asLvl, pkt.id, set.Sub.Mode, nil, false, nil)
	} else {
		// Request to approve/change someone's subscription
		changed, err = t.anotherUserSub(h, sess, asUid, target, set, sessOverrides)
	}
	if err != nil {
		return err
	}

	var resp *ServerComMessage
	if changed {
		// Report resulting access mode.
		pud := t.perUser[target]
		params := map[string]interface{}{"acs": MsgAccessMode{
			Given: pud.modeGiven.String(),
			Want:  pud.modeWant.String(),
			Mode:  (pud.modeGiven & pud.modeWant).String()}}
		if target != asUid {
			params["user"] = target.UserId()
		}
		resp = NoErrParams(pkt.id, toriginal, now, params)
	} else {
		resp = InfoNotModified(pkt.id, toriginal, now)
	}

	sess.queueOutWithOverrides(resp, sessOverrides)

	return nil
}

// replyGetData is a response to a get.data request - load a list of stored messages, send them to session as {data}
// response goes to a single session rather than all sessions in a topic
func (t *Topic) replyGetData(sess *Session, asUid types.Uid, id string, req *MsgGetOpts, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()
	toriginal := t.original(asUid)

	if req != nil && (req.IfModifiedSince != nil || req.User != "" || req.Topic != "") {
		sess.queueOutWithOverrides(ErrMalformed(id, toriginal, now), sessOverrides)
		return errors.New("invalid MsgGetOpts query")
	}

	// Check if the user has permission to read the topic data
	count := 0
	if userData := t.perUser[asUid]; (userData.modeGiven & userData.modeWant).IsReader() {
		// Read messages from DB
		messages, err := store.Messages.GetAll(t.name, asUid, msgOpts2storeOpts(req))
		if err != nil {
			sess.queueOutWithOverrides(ErrUnknown(id, toriginal, now), sessOverrides)
			return err
		}

		// Push the list of messages to the client as {data}.
		if messages != nil {
			count = len(messages)
			for i := range messages {
				mm := &messages[i]
				sess.queueOutWithOverrides(&ServerComMessage{Data: &MsgServerData{
					Topic:     toriginal,
					Head:      mm.Head,
					SeqId:     mm.SeqId,
					From:      types.ParseUid(mm.From).UserId(),
					Timestamp: mm.CreatedAt,
					Content:   mm.Content}}, sessOverrides)
			}
		}
	}

	// Inform the requester that all the data has been served.
	sess.queueOutWithOverrides(NoErrParams(id, toriginal, now, map[string]interface{}{"what": "data", "count": count}), sessOverrides)

	return nil
}

// replyGetTags returns topic's tags - tokens used for discovery.
func (t *Topic) replyGetTags(sess *Session, asUid types.Uid, id string, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if t.cat != types.TopicCatMe && t.cat != types.TopicCatGrp {
		sess.queueOutWithOverrides(ErrOperationNotAllowed(id, t.original(asUid), now), sessOverrides)
		return errors.New("invalid topic category for getting tags")
	}
	if t.cat == types.TopicCatGrp && t.owner != asUid {
		sess.queueOutWithOverrides(ErrPermissionDenied(id, t.original(asUid), now), sessOverrides)
		return errors.New("request for tags from non-owner")
	}

	if len(t.tags) > 0 {
		sess.queueOutWithOverrides(&ServerComMessage{
			Meta: &MsgServerMeta{Id: id, Topic: t.original(asUid), Timestamp: &now, Tags: t.tags}}, sessOverrides)
		return nil
	}

	// Inform the requester that there are no tags.
	sess.queueOutWithOverrides(NoErrParams(id, t.original(asUid), now, map[string]string{"what": "tags"}), sessOverrides)

	return nil
}

// replySetTags updates topic's tags - tokens used for discovery.
func (t *Topic) replySetTags(sess *Session, asUid types.Uid, set *MsgClientSet, sessOverrides *sessionOverrides) error {
	var resp *ServerComMessage
	var err error

	now := types.TimeNow()

	if t.cat != types.TopicCatMe && t.cat != types.TopicCatGrp {
		resp = ErrOperationNotAllowed(set.Id, t.original(asUid), now)
		err = errors.New("invalid topic category to assign tags")

	} else if t.cat == types.TopicCatGrp && t.owner != asUid {
		resp = ErrPermissionDenied(set.Id, t.original(asUid), now)
		err = errors.New("tags update by non-owner")

	} else if tags := normalizeTags(set.Tags); tags != nil {
		if !restrictedTagsEqual(t.tags, tags, globals.immutableTagNS) {
			err = errors.New("attempt to mutate restricted tags")
			resp = ErrPermissionDenied(set.Id, t.original(asUid), now)
		} else {
			added, removed := stringSliceDelta(t.tags, tags)
			if len(added) > 0 || len(removed) > 0 {
				update := map[string]interface{}{"Tags": types.StringSlice(tags), "UpdatedAt": now}
				if t.cat == types.TopicCatMe {
					err = store.Users.Update(asUid, update)
				} else if t.cat == types.TopicCatGrp {
					err = store.Topics.Update(t.name, update)
				}

				if err != nil {
					resp = ErrUnknown(set.Id, t.original(asUid), now)
				} else {
					t.tags = tags
					t.presSubsOnline("tags", "", nilPresParams, &presFilters{singleUser: asUid.UserId()}, "")

					params := make(map[string]interface{})
					if len(added) > 0 {
						params["added"] = len(added)
					}
					if len(removed) > 0 {
						params["removed"] = len(removed)
					}
					resp = NoErrParams(set.Id, t.original(asUid), now, params)
				}
			} else {
				resp = InfoNotModified(set.Id, t.original(asUid), now)
			}
		}
	} else {
		resp = InfoNotModified(set.Id, t.original(asUid), now)
	}

	sess.queueOutWithOverrides(resp, sessOverrides)

	return err
}

// replyGetCreds returns user's credentials such as email and phone numbers.
func (t *Topic) replyGetCreds(sess *Session, asUid types.Uid, id string, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if t.cat != types.TopicCatMe {
		sess.queueOutWithOverrides(ErrOperationNotAllowed(id, t.original(asUid), now), sessOverrides)
		return errors.New("invalid topic category for getting credentials")
	}

	screds, err := store.Users.GetAllCreds(asUid, "", false)
	if err != nil {
		sess.queueOutWithOverrides(decodeStoreError(err, id, t.original(asUid), now, nil), sessOverrides)
		return err
	}

	if len(screds) > 0 {
		creds := make([]*MsgCredServer, len(screds))
		for i, sc := range screds {
			creds[i] = &MsgCredServer{Method: sc.Method, Value: sc.Value, Done: sc.Done}
		}
		sess.queueOutWithOverrides(&ServerComMessage{
			Meta: &MsgServerMeta{Id: id, Topic: t.original(asUid), Timestamp: &now, Cred: creds}}, sessOverrides)
		return nil
	}

	// Inform the requester that there are no credentials.
	sess.queueOutWithOverrides(NoErrParams(id, t.original(asUid), now, map[string]string{"what": "creds"}), sessOverrides)

	return nil
}

// replySetCreds adds or validates user credentials such as email and phone numbers.
func (t *Topic) replySetCred(sess *Session, asUid types.Uid, authLevel auth.Level, set *MsgClientSet, sessOverrides *sessionOverrides) error {

	now := types.TimeNow()
	if t.cat != types.TopicCatMe {
		sess.queueOutWithOverrides(ErrOperationNotAllowed(set.Id, t.original(asUid), now), sessOverrides)
		return errors.New("invalid topic category for updating credentials")
	}

	var err error
	var tags []string
	creds := []MsgCredClient{*set.Cred}
	if set.Cred.Response != "" {
		// Credential is being validated. Return an arror if response is invalid.
		_, tags, err = validatedCreds(asUid, authLevel, creds, true)
	} else {
		// Credential is being added or updated.
		tmpToken, _, _ := store.GetLogicalAuthHandler("token").GenSecret(&auth.Rec{
			Uid:       asUid,
			AuthLevel: auth.LevelNone,
			Lifetime:  time.Hour * 24,
			Features:  auth.FeatureNoLogin})
		_, tags, err = addCreds(asUid, creds, nil, sess.lang, tmpToken)
	}

	if tags != nil {
		t.tags = tags
		t.presSubsOnline("tags", "", nilPresParams, nilPresFilters, "")
	}

	sess.queueOutWithOverrides(decodeStoreError(err, set.Id, t.original(asUid), now, nil), sessOverrides)

	return err
}

// replyGetDel is a response to a get[what=del] request: load a list of deleted message ids, send them to
// a session as {meta}
// response goes to a single session rather than all sessions in a topic
func (t *Topic) replyGetDel(sess *Session, asUid types.Uid, id string, req *MsgGetOpts, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()
	toriginal := t.original(asUid)

	if req != nil && (req.IfModifiedSince != nil || req.User != "" || req.Topic != "") {
		sess.queueOutWithOverrides(ErrMalformed(id, toriginal, now), sessOverrides)
		return errors.New("invalid MsgGetOpts query")
	}

	// Check if the user has permission to read the topic data and the request is valid
	if userData := t.perUser[asUid]; (userData.modeGiven & userData.modeWant).IsReader() {
		ranges, delID, err := store.Messages.GetDeleted(t.name, asUid, msgOpts2storeOpts(req))
		if err != nil {
			sess.queueOutWithOverrides(ErrUnknown(id, toriginal, now), sessOverrides)
			return err
		}

		if len(ranges) > 0 {
			sess.queueOutWithOverrides(&ServerComMessage{Meta: &MsgServerMeta{
				Id:    id,
				Topic: toriginal,
				Del: &MsgDelValues{
					DelId:  delID,
					DelSeq: delrangeDeserialize(ranges)},
				Timestamp: &now}}, sessOverrides)
			return nil
		}
	}

	sess.queueOutWithOverrides(NoErrParams(id, toriginal, now, map[string]string{"what": "del"}), sessOverrides)

	return nil
}

// replyDelMsg deletes (soft or hard) messages in response to del.msg packet.
func (t *Topic) replyDelMsg(sess *Session, asUid types.Uid, del *MsgClientDel, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	var err error

	pud := t.perUser[asUid]
	if !(pud.modeGiven & pud.modeWant).IsDeleter() {
		// User must have an R permission: if the user cannot read messages, he has
		// no business of deleting them.
		if !(pud.modeGiven & pud.modeWant).IsReader() {
			sess.queueOutWithOverrides(ErrPermissionDenied(del.Id, t.original(asUid), now), sessOverrides)
			return errors.New("del.msg: permission denied")
		}

		// User has just the R permission, cannot hard-delete messages, silently
		// switching to soft-deleting
		del.Hard = false
	}

	var ranges []types.Range
	if len(del.DelSeq) == 0 {
		err = errors.New("del.msg: no IDs to delete")
	} else {
		count := 0
		for _, dq := range del.DelSeq {
			if dq.LowId > t.lastID || dq.LowId < 0 || dq.HiId < 0 ||
				(dq.HiId > 0 && dq.LowId > dq.HiId) ||
				(dq.LowId == 0 && dq.HiId == 0) {
				err = errors.New("del.msg: invalid entry in list")
				break
			}

			if dq.HiId > t.lastID {
				// Range is inclusive - exclusive [low, hi),
				// to delete all messages hi must be lastId + 1
				dq.HiId = t.lastID + 1
			} else if dq.LowId == dq.HiId || dq.LowId+1 == dq.HiId {
				dq.HiId = 0
			}

			if dq.HiId == 0 {
				count++
			} else {
				count += dq.HiId - dq.LowId
			}

			ranges = append(ranges, types.Range{Low: dq.LowId, Hi: dq.HiId})
		}

		if err == nil {
			// Sort by Low ascending then by Hi descending.
			sort.Sort(types.RangeSorter(ranges))
			// Collapse overlapping ranges
			ranges = types.RangeSorter(ranges).Normalize()
		}

		if count > defaultMaxDeleteCount && len(ranges) > 1 {
			err = errors.New("del.msg: too many messages to delete")
		}
	}

	if err != nil {
		sess.queueOutWithOverrides(ErrMalformed(del.Id, t.original(asUid), now), sessOverrides)
		return err
	}

	forUser := asUid
	if del.Hard {
		forUser = types.ZeroUid
	}

	if err = store.Messages.DeleteList(t.name, t.delID+1, forUser, ranges); err != nil {
		sess.queueOutWithOverrides(ErrUnknown(del.Id, t.original(asUid), now), sessOverrides)
		return err
	}

	// Increment Delete transaction ID
	t.delID++
	dr := delrangeDeserialize(ranges)
	if del.Hard {
		for uid, pud := range t.perUser {
			pud.delID = t.delID
			t.perUser[uid] = pud
		}
		// Broadcast the change to all, online and offline, exclude the session making the change.
		params := &presParams{delID: t.delID, delSeq: dr, actor: asUid.UserId()}
		filters := &presFilters{filterIn: types.ModeRead}
		t.presSubsOnline("del", params.actor, params, filters, sess.sid)
		t.presSubsOffline("del", params, filters, sess.sid, true)
	} else {
		pud := t.perUser[asUid]
		pud.delID = t.delID
		t.perUser[asUid] = pud

		// Notify user's other sessions
		t.presPubMessageDelete(asUid, t.delID, dr, sess.sid)
	}

	sess.queueOutWithOverrides(NoErrParams(del.Id, t.original(asUid), now, map[string]int{"del": t.delID}), sessOverrides)

	return nil
}

// Shut down the topic in response to {del what="topic"} request
// See detailed description at hub.topicUnreg()
// 1. Checks if the requester is the owner. If so:
// 1.2 Evict all sessions
// 1.3 Ask hub to unregister self
// 1.4 Exit the run() loop
// 2. If requester is not the owner:
// 2.1 If this is a p2p topic:
// 2.1.1 Check if the other subscription still exists, if so, treat request as {leave unreg=true}
// 2.1.2 If the other subscription does not exist, delete topic
// 2.2 If this is not a p2p topic, treat it as {leave unreg=true}
func (t *Topic) replyDelTopic(h *Hub, sess *Session, asUid types.Uid, del *MsgClientDel, sessOverrides *sessionOverrides) error {
	if t.owner != asUid {
		// Cases 2.1.1 and 2.2
		if t.cat != types.TopicCatP2P || t.subsCount() == 2 {
			return t.replyLeaveUnsub(h, sess, asUid, del.Id, sessOverrides)
		}
	}

	// Notifications are sent from the topic loop.

	return nil
}

// Delete credential
func (t *Topic) replyDelCred(h *Hub, sess *Session, asUid types.Uid, authLvl auth.Level, del *MsgClientDel, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if t.cat != types.TopicCatMe {
		sess.queueOutWithOverrides(ErrPermissionDenied(del.Id, t.original(asUid), now), sessOverrides)
		return errors.New("del.cred: invalid topic category")
	}
	if del.Cred == nil || del.Cred.Method == "" {
		sess.queueOutWithOverrides(ErrMalformed(del.Id, t.original(asUid), now), sessOverrides)
		return errors.New("del.cred: missing method")
	}

	tags, err := deleteCred(asUid, authLvl, del.Cred)
	if tags != nil {
		// Check if anything has been actually removed.
		_, removed := stringSliceDelta(t.tags, tags)
		if len(removed) > 0 {
			t.tags = tags
			t.presSubsOnline("tags", "", nilPresParams, nilPresFilters, "")
		}
	} else if err == nil {
		sess.queueOutWithOverrides(InfoNoAction(del.Id, del.Topic, now), sessOverrides)
		return nil
	}
	sess.queueOutWithOverrides(decodeStoreError(err, del.Id, del.Topic, now, nil), sessOverrides)
	return err
}

// Delete subscription
func (t *Topic) replyDelSub(h *Hub, sess *Session, asUid types.Uid, del *MsgClientDel, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	var err error

	// Get ID of the affected user
	uid := types.ParseUserId(del.User)

	pud := t.perUser[asUid]
	if !(pud.modeGiven & pud.modeWant).IsAdmin() {
		err = errors.New("del.sub: permission denied")
	} else if uid.IsZero() || uid == asUid {
		// Cannot delete self-subscription. User [leave unsub] or [delete topic]
		err = errors.New("del.sub: cannot delete self-subscription")
	} else if t.cat == types.TopicCatP2P {
		// Don't try to delete the other P2P user
		err = errors.New("del.sub: cannot apply to a P2P topic")
	}

	if err != nil {
		sess.queueOutWithOverrides(ErrPermissionDenied(del.Id, t.original(asUid), now), sessOverrides)
		return err
	}

	pud, ok := t.perUser[uid]
	if !ok {
		sess.queueOutWithOverrides(InfoNoAction(del.Id, t.original(asUid), now), sessOverrides)
		return errors.New("del.sub: user not found")
	}

	// Check if the user being ejected is the owner.
	if (pud.modeGiven & pud.modeWant).IsOwner() {
		err = errors.New("del.sub: cannot evict topic owner")
	} else if !pud.modeWant.IsJoiner() {
		// If the user has banned the topic, subscription should not be deleted. Otherwise user may be re-invited
		// which defeats the purpose of banning.
		err = errors.New("del.sub: cannot delete banned subscription")
	}

	if err != nil {
		sess.queueOutWithOverrides(ErrPermissionDenied(del.Id, t.original(asUid), now), sessOverrides)
		return err
	}

	// Delete user's subscription from the database
	if err := store.Subs.Delete(t.name, uid); err != nil {
		if err == types.ErrNotFound {
			sess.queueOutWithOverrides(InfoNoAction(del.Id, t.original(asUid), now), sessOverrides)
		} else {
			sess.queueOutWithOverrides(ErrUnknown(del.Id, t.original(asUid), now), sessOverrides)
			return err
		}
	} else {
		sess.queueOutWithOverrides(NoErr(del.Id, t.original(asUid), now), sessOverrides)
	}

	// Update cached unread count: negative value
	if (pud.modeWant & pud.modeGiven).IsReader() {
		usersUpdateUnread(uid, pud.readID-t.lastID, true)
	}

	// ModeUnset signifies deleted subscription as opposite to ModeNone - no access.
	t.notifySubChange(uid, asUid, pud.modeWant, pud.modeGiven, types.ModeUnset, types.ModeUnset, sidFromSessionOrOverrides(sess, sessOverrides))

	t.evictUser(uid, true, "")

	return nil
}

func (t *Topic) replyLeaveUnsub(h *Hub, sess *Session, asUid types.Uid, id string, sessOverrides *sessionOverrides) error {
	now := types.TimeNow()

	if t.owner == asUid {
		if id != "" {
			sess.queueOutWithOverrides(ErrPermissionDenied(id, t.original(asUid), now), sessOverrides)
		}
		return errors.New("replyLeaveUnsub: owner cannot unsubscribe")
	}

	// Delete user's subscription from the database.
	if err := store.Subs.Delete(t.name, asUid); err != nil {
		if err == types.ErrNotFound {
			if id != "" {
				sess.queueOutWithOverrides(InfoNoAction(id, t.original(asUid), now), sessOverrides)
			}
			err = nil
		} else if id != "" {
			sess.queueOutWithOverrides(ErrUnknown(id, t.original(asUid), now), sessOverrides)
		}

		return err
	}

	if id != "" {
		sess.queueOutWithOverrides(NoErr(id, t.original(asUid), now), sessOverrides)
	}

	pud := t.perUser[asUid]

	// Update cached unread count: negative value
	if (pud.modeWant & pud.modeGiven).IsReader() {
		usersUpdateUnread(asUid, pud.readID-t.lastID, true)
	}

	// Send notifications.
	skipSid := sidFromSessionOrOverrides(sess, sessOverrides)
	t.notifySubChange(asUid, asUid, pud.modeWant, pud.modeGiven, types.ModeUnset, types.ModeUnset, skipSid)
	// Evict all user's sessions, clear cached data, send notifications.
	t.evictUser(asUid, true, skipSid)

	return nil
}

// evictUser evicts all given user's sessions from the topic and clears user's cached data, if appropriate.
func (t *Topic) evictUser(uid types.Uid, unsub bool, skip string) {
	now := types.TimeNow()
	pud := t.perUser[uid]

	// Detach user from topic
	if unsub {
		if t.cat == types.TopicCatP2P {
			// P2P: mark user as deleted
			pud.online = 0
			pud.deleted = true
			t.perUser[uid] = pud
		} else {
			// Grp: delete per-user data
			delete(t.perUser, uid)
			t.computePerUserAcsUnion()

			usersRegisterUser(uid, false)
		}
	} else {
		// Clear online status
		pud.online = 0
		t.perUser[uid] = pud
	}

	// Detach all user's sessions
	msg := NoErrEvicted("", t.original(uid), now)
	msg.Ctrl.Params = map[string]interface{}{"unsub": unsub}
	msg.skipSid = skip
	msg.uid = uid
	for sess := range t.sessions {
		isProxy := sess.isProxy()
		if isProxy || t.remSession(sess, uid) != nil {
			if !isProxy {
				sess.detach <- t.name
			}
			if sess.sid != skip {
				sess.queueOut(msg)
			}
		}
	}
}

// User's subscription to a topic has changed, send presence notifications.
// 1. New subscription
// 2. Deleted subscription
// 3. Permissions changed
// Sending to
// (a) Topic admis online on topic itself.
// (b) Topic admins offline on 'me' if approval is needed.
// (c) If subscription is deleted, 'gone' to target.
// (d) 'off' to topic members online if deleted or muted.
// (e) To target user.
func (t *Topic) notifySubChange(uid, actor types.Uid,
	oldWant, oldGiven, newWant, newGiven types.AccessMode, skip string) {

	unsub := newWant == types.ModeUnset || newGiven == types.ModeUnset

	target := uid.UserId()

	dWant := types.ModeNone.String()
	if newWant.IsDefined() {
		if oldWant.IsDefined() && !oldWant.IsZero() {
			dWant = oldWant.Delta(newWant)
		} else {
			dWant = newWant.String()
		}
	}

	dGiven := types.ModeNone.String()
	if newGiven.IsDefined() {
		if oldGiven.IsDefined() && !oldGiven.IsZero() {
			dGiven = oldGiven.Delta(newGiven)
		} else {
			dGiven = newGiven.String()
		}
	}
	params := &presParams{
		target: target,
		actor:  actor.UserId(),
		dWant:  dWant,
		dGiven: dGiven}

	filter := &presFilters{
		filterIn:    types.ModeCSharer,
		excludeUser: target}

	// Announce the change in permissions to the admins who are online in the topic, exclude the target
	// and exclude the actor's session.
	t.presSubsOnline("acs", target, params, filter, skip)

	// If it's a new subscription or if the user asked for permissions in excess of what was granted,
	// announce the request to topic admins on 'me' so they can approve the request. The notification
	// is not sent to the target user or the actor's session.
	if newWant.BetterThan(newGiven) || oldWant == types.ModeNone {
		t.presSubsOffline("acs", params, filter, skip, true)
	}

	// Handling of muting/unmuting.
	// Case A: subscription deleted.
	// Case B: subscription muted only.
	if unsub {
		// Subscription deleted
		// In case of a P2P topic subscribe/unsubscribe users from each other's notifications.
		if t.cat == types.TopicCatP2P {
			uid2 := t.p2pOtherUser(uid)
			// Remove user1's subscription to user2 and notify user1's other sessions that he is gone.
			t.presSingleUserOffline(uid, "gone", nilPresParams, skip, false)
			// Tell user2 that user1 is offline but let him keep sending updates in case user1 resubscribes.
			presSingleUserOfflineOffline(uid2, target, "off", nilPresParams, "")
		} else if t.cat == types.TopicCatGrp {
			// Notify all sharers that the user is offline now.
			t.presSubsOnline("off", uid.UserId(), nilPresParams, filter, skip)
		}
	} else if !(newWant & newGiven).IsPresencer() && (oldWant & oldGiven).IsPresencer() {
		// Subscription just muted.
		var source string
		if t.cat == types.TopicCatP2P {
			source = t.p2pOtherUser(uid).UserId()
		} else if t.cat == types.TopicCatGrp {
			source = t.name
		}
		if source != "" {
			// Tell user1 to start discarding updates from muted topic/user.
			presSingleUserOfflineOffline(uid, source, "off+dis", nilPresParams, "")
		}

	} else if (newWant & newGiven).IsPresencer() && !(oldWant & oldGiven).IsPresencer() {
		// Subscription un-muted.

		// Notify subscriber of topic's online status.
		if t.cat == types.TopicCatGrp {
			t.presSingleUserOffline(uid, "?unkn+en", nilPresParams, "", false)
		} else if t.cat == types.TopicCatMe {
			// User is visible online now, notify subscribers.
			t.presUsersOfInterest("on+en", t.userAgent)
		}
	}

	// Notify target that permissions have changed.
	if !unsub {
		// Notify sessions online in the topic.
		t.presSubsOnlineDirect("acs", params, &presFilters{singleUser: target}, skip)
		// Notify target's other sessions on 'me'.
		t.presSingleUserOffline(uid, "acs", params, skip, true)
	}
}

// Prepares a payload to be delivered to a mobile device as a push notification in response to a {data} message.
func (t *Topic) pushForData(fromUid types.Uid, data *MsgServerData) *push.Receipt {
	// The `Topic` in the push receipt is `t.xoriginal` for group topics, `fromUid` for p2p topics,
	// not the t.original(fromUid) because it's the topic name as seen by the recipient, not by the sender.
	topic := t.xoriginal
	if t.cat == types.TopicCatP2P {
		topic = fromUid.UserId()
	}

	// Initialize the push receipt.
	contentType, _ := data.Head["mime"].(string)
	receipt := push.Receipt{
		To: make(map[types.Uid]push.Recipient, t.subsCount()),
		Payload: push.Payload{
			What:        push.ActMsg,
			Silent:      false,
			Topic:       topic,
			From:        data.From,
			Timestamp:   data.Timestamp,
			SeqId:       data.SeqId,
			ContentType: contentType,
			Content:     data.Content}}

	for uid, pud := range t.perUser {
		// Send only to those who have notifications enabled, exclude the originating user.
		if uid == fromUid {
			continue
		}
		mode := pud.modeWant & pud.modeGiven
		if mode.IsPresencer() && mode.IsReader() && !pud.deleted {
			receipt.To[uid] = push.Recipient{
				// Number of sessions this data message will be delivered to.
				// Push notifications sent to users with non-zero online sessions will be marked silent.
				Delivered: pud.online,
			}
		}
	}
	if len(receipt.To) > 0 {
		return &receipt
	}
	// If there are no recipient there is no need to send the push notification.
	return nil
}

// Prepares payload to be delivered to a mobile device as a push notification in response to a new subscription.
func (t *Topic) pushForSub(fromUid, toUid types.Uid, want, given types.AccessMode, now time.Time) *push.Receipt {
	// The `Topic` in the push receipt is `t.xoriginal` for group topics, `fromUid` for p2p topics,
	// not the t.original(fromUid) because it's the topic name as seen by the recipient, not by the sender.
	topic := t.xoriginal
	if t.cat == types.TopicCatP2P {
		topic = fromUid.UserId()
	}

	// Initialize the push receipt.
	receipt := push.Receipt{
		To: make(map[types.Uid]push.Recipient, t.subsCount()),
		Payload: push.Payload{
			What:      push.ActSub,
			Silent:    false,
			Topic:     topic,
			From:      fromUid.UserId(),
			Timestamp: now,
			SeqId:     t.lastID,
			ModeWant:  want,
			ModeGiven: given}}

	receipt.To[toUid] = push.Recipient{}

	return &receipt
}

func (t *Topic) mostRecentSession() *Session {
	var sess *Session
	var latest time.Time
	for s := range t.sessions {
		if s.lastAction.After(latest) {
			sess = s
			latest = s.lastAction
		}
	}
	return sess
}

const (
	// Topic is fully initialized.
	topicStatusLoaded = 0x1
	// Topic is paused: all packets are rejected.
	topicStatusPaused = 0x2

	// Topic is in the process of being deleted. This is irrecoverable.
	topicStatusMarkedDeleted = 0x10
	// Topic is suspended: read-only mode.
	topicStatusReadOnly = 0x20
)

// statusChangeBits sets or removes given bits from t.status
func (t *Topic) statusChangeBits(bits int32, set bool) {
	for {
		oldStatus := atomic.LoadInt32((*int32)(&t.status))
		newStatus := oldStatus
		if set {
			newStatus = newStatus | bits
		} else {
			newStatus = newStatus & ^bits
		}
		if newStatus == oldStatus {
			break
		}
		if atomic.CompareAndSwapInt32((*int32)(&t.status), oldStatus, newStatus) {
			break
		}
	}
}

// markLoaded indicates that topic subscribers have been loaded into memory.
func (t *Topic) markLoaded() {
	t.statusChangeBits(topicStatusLoaded, true)
}

// markPaused pauses or unpauses the topic. When the topic is paused all
// messages are rejected.
func (t *Topic) markPaused(pause bool) {
	t.statusChangeBits(topicStatusPaused, pause)
}

// markDeleted marks topic as being deleted.
func (t *Topic) markDeleted() {
	t.statusChangeBits(topicStatusMarkedDeleted, true)
}

// markReadOnly suspends/un-suspends the topic: adds or removes the 'read-only' flag.
func (t *Topic) markReadOnly(readOnly bool) {
	t.statusChangeBits(topicStatusReadOnly, readOnly)
}

// isInactive checks if topic is paused or being deleted.
func (t *Topic) isInactive() bool {
	return (atomic.LoadInt32((*int32)(&t.status)) & (topicStatusPaused | topicStatusMarkedDeleted)) != 0
}

func (t *Topic) isReadOnly() bool {
	return (atomic.LoadInt32((*int32)(&t.status)) & topicStatusReadOnly) != 0
}

func (t *Topic) isLoaded() bool {
	return (atomic.LoadInt32((*int32)(&t.status)) & topicStatusLoaded) != 0
}

func (t *Topic) isDeleted() bool {
	return (atomic.LoadInt32((*int32)(&t.status)) & topicStatusMarkedDeleted) != 0
}

// Get topic name suitable for the given client
func (t *Topic) original(uid types.Uid) string {
	if t.cat != types.TopicCatP2P {
		return t.xoriginal
	}

	if pud, ok := t.perUser[uid]; ok {
		return pud.topicName
	}

	panic("Invalid P2P topic")
}

// Get ID of the other user in a P2P topic
func (t *Topic) p2pOtherUser(uid types.Uid) types.Uid {
	if t.cat == types.TopicCatP2P {
		// Try to find user in subscribers.
		for u2 := range t.perUser {
			if u2.Compare(uid) != 0 {
				return u2
			}
		}
	}

	// Even when one user is deleted, the subscription must be restored
	// before p2pOtherUser is called.
	panic("Not a valid P2P topic")
}

// Get per-session value of fnd.Public
func (t *Topic) fndGetPublic(sess *Session) interface{} {
	if t.cat == types.TopicCatFnd {
		if t.public == nil {
			return nil
		}
		if pubmap, ok := t.public.(map[string]interface{}); ok {
			return pubmap[sess.sid]
		}
		panic("Invalid Fnd.Public type")
	}
	panic("Not Fnd topic")
}

// Assign per-session fnd.Public. Returns true if value has been changed.
func (t *Topic) fndSetPublic(sess *Session, public interface{}) bool {
	if t.cat != types.TopicCatFnd {
		panic("Not Fnd topic")
	}

	var pubmap map[string]interface{}
	var ok bool
	if t.public != nil {
		if pubmap, ok = t.public.(map[string]interface{}); !ok {
			// This could only happen if fnd.public is assigned outside of this function.
			panic("Invalid Fnd.Public type")
		}
	}
	if pubmap == nil {
		pubmap = make(map[string]interface{})
	}

	if public != nil {
		pubmap[sess.sid] = public
	} else {
		ok = (pubmap[sess.sid] != nil)
		delete(pubmap, sess.sid)
		if len(pubmap) == 0 {
			pubmap = nil
		}
	}
	t.public = pubmap
	return ok

}

// Remove per-session value of fnd.Public
func (t *Topic) fndRemovePublic(sess *Session) {
	if t.cat == types.TopicCatFnd {
		if t.public == nil {
			return
		}
		if pubmap, ok := t.public.(map[string]interface{}); ok {
			delete(pubmap, sess.sid)
			return
		}
		panic("Invalid Fnd.Public type")
	}
	panic("Not Fnd topic")
}

func (t *Topic) accessFor(authLvl auth.Level) types.AccessMode {
	return selectAccessMode(authLvl, t.accessAnon, t.accessAuth, getDefaultAccess(t.cat, true))
}

// subsCount returns the number of topic subsribers
func (t *Topic) subsCount() int {
	if t.cat == types.TopicCatP2P {
		count := 0
		for uid := range t.perUser {
			if !t.perUser[uid].deleted {
				count++
			}
		}
		return count
	}
	return len(t.perUser)
}

// Add session record. 'user' may be different from s.uid.
func (t *Topic) addSession(s *Session, asUid types.Uid) bool {
	if _, ok := t.sessions[s]; ok {
		return false
	}
	t.sessions[s] = perSessionData{uid: asUid}
	return true
}

// Removes session record if 'doRemove' is true and either:
// * 'asUid' matches subscribed user.
// * or 's' is a proxy session for a proxy-master topic connection.
func (t *Topic) maybeRemoveSession(s *Session, asUid types.Uid, doRemove bool) *perSessionData {
	if pssd, ok := t.sessions[s]; ok && (s.isProxy() || pssd.uid == asUid || asUid.IsZero()) {
		if doRemove {
			// Check for deferred presence notification and cancel it if found.
			if pssd.ref != nil {
				t.defrNotif.Remove(pssd.ref)
			}
			delete(t.sessions, s)
		}
		return &pssd
	}
	return nil
}

// Removes session record if 'asUid' matches subscribed user.
func (t *Topic) remSession(s *Session, asUid types.Uid) *perSessionData {
	return t.maybeRemoveSession(s, asUid, true)
}

func (t *Topic) isOnline() bool {
	// Some sessions may be background sessions. They should not be counted.
	for _, pssd := range t.sessions {
		// At least one non-background session.
		if pssd.ref == nil {
			return true
		}
	}
	return false
}

func topicCat(name string) types.TopicCat {
	return types.GetTopicCat(name)
}

// Generate random string as a name of the group topic
func genTopicName() string {
	return "grp" + store.GetUidString()
}
