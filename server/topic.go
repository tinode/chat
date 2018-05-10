/******************************************************************************
 *
 *  Description :
 *    An isolated communication channel (chat room, 1:1 conversation) for
 *    usually multiple users. There is no communication across topics.
 *
 *****************************************************************************/

package main

import (
	"errors"
	"log"
	"sort"
	"sync/atomic"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/push"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

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
	isProxy bool

	// Time when the topic was first created
	created time.Time
	// Time when the topic was last updated
	updated time.Time

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
	// User's contact list (not nil for 'me' topic only).
	// The map keys are UserIds for P2P topics and grpXXX for group topics.
	perSubs map[string]perSubsData

	// Sessions attached to this topic
	sessions map[*Session]bool

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
	// Flag which tells topic to stop acception requests: hub is in the process of shutting it down
	suspended atomicBool
}

type atomicBool int32

// perUserData holds topic's cache of per-subscriber data
type perUserData struct {
	// Timestamps when the subscription was created and updated
	created time.Time
	updated time.Time

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
}

// perSubsData holds user's (on 'me' topic) cache of subscription data
type perSubsData struct {
	// The other user is online.
	online bool
	// True if we care about the updates from the other user. False otherwise.
	enabled bool
}

// Session wants to leave the topic
type sessionLeave struct {
	// Session which initiated the request
	sess *Session
	// Leave and unsubscribe
	unsub bool
	// Topic to report success of failure on
	topic string
	// ID of originating request, if any
	reqID string
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

type pushReceipt struct {
	rcpt   *push.Receipt
	uidMap map[types.Uid]int
}

var nilPresParams = &presParams{}
var nilPresFilters = &presFilters{}

func (t *Topic) run(hub *Hub) {

	log.Printf("Topic started: '%s'", t.name)

	// TODO(gene): read keepalive value from the command line
	keepAlive := idleTopicTimeout
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	// 'me' only
	var uaTimer *time.Timer
	var currentUA string
	uaTimer = time.NewTimer(time.Minute)
	uaTimer.Stop()

	for {
		select {
		case sreg := <-t.reg:
			// Request to add a connection to this topic

			if t.isSuspended() {
				sreg.sess.queueOut(ErrLocked(sreg.pkt.Id, t.original(sreg.sess.uid), types.TimeNow()))
			} else {
				// The topic is alive, so stop the kill timer, if it's ticking. We don't want the topic to die
				// while processing the call
				killTimer.Stop()
				if err := t.handleSubscription(hub, sreg); err == nil {
					if sreg.created {
						// Call plugins with the new topic
						pluginTopic(t, plgActCreate)
					}

					// give a broadcast channel to the connection (.read)
					// give channel to use when shutting down (.done)
					sreg.sess.subs[t.name] = &Subscription{
						broadcast: t.broadcast,
						done:      t.unreg,
						meta:      t.meta,
						uaChange:  t.uaChange}

					t.sessions[sreg.sess] = true

				} else {
					if len(t.sessions) == 0 {
						// Failed to subscribe, the topic is still inactive
						killTimer.Reset(keepAlive)
					}
					log.Printf("topic[%s] subscription failed %v", t.name, err)
				}
			}

		case leave := <-t.unreg:
			// Remove connection from topic; session may continue to function
			now := types.TimeNow()

			if t.isSuspended() {
				leave.sess.queueOut(ErrLocked(leave.reqID, t.original(leave.sess.uid), now))
				continue

			} else if leave.unsub {
				// User wants to leave and unsubscribe.
				if err := t.replyLeaveUnsub(hub, leave.sess, leave.reqID); err != nil {
					log.Println("failed to unsub", err)
					continue
				}

			} else {
				// Just leaving the topic without unsubscribing
				delete(t.sessions, leave.sess)

				pud := t.perUser[leave.sess.uid]
				pud.online--
				if t.cat == types.TopicCatMe {
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
					if err := store.Users.UpdateLastSeen(mrs.uid, mrs.userAgent, now); err != nil {
						log.Println(err)
					}
				} else if t.cat == types.TopicCatGrp && pud.online == 0 {
					// User is going offline: notify online subscribers on 'me'
					t.presSubsOnline("off", leave.sess.uid.UserId(), nilPresParams,
						&presFilters{filterIn: types.ModeRead}, "")
				}

				t.perUser[leave.sess.uid] = pud

				if leave.reqID != "" {
					leave.sess.queueOut(NoErr(leave.reqID, t.original(leave.sess.uid), now))
				}
			}

			// If there are no more subscriptions to this topic, start a kill timer
			if len(t.sessions) == 0 {
				killTimer.Reset(keepAlive)
			}

		case msg := <-t.broadcast:
			// Content message intended for broadcasting to recipients

			var pushRcpt *pushReceipt

			if msg.Data != nil {
				if t.isSuspended() {
					if msg.sessFrom != nil {
						msg.sessFrom.queueOut(ErrLocked(msg.id, t.original(msg.sessFrom.uid), msg.timestamp))
					}
					continue
				}

				from := types.ParseUserId(msg.Data.From)
				userData := t.perUser[from]

				// msg.sessFrom is not nil when the message originated at the client.
				// for internally generated messages the akn is nil
				if msg.sessFrom != nil {
					if !(userData.modeWant & userData.modeGiven).IsWriter() {
						msg.sessFrom.queueOut(ErrPermissionDenied(msg.id, t.original(msg.sessFrom.uid),
							msg.timestamp))
						continue
					}
				}

				if err := store.Messages.Save(&types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: msg.Data.Timestamp},
					SeqId:     t.lastID + 1,
					Topic:     t.name,
					From:      from.String(),
					Head:      msg.Data.Head,
					Content:   msg.Data.Content}); err != nil {

					log.Printf("topic[%s]: failed to save message: %v", t.name, err)
					msg.sessFrom.queueOut(ErrUnknown(msg.id, t.original(msg.sessFrom.uid), msg.timestamp))

					continue
				}

				t.lastID++
				msg.Data.SeqId = t.lastID

				if msg.id != "" {
					reply := NoErrAccepted(msg.id, t.original(msg.sessFrom.uid), msg.timestamp)
					reply.Ctrl.Params = map[string]int{"seq": t.lastID}
					msg.sessFrom.queueOut(reply)
				}

				pushRcpt = t.makePushReceipt(msg.Data)

				// Message sent: notify offline 'R' subscrbers on 'me'
				t.presSubsOffline("msg", &presParams{seqID: t.lastID},
					&presFilters{filterIn: types.ModeRead}, "", true)

				// Tell the plugins that a message was accepted for delivery
				pluginMessage(msg.Data, plgActCreate)

			} else if msg.Pres != nil {

				what := t.presProcReq(msg.Pres.Src, msg.Pres.What, msg.Pres.wantReply)
				if t.xoriginal != msg.Pres.Topic || what == "" {
					// This is just a request for status, don't forward it to sessions
					continue
				}

				// "what" may have changed, i.e. unset or "+command" removed ("on+en" -> "on")
				msg.Pres.What = what
			} else if msg.Info != nil {
				if t.isSuspended() {
					// Ignore info messages - topic is being deleted
					continue
				}

				if msg.Info.SeqId > t.lastID {
					// Drop bogus read notification
					continue
				}

				uid := types.ParseUserId(msg.Info.From)
				pud := t.perUser[uid]

				// Filter out "kp" from users with no 'W' permission (or people without a subscription)
				if msg.Info.What == "kp" && !(pud.modeGiven & pud.modeWant).IsWriter() {
					continue
				}

				if msg.Info.What == "read" || msg.Info.What == "recv" {
					// Filter out "read/recv" from users with no 'R' permission (or people without a subscription)
					if !(pud.modeGiven & pud.modeWant).IsReader() {
						continue
					}

					var read, recv int
					if msg.Info.What == "read" {
						if msg.Info.SeqId > pud.readID {
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

					if err := store.Subs.Update(t.name, uid,
						map[string]interface{}{
							"RecvSeqId": pud.recvID,
							"ReadSeqId": pud.readID},
						false); err != nil {

						log.Printf("topic[%s]: failed to update SeqRead/Recv counter: %v", t.name, err)
						continue
					}

					// Read/recv updated: notify user's other sessions of the change
					t.presPubMessageCount(uid, recv, read, msg.skipSid)

					t.perUser[uid] = pud
				}
			}

			// Broadcast the message. Only {data}, {pres}, {info} are broadcastable.
			// {meta} and {ctrl} are sent to the session only
			if msg.Data != nil || msg.Pres != nil || msg.Info != nil {
				for sess := range t.sessions {
					if sess.sid == msg.skipSid {
						continue
					}

					if msg.Pres != nil {
						// Skip notifying - already notified on topic.
						if msg.Pres.skipTopic != "" && sess.subs[msg.Pres.skipTopic] != nil {
							continue
						}

						// Notification addressed to a single user only
						if msg.Pres.singleUser != "" && sess.uid.UserId() != msg.Pres.singleUser {
							continue
						}

						// Check presence filters
						pud, _ := t.perUser[sess.uid]
						if !(pud.modeGiven & pud.modeWant).IsPresencer() ||
							(msg.Pres.filterIn != 0 && int(pud.modeGiven&pud.modeWant)&msg.Pres.filterIn == 0) ||
							(msg.Pres.filterOut != 0 && int(pud.modeGiven&pud.modeWant)&msg.Pres.filterOut != 0) {
							continue
						}
					} else {
						// Check if the user has Read permission
						pud, _ := t.perUser[sess.uid]
						if !(pud.modeGiven & pud.modeWant).IsReader() {
							continue
						}
					}

					if t.cat == types.TopicCatP2P {
						// For p2p topics topic name is dependent on receiver
						if msg.Data != nil {
							msg.Data.Topic = t.original(sess.uid)
						} else if msg.Pres != nil {
							msg.Pres.Topic = t.original(sess.uid)
						} else if msg.Info != nil {
							msg.Info.Topic = t.original(sess.uid)
						}
					}

					if sess.queueOut(msg) {
						// Update device map with the device ID which should NOT receive the notification.
						if pushRcpt != nil {
							if i, ok := pushRcpt.uidMap[sess.uid]; ok {
								pushRcpt.rcpt.To[i].Delivered++
								if sess.deviceID != "" {
									// List of device IDs which already received the message. Push should
									// skip them.
									pushRcpt.rcpt.To[i].Devices = append(pushRcpt.rcpt.To[i].Devices, sess.deviceID)
								}
							}
						}
					} else {
						log.Printf("topic[%s]: connection stuck, detaching", t.name)
						t.unreg <- &sessionLeave{sess: sess, unsub: false}
					}
				}

				if pushRcpt != nil {
					push.Push(pushRcpt.rcpt)
				}

			} else {
				// TODO(gene): remove this
				log.Panic("topic: wrong message type for broadcasting", t.name)
			}

		case meta := <-t.meta:
			// log.Printf("topic[%s]: got meta message '%#+v' %x", t.name, meta, meta.what)

			// Request to get/set topic metadata
			if meta.pkt.Get != nil {
				// Get request
				if meta.what&constMsgMetaDesc != 0 {
					if err := t.replyGetDesc(meta.sess, meta.pkt.Get.Id, "", meta.pkt.Get.Desc); err != nil {
						log.Printf("topic[%s] meta.Get.Desc failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaSub != 0 {
					if err := t.replyGetSub(meta.sess, meta.pkt.Get.Id, meta.pkt.Get.Sub); err != nil {
						log.Printf("topic[%s] meta.Get.Sub failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaData != 0 {
					if err := t.replyGetData(meta.sess, meta.pkt.Get.Id, meta.pkt.Get.Data); err != nil {
						log.Printf("topic[%s] meta.Get.Data failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaDel != 0 {
					if err := t.replyGetDel(meta.sess, meta.pkt.Get.Id, meta.pkt.Get.Del); err != nil {
						log.Printf("topic[%s] meta.Get.Del failed: %v", t.name, err)
					}
				}

			} else if meta.pkt.Set != nil {
				// Set request
				if meta.what&constMsgMetaDesc != 0 {
					if err := t.replySetDesc(meta.sess, meta.pkt.Set); err == nil {
						// Notify plugins of the update
						pluginTopic(t, plgActUpd)
					} else {
						log.Printf("topic[%s] meta.Set.Desc failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaSub != 0 {
					if err := t.replySetSub(hub, meta.sess, meta.pkt.Set); err != nil {
						log.Printf("topic[%s] meta.Set.Sub failed: %v", t.name, err)
					}
				}
				if meta.what&constMsgMetaTags != 0 {
					if err := t.replySetTags(meta.sess, meta.pkt.Set); err != nil {
						log.Printf("topic[%s] meta.Set.Tags failed: %v", t.name, err)
					}
				}

			} else if meta.pkt.Del != nil {
				// Del request
				var err error
				switch meta.what {
				case constMsgDelMsg:
					err = t.replyDelMsg(meta.sess, meta.pkt.Del)
				case constMsgDelSub:
					err = t.replyDelSub(hub, meta.sess, meta.pkt.Del)
				case constMsgDelTopic:
					err = t.replyDelTopic(hub, meta.sess, meta.pkt.Del)
				}

				if err != nil {
					log.Printf("topic[%s] meta.Del failed: %v", t.name, err)
				}
			}
		case ua := <-t.uaChange:
			// process an update to user agent from one of the sessions
			currentUA = ua
			uaTimer.Reset(uaTimerDelay)

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
			if t.cat == types.TopicCatMe {
				uaTimer.Stop()
				t.presUsersOfInterest("off", currentUA)
			} else if t.cat == types.TopicCatGrp {
				t.presSubsOffline("off", nilPresParams, nilPresFilters, "", false)
			}
			return

		case sd := <-t.exit:
			// Handle four cases:
			// 1. Topic is shutting down by timer due to inactivity (reason == StopNone)
			// 2. Topic is being deleted (reason == StopDeleted)
			// 3. System shutdown (reason == StopShutdown, done != nil).
			// 4. Cluster rehashing (reason == StopRehashing)
			// TODO(gene): save lastMessage value;

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
				t.presSubsOnlineDirect("term")
			}

			// In case of a system shutdown don't bother with notifications. They won't be delivered anyway.

			// Report completion back to sender, if 'done' is not nil.
			if sd.done != nil {
				sd.done <- true
			}
			return
		}
	}
}

// Session subscribed to a topic, created == true if topic was just created and {pres} needs to be announced
func (t *Topic) handleSubscription(h *Hub, sreg *sessionJoin) error {
	var getWhat = 0
	if sreg.pkt.Get != nil {
		getWhat = parseMsgClientMeta(sreg.pkt.Get.What)
	}

	if err := t.subCommonReply(h, sreg, (getWhat&constMsgMetaDesc != 0)); err != nil {
		return err
	}

	pud := t.perUser[sreg.sess.uid]
	if sreg.loaded {
		// Notify user's contact that the given user is online now.
		if t.cat == types.TopicCatMe {
			if err := t.loadContacts(sreg.sess.uid); err != nil {
				log.Println("topic: failed to load contacts", t.name, err.Error())
			}
			// User online: notify users of interest
			t.presUsersOfInterest("on", sreg.sess.userAgent)
		} else if t.cat == types.TopicCatGrp || t.cat == types.TopicCatP2P {
			var enable string
			if sreg.created {
				// Notify creator's other sessions that the topic was created.
				t.presSingleUserOffline(sreg.sess.uid, "acs",
					&presParams{
						dWant:  pud.modeWant.String(),
						dGiven: pud.modeGiven.String(),
						actor:  "me"},
					sreg.sess.sid, false)

				// Special handling of a P2P topic - notifying the other
				// participant.
				if t.cat == types.TopicCatP2P {
					user2 := t.p2pOtherUser(sreg.sess.uid)
					pud2 := t.perUser[user2]

					// Inform the other user that the topic was just created
					t.presSingleUserOffline(user2, "acs", &presParams{
						dWant:  pud2.modeWant.String(),
						dGiven: pud2.modeGiven.String(),
						actor:  sreg.sess.uid.UserId()}, "", false)

					// Notify current user's 'me' topic to accept notifications from user2
					t.presSingleUserOffline(sreg.sess.uid, "?none+en", nilPresParams, "", false)

					// Initiate exchange of 'online' status with the other user.
					// We don't know if the current user is online in the 'me' topic,
					// so sending an '?unkn' status to user2. His 'me' topic
					// will reply with user2's status and request an actual status from user1.
					if (pud2.modeGiven & pud2.modeWant).IsPresencer() {
						// If user2 should receive notifications, enable it.
						enable = "+en"
					}
					t.presSingleUserOffline(user2, "?unkn"+enable, nilPresParams, "", false)

				} else if t.cat == types.TopicCatGrp {
					// Enable notifications for a new topic, if appropriate
					if (pud.modeGiven & pud.modeWant).IsPresencer() {
						enable = "+en"
					}
				}
			}

			if t.cat == types.TopicCatGrp {
				// Notify topic subscribers that the topic is online now.
				t.presSubsOffline("on"+enable, nilPresParams, nilPresFilters, "", false)
			}
		}
	} else if t.cat == types.TopicCatGrp && pud.online == 1 {
		// User just joined. Notify other group members
		t.presSubsOnline("on", sreg.sess.uid.UserId(), nilPresParams,
			&presFilters{filterIn: types.ModeRead}, sreg.sess.sid)
	}

	if getWhat&constMsgMetaSub != 0 {
		// Send get.sub response as a separate {meta} packet
		if err := t.replyGetSub(sreg.sess, sreg.pkt.Id, sreg.pkt.Get.Sub); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Sub failed: %v", t.name, err)
		}
	}

	if getWhat&constMsgMetaTags != 0 {
		// Send get.tags response as a separate {meta} packet
		if err := t.replyGetTags(sreg.sess, sreg.pkt.Id); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Tags failed: %v", t.name, err)
		}
	}

	if getWhat&constMsgMetaData != 0 {
		// Send get.data response as {data} packets
		if err := t.replyGetData(sreg.sess, sreg.pkt.Id, sreg.pkt.Get.Data); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Data failed: %v", t.name, err)
		}
	}

	if getWhat&constMsgMetaDel != 0 {
		// Send get.del response as a separate {meta} packet
		if err := t.replyGetDel(sreg.sess, sreg.pkt.Id, sreg.pkt.Get.Del); err != nil {
			log.Printf("topic[%s] handleSubscription Get.Del failed: %v", t.name, err)
		}
	}
	return nil
}

// subCommonReply generates a response to a subscription request
func (t *Topic) subCommonReply(h *Hub, sreg *sessionJoin, sendDesc bool) error {
	var now time.Time
	// For newly created topics report topic creation time.
	if sreg.created {
		now = t.updated
	} else {
		now = types.TimeNow()
	}

	// The topic is already initialized by the Hub

	var private interface{}
	var mode string

	if sreg.pkt.Set != nil {
		if sreg.pkt.Set.Sub != nil {
			if sreg.pkt.Set.Sub.User != "" {
				sreg.sess.queueOut(ErrMalformed(sreg.pkt.Id, t.original(sreg.sess.uid), now))
				return errors.New("user id must not be specified")
			}

			mode = sreg.pkt.Set.Sub.Mode
		}

		if sreg.pkt.Set.Desc != nil && !isNullValue(sreg.pkt.Set.Desc.Private) {
			private = sreg.pkt.Set.Desc.Private
		}
	}

	// Create new subscription or modify an existing one.
	if err := t.requestSub(h, sreg.sess, sreg.pkt.Id, mode, private); err != nil {
		return err
	}

	pud := t.perUser[sreg.sess.uid]
	pud.online++
	t.perUser[sreg.sess.uid] = pud

	resp := NoErr(sreg.pkt.Id, t.original(sreg.sess.uid), now)
	// Report access mode.
	resp.Ctrl.Params = map[string]MsgAccessMode{"acs": {
		Given: pud.modeGiven.String(),
		Want:  pud.modeWant.String(),
		Mode:  (pud.modeGiven & pud.modeWant).String()}}
	sreg.sess.queueOut(resp)

	if sendDesc {
		var tmpName string
		if sreg.created {
			tmpName = sreg.pkt.Topic
		}
		if err := t.replyGetDesc(sreg.sess, sreg.pkt.Id, tmpName, sreg.pkt.Get.Desc); err != nil {
			log.Printf("topic[%s] subCommonReply Get.Desc failed: %v", t.name, err)
		}
	}

	return nil
}

// User requests or updates a self-subscription to a topic. Called as a
// result of {sub} or {meta set=sub}.
//
//	h 		- hub
//	sess 	- originating session
//  pktId 	- originating packet Id
//	want	- requested access mode
//	info 	- explanation info given by the requester
//	private	- private value to assign to the subscription
// Handle these cases:
// A. User is trying to subscribe for the first time (no subscription)
// B. User is already subscribed, just joining without changing anything
// C. User is responding to an earlier invite (modeWant was "N" in subscription)
// D. User is already subscribed, changing modeWant
// E. User is accepting ownership transfer (requesting ownership transfer is not permitted)
func (t *Topic) requestSub(h *Hub, sess *Session, pktID string, want string,
	private interface{}) error {

	now := types.TimeNow()

	// Access mode values as they were before this request was processed.
	oldWant := types.ModeNone
	oldGiven := types.ModeNone

	// Parse access mode requested by the user
	modeWant := types.ModeUnset
	if want != "" {
		if err := modeWant.UnmarshalText([]byte(want)); err != nil {
			sess.queueOut(ErrMalformed(pktID, t.original(sess.uid), now))
			return err
		}
	}

	// Check if it's an attempt at a new subscription to the topic.
	// It could be an actual subscription (IsJoiner() == true) or a ban (IsJoiner() == false)
	userData, existingSub := t.perUser[sess.uid]
	if !existingSub {

		// Check if the max number of subscriptions is already reached.
		if t.cat == types.TopicCatGrp && len(t.perUser) >= globals.maxSubscriberCount {
			sess.queueOut(ErrPolicy(pktID, t.original(sess.uid), now))
			return errors.New("max subscription count exceeded")
		}

		userData.private = private

		if t.cat == types.TopicCatP2P {
			// If it's a re-subscription to a p2p topic, set public and permissions

			// t.perUser contains just one element - the other user
			for uid2, user2Data := range t.perUser {
				if user2, err := store.Users.Get(uid2); err != nil {
					sess.queueOut(ErrUnknown(pktID, t.original(sess.uid), now))
					return err
				} else if user2 == nil {
					sess.queueOut(ErrUserNotFound(pktID, t.original(sess.uid), now))
					return errors.New("user not found")
				} else {
					userData.public = user2.Public
					userData.topicName = uid2.UserId()
					userData.modeGiven = selectAccessMode(sess.authLvl,
						user2.Access.Anon, user2.Access.Auth, types.ModeCP2P)
					if modeWant == types.ModeUnset {
						// By default give user1 the same thing user1 gave to user2.
						userData.modeWant = user2Data.modeGiven
					} else {
						userData.modeWant = modeWant
					}
				}
				break
			}

			// Make sure the user is not asking for unreasonable permissions
			userData.modeWant = (userData.modeWant & types.ModeCP2P) | types.ModeApprove
		} else {
			// For non-p2p2 topics access is given as default access
			userData.modeGiven = t.accessFor(sess.authLvl)

			if modeWant == types.ModeUnset {
				// User wants default access mode.
				userData.modeWant = t.accessFor(sess.authLvl)
			} else {
				userData.modeWant = modeWant
			}
		}

		// Add subscription to database
		sub := &types.Subscription{
			User:      sess.uid.String(),
			Topic:     t.name,
			ModeWant:  userData.modeWant,
			ModeGiven: userData.modeGiven,
			Private:   userData.private,
		}

		if err := store.Subs.Create(sub); err != nil {
			sess.queueOut(ErrUnknown(pktID, t.original(sess.uid), now))
			return err
		}

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
				if t.owner == sess.uid && !modeWant.IsOwner() {
					sess.queueOut(ErrPermissionDenied(pktID, t.original(sess.uid), now))
					return errors.New("cannot unset ownership")
				}

				// Ownership transfer
				ownerChange = modeWant.IsOwner() && !userData.modeWant.IsOwner()

				// The owner should be able to grant himself any access permissions
				// If ownership transfer is rejected don't upgrade
				if modeWant.IsOwner() && !userData.modeGiven.BetterEqual(modeWant) {
					userData.modeGiven |= modeWant
				}
			} else if modeWant.IsOwner() {
				// Ownership transfer can only be initiated by the owner
				sess.queueOut(ErrPermissionDenied(pktID, t.original(sess.uid), now))
				return errors.New("non-owner cannot request ownership transfer")
			} else if t.cat == types.TopicCatP2P {
				// For P2P topics ignore requests for 'D'. Otherwise it will generate a useless announcement
				modeWant = (modeWant & types.ModeCP2P) | types.ModeApprove
			} else if userData.modeGiven.IsAdmin() && modeWant.IsAdmin() {
				// The Admin should be able to grant any permissions except ownership (checked previously) &
				// hard-deleting messages.
				if !userData.modeGiven.BetterEqual(modeWant & ^types.ModeDelete) {
					userData.modeGiven |= (modeWant & ^types.ModeDelete)
				}
			}
		}

		// If user has not requested a new access mode, provide one by default.
		if modeWant == types.ModeUnset {
			// If the user has self-banned before, un-self-ban. Otherwise do not make a change.
			if !userData.modeWant.IsJoiner() {
				// Set permissions NO WORSE than default, but possibly better (admin or owner banned himself).
				userData.modeWant = userData.modeGiven | t.accessFor(sess.authLvl)
			}
		} else if userData.modeWant != modeWant {
			// The user has provided a new modeWant and it' different from the one before
			userData.modeWant = modeWant
		}

		// Save changes to DB
		if userData.modeWant != oldWant || userData.modeGiven != oldGiven {
			update := map[string]interface{}{}
			if userData.modeWant != oldWant {
				update["ModeWant"] = userData.modeWant
			}
			if userData.modeGiven != oldGiven {
				update["ModeGiven"] = userData.modeGiven
			}
			if err := store.Subs.Update(t.name, sess.uid, update, true); err != nil {
				sess.queueOut(ErrUnknown(pktID, t.original(sess.uid), now))
				return err
			}
		}

		// No transactions in RethinkDB, but two owners are better than none
		if ownerChange {

			oldOwnerData := t.perUser[t.owner]
			oldOwnerData.modeGiven = (oldOwnerData.modeGiven & ^types.ModeOwner)
			oldOwnerData.modeWant = (oldOwnerData.modeWant & ^types.ModeOwner)
			if err := store.Subs.Update(t.name, t.owner,
				map[string]interface{}{
					"ModeWant":  oldOwnerData.modeWant,
					"ModeGiven": oldOwnerData.modeGiven}, true); err != nil {
				return err
			}
			t.perUser[t.owner] = oldOwnerData
			t.owner = sess.uid
		}
	}

	// If topic is being muted, send "off" notification and disable updates.
	// DO it before applying the new permissions.
	if (oldWant & oldGiven).IsPresencer() && !(userData.modeWant & userData.modeGiven).IsPresencer() {
		t.presSingleUserOffline(sess.uid, "off+dis", nilPresParams, "", false)
	}

	// Apply changes.
	t.perUser[sess.uid] = userData

	// If the user is self-banning himself from the topic, no action is needed.
	// Re-subscription will unban.
	if !userData.modeWant.IsJoiner() {
		t.evictUser(sess.uid, false, "")
		// The callee will send NoErrOK
		return nil
	} else if !userData.modeGiven.IsJoiner() {
		// User was banned
		sess.queueOut(ErrPermissionDenied(pktID, t.original(sess.uid), now))
		return errors.New("topic access denied; user is banned")
	}

	// If this is a new subscription or the topic is being un-muted, notify after applying the changes.
	if (userData.modeWant & userData.modeGiven).IsPresencer() &&
		(!existingSub || !(oldWant & oldGiven).IsPresencer()) {
		// Notify subscriber of topic's online status.
		// log.Printf("topic[%s] sending ?unkn+en to me[%s]", t.name, sess.uid.String())
		t.presSingleUserOffline(sess.uid, "?unkn+en", nilPresParams, "", false)
	}

	// If something has changed and the requested access mode is different from the given, notify topic admins.
	// This will not send a notification for a newly created topic because Hub sets the same values for
	// the old want/given.
	if userData.modeWant != oldWant || userData.modeGiven != oldGiven {
		params := &presParams{
			actor:  sess.uid.UserId(),
			dWant:  oldWant.Delta(userData.modeWant),
			dGiven: oldGiven.Delta(userData.modeGiven)}

		// Announce to the admins who are online in the topic.
		t.presSubsOnline("acs", sess.uid.UserId(), params, &presFilters{filterIn: types.ModeCSharer}, sess.sid)

		// If it's a new subscription or if the user asked for permissions in excess of what was granted,
		// announce the request to topic admins on 'me' as well.
		var adminsNotified bool
		if !userData.modeGiven.BetterEqual(userData.modeWant) || !existingSub {
			t.presSubsOffline("acs", params, &presFilters{filterIn: types.ModeCSharer}, sess.sid, true)
			adminsNotified = true
		}

		if !adminsNotified || !(userData.modeWant & userData.modeGiven).IsSharer() {
			// Notify requester's other sessions.
			// Don't notify if already notified as an admin in the step above.
			t.presSingleUserOffline(sess.uid, "acs", params, sess.sid, false)
		}
	}

	return nil
}

// approveSub processes a request to initiate an invite or approve a subscription request from another user:
// Handle these cases:
// A. Sharer or Approver is inviting another user for the first time (no prior subscription)
// B. Sharer or Approver is re-inviting another user (adjusting modeGiven, modeWant is still Unset)
// C. Approver is changing modeGiven for another user, modeWant != Unset
func (t *Topic) approveSub(h *Hub, sess *Session, target types.Uid, set *MsgClientSet) error {
	log.Printf("approveSub, session uid=%s, target uid=%s", sess.uid.String(), target.String())

	now := types.TimeNow()

	// Access mode values as they were before this request was processed.
	oldWant := types.ModeNone
	oldGiven := types.ModeNone

	// Access mode of the person who is executing this approval process
	var hostMode types.AccessMode

	// Check if approver actually has permission to manage sharing
	userData, ok := t.perUser[sess.uid]
	if !ok || !(userData.modeGiven & userData.modeWant).IsSharer() {
		sess.queueOut(ErrPermissionDenied(set.Id, t.original(sess.uid), now))
		return errors.New("topic access denied; approver has no permission")
	}

	hostMode = userData.modeGiven & userData.modeWant

	// Parse the access mode granted
	modeGiven := types.ModeUnset
	if set.Sub.Mode != "" {
		if err := modeGiven.UnmarshalText([]byte(set.Sub.Mode)); err != nil {
			sess.queueOut(ErrMalformed(set.Id, t.original(sess.uid), now))
			return err
		}

		// Make sure the new permissions are reasonable in P2P topics: permissions no greater than default,
		// approver permission cannot be removed.
		if t.cat == types.TopicCatP2P {
			modeGiven &= types.ModeCP2P
			modeGiven |= types.ModeApprove
		}
	}

	// Make sure only the owner & approvers can set non-default access mode
	if modeGiven != types.ModeUnset && !hostMode.IsAdmin() {
		sess.queueOut(ErrPermissionDenied(set.Id, t.original(sess.uid), now))
		return errors.New("sharer cannot set explicit modeGiven")
	}

	// Make sure no one but the owner can do an ownership transfer
	if modeGiven.IsOwner() && t.owner != sess.uid {
		sess.queueOut(ErrPermissionDenied(set.Id, t.original(sess.uid), now))
		return errors.New("attempt to transfer ownership by non-owner")
	}

	// Check if it's a new invite. If so, save it to database as a subscription.
	// Saved subscription does not mean the user is allowed to post/read
	userData, existingSub := t.perUser[target]
	if !existingSub {

		// Check if the max number of subscriptions is already reached.
		if t.cat == types.TopicCatGrp && len(t.perUser) >= globals.maxSubscriberCount {
			sess.queueOut(ErrPolicy(set.Id, t.original(sess.uid), now))
			return errors.New("max subscription count exceeded")
		}

		if modeGiven == types.ModeUnset {
			// Request to use default access mode for the new subscriptions.
			// Assuming LevelAuth. Approver should use non-default access if that is not suitable.
			modeGiven = t.accessFor(auth.LevelAuth)
		}

		// Get user's default access mode to be used as modeWant
		var modeWant types.AccessMode
		if user, err := store.Users.Get(target); err != nil {
			sess.queueOut(ErrUnknown(set.Id, t.original(sess.uid), now))
			return err
		} else if user == nil {
			sess.queueOut(ErrUserNotFound(set.Id, t.original(sess.uid), now))
			return errors.New("user not found")
		} else {
			modeWant = user.Access.Auth
		}

		// Add subscription to database
		sub := &types.Subscription{
			User:      target.String(),
			Topic:     t.name,
			ModeWant:  modeWant,
			ModeGiven: modeGiven,
		}

		if err := store.Subs.Create(sub); err != nil {
			sess.queueOut(ErrUnknown(set.Id, t.original(sess.uid), now))
			return err
		}

		userData = perUserData{
			modeGiven: sub.ModeGiven,
			modeWant:  sub.ModeWant,
			private:   nil,
		}
		t.perUser[target] = userData

	} else {
		// Action on an existing subscription (re-invite or confirm/decline request)

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
				map[string]interface{}{"ModeGiven": modeGiven}, true); err != nil {
				return err
			}

			t.perUser[target] = userData
		}
	}

	// The user does not want to be bothered, no further action is needed
	if !userData.modeWant.IsJoiner() {
		sess.queueOut(ErrPermissionDenied(set.Id, t.original(sess.uid), now))
		return errors.New("user banned the topic")
	}

	// Access mode has changed.
	if oldGiven != userData.modeGiven {
		params := &presParams{
			actor:  sess.uid.UserId(),
			target: target.UserId(),
			dWant:  oldWant.Delta(userData.modeWant),
			dGiven: oldGiven.Delta(userData.modeGiven)}

		// Inform topic sharers.
		t.presSubsOffline("acs", params, &presFilters{filterIn: types.ModeCSharer}, sess.sid, false)

		// If tagret user is not a sharer, inform the target user separately.
		if !(userData.modeGiven & userData.modeWant).IsSharer() {
			t.presSingleUserOffline(target, "acs", params, sess.sid, false)
		}
	}

	if !existingSub && len(t.sessions) > 0 {
		// Notify the new subscriber that the topic is online, tell him to
		// track the new topic.
		t.presSingleUserOffline(target, "on+en", nilPresParams, "", false)
	}

	return nil
}

// replyGetDesc is a response to a get.desc request on a topic, sent to just the session as a {meta} packet
func (t *Topic) replyGetDesc(sess *Session, id, tempName string, opts *MsgGetOpts) error {
	now := types.TimeNow()

	// Check if user requested modified data
	ifUpdated := (opts == nil || opts.IfModifiedSince == nil || opts.IfModifiedSince.Before(t.updated))

	desc := &MsgTopicDesc{CreatedAt: &t.created}
	if !t.updated.IsZero() {
		desc.UpdatedAt = &t.updated
	}

	pud, full := t.perUser[sess.uid]
	if t.cat == types.TopicCatMe {
		full = true
	}

	if ifUpdated {
		if t.public != nil {
			desc.Public = t.public
		} else if full {
			// p2p topic
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

		if t.cat != types.TopicCatMe {
			desc.Acs = &MsgAccessMode{
				Want:  pud.modeWant.String(),
				Given: pud.modeGiven.String(),
				Mode:  (pud.modeGiven & pud.modeWant).String()}
		}

		if ifUpdated {
			desc.Private = pud.private
		}

		// Don't report message IDs to users without Read access.
		if (pud.modeGiven & pud.modeWant).IsReader() {
			desc.SeqId = t.lastID
			// Make sure reported values are sane:
			// t.delID <= pud.delID; t.readID <= t.recvID <= t.lastID
			desc.DelId = max(pud.delID, t.delID)
			desc.ReadSeqId = pud.readID
			desc.RecvSeqId = max(pud.recvID, pud.readID)
		}

		// When the topic is first created it may have been assigned a temporary name.
		// Report the temporary name here. It could be empty.
		if tempName != "" && tempName != t.original(sess.uid) {
			desc.TempName = tempName
		}
	}

	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{
			Id:        id,
			Topic:     t.original(sess.uid),
			Desc:      desc,
			Timestamp: &now}})

	return nil
}

// replySetDesc updates topic metadata, saves it to DB,
// replies to the caller as {ctrl} message, generates {pres} update if necessary
func (t *Topic) replySetDesc(sess *Session, set *MsgClientSet) error {
	now := types.TimeNow()

	assignAccess := func(upd map[string]interface{}, mode *MsgDefaultAcsMode) error {
		if auth, anon, err := parseTopicAccess(mode, types.ModeUnset, types.ModeUnset); err != nil {
			return err
		} else if auth.IsOwner() || anon.IsOwner() {
			return errors.New("default 'owner' access is not permitted")
		} else {
			access := types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon}
			if auth != types.ModeUnset {
				if t.cat == types.TopicCatMe {
					auth &= types.ModeCP2P
					if auth != types.ModeNone {
						// This is the default access mode for P2P topics.
						// It must be either an N or must include an A permission
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

	assignGenericValues := func(upd map[string]interface{}, what string, p interface{}) (changed bool) {
		if isNullValue(p) {
			// Request to clear the value
			if upd[what] != nil {
				upd[what] = nil
				changed = true
			}
		} else if p != nil {
			// A new non-nil value
			upd[what] = p
			changed = true
		}
		return
	}

	updateCached := func(upd map[string]interface{}) {
		if tmp, ok := upd["Access"]; ok {
			access := tmp.(types.DefaultAccess)
			t.accessAuth = access.Auth
			t.accessAnon = access.Anon
		}
		if public, ok := upd["Public"]; ok {
			t.public = public
		}
	}

	var err error
	var sendPres bool

	user := make(map[string]interface{})
	topic := make(map[string]interface{})
	sub := make(map[string]interface{})
	if set.Desc != nil {
		if t.cat == types.TopicCatMe {
			// Update current user
			if set.Desc.DefaultAcs != nil {
				err = assignAccess(user, set.Desc.DefaultAcs)
			}
			if set.Desc.Public != nil {
				sendPres = assignGenericValues(user, "Public", set.Desc.Public)
			}
		} else if t.cat == types.TopicCatP2P {
			// Reject direct changes to P2P topics.
			if set.Desc.Public != nil || set.Desc.DefaultAcs != nil {
				sess.queueOut(ErrPermissionDenied(set.Id, set.Topic, now))
				return errors.New("incorrect attempt to change metadata of a p2p topic")
			}
		} else if t.cat == types.TopicCatGrp {
			// Update group topic
			if set.Desc.DefaultAcs != nil || set.Desc.Public != nil {
				if t.owner == sess.uid {
					if set.Desc.DefaultAcs != nil {
						err = assignAccess(topic, set.Desc.DefaultAcs)
					}
					if set.Desc.Public != nil {
						sendPres = assignGenericValues(topic, "Public", set.Desc.Public)
					}
				} else {
					// This is a request from non-owner
					sess.queueOut(ErrPermissionDenied(set.Id, set.Topic, now))
					return errors.New("attempt to change public or permissions by non-owner")
				}
			}
		}
		// else fnd: update ignored

		if err != nil {
			sess.queueOut(ErrMalformed(set.Id, set.Topic, now))
			return err
		}

		if set.Desc.Private != nil {
			assignGenericValues(sub, "Private", set.Desc.Private)
		}
	}

	var change int
	if len(user) > 0 {
		err = store.Users.Update(sess.uid, user)
		change++
	}
	if err == nil && len(topic) > 0 {
		err = store.Topics.Update(t.name, topic)
		change++
	}
	if err == nil && len(sub) > 0 {
		err = store.Subs.Update(t.name, sess.uid, sub, true)
		change++
	}

	if err != nil {
		sess.queueOut(ErrUnknown(set.Id, set.Topic, now))
		return err
	} else if change == 0 {
		sess.queueOut(InfoNotModified(set.Id, set.Topic, now))
		return errors.New("{set} generated no update to DB")
	}

	// Update values cached in the topic object
	if private, ok := sub["Private"]; ok {
		pud := t.perUser[sess.uid]
		pud.private = private
		t.perUser[sess.uid] = pud
	}
	if t.cat == types.TopicCatMe {
		updateCached(user)
	} else if t.cat == types.TopicCatGrp {
		updateCached(topic)
	}

	if sendPres {
		// t.Public has changed, make an announcement
		if t.cat == types.TopicCatMe {
			t.presUsersOfInterest("upd", "")
			t.presSingleUserOffline(sess.uid, "upd", nilPresParams, sess.sid, false)
		} else {
			t.presSubsOffline("upd", nilPresParams, nilPresFilters, sess.sid, false)
		}
	}

	sess.queueOut(NoErr(set.Id, set.Topic, now))

	return nil
}

// replyGetSub is a response to a get.sub request on a topic - load a list of subscriptions/subscribers,
// send it just to the session as a {meta} packet
// FIXME(gene): reject request if the user does not have the R permission
func (t *Topic) replyGetSub(sess *Session, id string, opts *MsgGetOpts) error {
	now := types.TimeNow()

	var subs []types.Subscription
	var err error
	var isSharer bool

	if t.cat == types.TopicCatMe {
		// Fetch user's subscriptions, with Topic.Public denormalized into subscription.
		// Include deleted subscriptions too.
		subs, err = store.Users.GetTopicsAny(sess.uid)
		isSharer = true
	} else if t.cat == types.TopicCatFnd {
		// TODO: check the query (.private) against the set of allowed tags.
		// Given a query provided in .private, fetch user's contacts. Private contains a string.
		if query, ok := t.perUser[sess.uid].private.(string); ok {
			if len(query) > 0 {
				query, subs, err = pluginFind(sess.uid, query)
				if err == nil && subs == nil && query != "" {
					var req, opt []string
					req, opt, err = parseSearchQuery(query)
					if err == nil {
						subs, err = store.Users.FindSubs(sess.uid, req, opt)
					}
				}
			}
		} else {
			err = types.ErrMalformed
		}
	} else {
		// FIXME(gene): don't load subs from DB, use perUserData - it already contains subscriptions.
		subs, err = store.Topics.GetUsersAny(t.name)
		userData := t.perUser[sess.uid]
		isSharer = (userData.modeGiven & userData.modeWant).IsSharer()
	}

	if err != nil {
		sess.queueOut(decodeStoreError(err, id, t.original(sess.uid), now, nil))
		return err
	}

	var ifModified time.Time
	var limit int
	if opts != nil {
		if opts.IfModifiedSince != nil {
			ifModified = *opts.IfModifiedSince
		}
		limit = opts.Limit
	}

	if limit <= 0 {
		limit = 1024
	}

	meta := &MsgServerMeta{Id: id, Topic: t.original(sess.uid), Timestamp: &now}
	if len(subs) > 0 {
		meta.Sub = make([]MsgTopicSub, 0, len(subs))
		idx := 0
		for _, sub := range subs {
			if idx == limit {
				break
			}
			// Check if the requester has provided a cut off date for ts of pub & priv updates.
			var sendPubPriv bool
			var deleted, banned bool
			var mts MsgTopicSub

			if ifModified.IsZero() {
				// If IfModifiedSince is not set then the user does not care about managing cache. The user
				// only wants active subscriptions. Skip all deleted subscriptions regarless of deletion time.
				if sub.DeletedAt != nil {
					continue
				}

				sendPubPriv = true
			} else {
				// Skip sending deleted subscriptions if they were deleted before the cut off date.
				// If they are freshly deleted send minimum info
				if sub.DeletedAt != nil {
					if !sub.DeletedAt.After(ifModified) {
						continue
					}
					mts.DeletedAt = sub.DeletedAt
					deleted = true
				}
				sendPubPriv = !deleted && sub.UpdatedAt.After(ifModified)
			}

			uid := types.ParseUid(sub.User)
			isReader := sub.ModeGiven.IsReader() && sub.ModeWant.IsReader()
			if t.cat == types.TopicCatMe {
				// Mark subscriptions that the user does not care about.
				if !sub.ModeWant.IsJoiner() || !sub.ModeGiven.IsJoiner() {
					banned = true
				}

				// Reporting user's subscriptions to other topics. P2P topic name is the
				// UID of the other user.
				with := sub.GetWith()
				if with != "" {
					mts.Topic = with
					mts.Online = t.perSubs[with].online && !deleted && !banned
				} else {
					mts.Topic = sub.Topic
					mts.Online = t.perSubs[sub.Topic].online && !deleted && !banned
				}

				if !deleted && !banned {
					if isReader {
						mts.TouchedAt = sub.GetTouchedAt()
						mts.SeqId = sub.GetSeqId()
						mts.DelId = sub.DelId
					}

					lastSeen := sub.GetLastSeen()
					if !lastSeen.IsZero() {
						mts.LastSeen = &MsgLastSeenInfo{
							When:      &lastSeen,
							UserAgent: sub.GetUserAgent()}
					}
				}
			} else {
				// Mark subscriptions that the user does not care about.
				if t.cat == types.TopicCatGrp && !isSharer &&
					(!sub.ModeWant.IsJoiner() || !sub.ModeGiven.IsJoiner()) {
					banned = true
				}

				// Reporting subscribers to fnd, a group or a p2p topic
				mts.User = uid.UserId()
				if t.cat == types.TopicCatFnd {
					mts.Topic = sub.Topic
				}

				if !deleted {
					if uid == sess.uid && isReader {
						// Report deleted ID for own subscriptions only
						mts.DelId = sub.DelId
					}

					if t.cat == types.TopicCatGrp {
						pud := t.perUser[uid]
						mts.Online = pud.online > 0
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
					if isSharer {
						mts.Acs.Want = sub.ModeWant.String()
						mts.Acs.Given = sub.ModeGiven.String()
					}
				}

				// Returning public and private only if they have changed since ifModified
				if sendPubPriv {
					mts.Public = sub.GetPublic()
					// Reporting private only if it's user's own subscription or
					// a synthetic 'private' in 'find' topic where it's a list of tags matched on.
					if uid == sess.uid || t.cat == types.TopicCatFnd {
						mts.Private = sub.Private
					}
				}
			}

			meta.Sub = append(meta.Sub, mts)
			idx++
		}
	}

	sess.queueOut(&ServerComMessage{Meta: meta})

	return nil
}

// replySetSub is a response to new subscription request or an update to a subscription {set.sub}:
// update topic metadata cache, save/update subs, reply to the caller as {ctrl} message,
// generate a presence notification, if appropriate.
func (t *Topic) replySetSub(h *Hub, sess *Session, set *MsgClientSet) error {
	now := types.TimeNow()

	var uid types.Uid
	if uid = types.ParseUserId(set.Sub.User); uid.IsZero() && set.Sub.User != "" {
		// Invalid user ID
		sess.queueOut(ErrMalformed(set.Id, t.original(sess.uid), now))
		return errors.New("invalid user id")
	}

	// if set.User is not set, request is for the current user
	if uid.IsZero() {
		uid = sess.uid
	}

	var err error
	if uid == sess.uid {
		// Request new subscription or modify own subscription
		err = t.requestSub(h, sess, set.Id, set.Sub.Mode, nil)
	} else {
		// Request to approve/change someone's subscription
		err = t.approveSub(h, sess, uid, set)
	}
	if err != nil {
		return err
	}

	resp := NoErr(set.Id, t.original(sess.uid), now)
	// Report resulting access mode.
	pud := t.perUser[uid]
	params := map[string]interface{}{"acs": MsgAccessMode{
		Given: pud.modeGiven.String(),
		Want:  pud.modeWant.String(),
		Mode:  (pud.modeGiven & pud.modeWant).String()}}
	if uid != sess.uid {
		params["user"] = uid.String()
	}
	resp.Ctrl.Params = params
	sess.queueOut(resp)

	return nil
}

// replyGetData is a response to a get.data request - load a list of stored messages, send them to session as {data}
// response goes to a single session rather than all sessions in a topic
func (t *Topic) replyGetData(sess *Session, id string, req *MsgBrowseOpts) error {
	now := types.TimeNow()

	// Check if the user has permission to read the topic data
	if userData := t.perUser[sess.uid]; (userData.modeGiven & userData.modeWant).IsReader() {
		// Read messages from DB
		messages, err := store.Messages.GetAll(t.name, sess.uid, msgOpts2storeOpts(req))
		if err != nil {
			sess.queueOut(ErrUnknown(id, t.original(sess.uid), now))
			return err
		}

		// Push the list of messages to the client as {data}.
		// Messages are sent in reverse order than fetched from DB to make it easier for
		// clients to process.
		if messages != nil {
			for i := len(messages) - 1; i >= 0; i-- {
				mm := messages[i]

				from := types.ParseUid(mm.From)
				msg := &ServerComMessage{Data: &MsgServerData{
					Topic:     t.original(sess.uid),
					Head:      mm.Head,
					SeqId:     mm.SeqId,
					From:      from.UserId(),
					Timestamp: mm.CreatedAt,
					Content:   mm.Content}}

				sess.queueOut(msg)
			}
		}
	}

	// Inform the requester that all the data has been served.
	reply := NoErr(id, t.original(sess.uid), now)
	reply.Ctrl.Params = map[string]string{"what": "data"}
	sess.queueOut(reply)

	return nil
}

// replyGetTags returns topic's tags - tokens used for discovery.
func (t *Topic) replyGetTags(sess *Session, id string) error {
	now := types.TimeNow()

	if t.cat != types.TopicCatFnd && t.cat != types.TopicCatGrp {
		sess.queueOut(ErrOperationNotAllowed(id, t.original(sess.uid), now))
		return errors.New("invalid topic category for getting tags")
	}
	if t.cat == types.TopicCatGrp && t.owner != sess.uid {
		sess.queueOut(ErrPermissionDenied(id, t.original(sess.uid), now))
		return errors.New("request for tags from non-owner")
	}

	if len(t.tags) > 0 {
		sess.queueOut(&ServerComMessage{
			Meta: &MsgServerMeta{Id: id, Topic: t.original(sess.uid), Timestamp: &now, Tags: t.tags}})
		return nil
	}

	// Inform the requester that there are no tags.
	reply := NoErr(id, t.original(sess.uid), now)
	reply.Ctrl.Params = map[string]string{"what": "tags"}
	sess.queueOut(reply)

	return nil
}

// replySetTags updates topic's tags - tokens used for discovery.
func (t *Topic) replySetTags(sess *Session, set *MsgClientSet) error {
	var resp *ServerComMessage
	var err error

	now := types.TimeNow()

	if t.cat != types.TopicCatFnd && t.cat != types.TopicCatGrp {
		resp = ErrOperationNotAllowed(set.Id, t.original(sess.uid), now)
		err = errors.New("invalid topic category to assign tags")

	} else if t.cat == types.TopicCatGrp && t.owner != sess.uid {
		resp = ErrPermissionDenied(set.Id, t.original(sess.uid), now)
		err = errors.New("tags update by non-owner")

	} else if tags := normalizeTags(set.Tags); tags != nil {
		if !restrictedTagsEqual(t.tags, tags, globals.immutableTagNS) {
			err = errors.New("attempt to mutate restricted tags")
			resp = ErrPermissionDenied(set.Id, t.original(sess.uid), now)
		} else {
			added, removed := stringSliceDelta(t.tags, tags)
			if len(added) > 0 || len(removed) > 0 {
				update := map[string]interface{}{"Tags": types.StringSlice(tags)}

				if t.cat == types.TopicCatFnd {
					err = store.Users.Update(sess.uid, update)
				} else if t.cat == types.TopicCatGrp {
					err = store.Topics.Update(t.name, update)
				}

				if err != nil {
					resp = ErrUnknown(set.Id, t.original(sess.uid), now)
				} else {
					t.tags = tags

					resp = NoErr(set.Id, t.original(sess.uid), now)
					params := make(map[string]interface{})
					if len(added) > 0 {
						params["added"] = len(added)
					}
					if len(removed) > 0 {
						params["removed"] = len(removed)
					}
					resp.Ctrl.Params = params
				}
			} else {
				resp = InfoNotModified(set.Id, t.original(sess.uid), now)
			}
		}
	} else {
		resp = InfoNotModified(set.Id, t.original(sess.uid), now)
	}

	sess.queueOut(resp)

	return err
}

// replyGetDel is a response to a get[what=del] request: load a list of deleted message ids, send them to
// a session as {meta}
// response goes to a single session rather than all sessions in a topic
func (t *Topic) replyGetDel(sess *Session, id string, req *MsgBrowseOpts) error {
	now := types.TimeNow()

	// Check if the user has permission to read the topic data and the request is valid
	if userData := t.perUser[sess.uid]; (userData.modeGiven & userData.modeWant).IsReader() && req != nil {
		ranges, delID, err := store.Messages.GetDeleted(t.name, sess.uid, msgOpts2storeOpts(req))
		if err != nil {
			sess.queueOut(ErrUnknown(id, t.original(sess.uid), now))
			return err
		}

		if len(ranges) > 0 {
			sess.queueOut(&ServerComMessage{Meta: &MsgServerMeta{
				Id:    id,
				Topic: t.original(sess.uid),
				Del: &MsgDelValues{
					DelId:  delID,
					DelSeq: delrangeDeserialize(ranges)},
				Timestamp: &now}})
			return nil
		}
	}

	reply := NoErr(id, t.original(sess.uid), now)
	reply.Ctrl.Params = map[string]string{"what": "del"}
	sess.queueOut(reply)

	return nil
}

// replyDelMsg deletes (soft or hard) messages in response to del.msg packet.
func (t *Topic) replyDelMsg(sess *Session, del *MsgClientDel) error {
	now := types.TimeNow()

	var err error

	defer func() {
		if err != nil {
			log.Println("failed to delete message(s):", err)
		}
	}()

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
				dq.HiId = t.lastID
			} else if dq.LowId == dq.HiId {
				dq.HiId = 0
			}

			if dq.HiId == 0 {
				count++
			} else {
				count += dq.HiId - dq.LowId + 1
			}

			ranges = append(ranges, types.Range{Low: dq.LowId, Hi: dq.HiId})
		}

		if err == nil {
			// Sort by Low ascending then by Hi descending.
			sort.Sort(types.RangeSorter(ranges))
			// Collapse overlapping ranges
			types.RangeSorter(ranges).Normalize()
		}

		if count > defaultMaxDeleteCount && len(ranges) > 1 {
			err = errors.New("del.msg: too many messages to delete")
		}
	}

	if err != nil {
		sess.queueOut(ErrMalformed(del.Id, t.original(sess.uid), now))
		return err
	}

	pud := t.perUser[sess.uid]
	if !(pud.modeGiven & pud.modeWant).IsDeleter() {
		// User must have an R permission: if the user cannot read messages, he has
		// no business of deleting them.
		if !(pud.modeGiven & pud.modeWant).IsReader() {
			sess.queueOut(ErrPermissionDenied(del.Id, t.original(sess.uid), now))
			return errors.New("del.msg: permission denied")
		}

		// User has just the R permission, cannot hard-delete messages, silently
		// switching to soft-deleting
		del.Hard = false
	}

	forUser := sess.uid
	if del.Hard {
		forUser = types.ZeroUid
	}

	if err = store.Messages.DeleteList(t.name, t.delID+1, forUser, ranges); err != nil {
		sess.queueOut(ErrUnknown(del.Id, t.original(sess.uid), now))
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
		params := &presParams{delID: t.delID, delSeq: dr, actor: sess.uid.UserId()}
		filters := &presFilters{filterIn: types.ModeRead}
		t.presSubsOnline("del", params.actor, params, filters, sess.sid)
		t.presSubsOffline("del", params, filters, sess.sid, true)
	} else {
		pud := t.perUser[sess.uid]
		pud.delID = t.delID
		t.perUser[sess.uid] = pud

		// Notify user's other sessions
		t.presPubMessageDelete(sess.uid, t.delID, dr, sess.sid)
	}

	reply := NoErr(del.Id, t.original(sess.uid), now)
	reply.Ctrl.Params = map[string]int{"del": t.delID}
	sess.queueOut(reply)

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
func (t *Topic) replyDelTopic(h *Hub, sess *Session, del *MsgClientDel) error {
	if t.owner != sess.uid {
		// Cases 2.1.1 and 2.2
		if t.cat != types.TopicCatP2P || len(t.perUser) > 1 {
			return t.replyLeaveUnsub(h, sess, del.Id)
		}
	}

	// Notifications are sent from the topic loop.

	for s := range t.sessions {
		delete(t.sessions, s)
		s.detach <- t.name
	}

	return nil
}

func (t *Topic) replyDelSub(h *Hub, sess *Session, del *MsgClientDel) error {
	now := types.TimeNow()

	var err error

	// Get ID of the affected user
	uid := types.ParseUserId(del.User)

	pud := t.perUser[sess.uid]
	if !(pud.modeGiven & pud.modeWant).IsAdmin() {
		err = errors.New("del.sub: permission denied")
	} else if uid.IsZero() || uid == sess.uid {
		// Cannot delete self-subscription. User [leave unsub] or [delete topic]
		err = errors.New("del.sub: cannot delete self-subscription")
	} else if t.cat == types.TopicCatP2P {
		// Don't try to delete the other P2P user
		err = errors.New("del.sub: cannot apply to a P2P topic")
	}

	if err != nil {
		sess.queueOut(ErrPermissionDenied(del.Id, t.original(sess.uid), now))
		return err
	}

	pud, ok := t.perUser[uid]
	if !ok {
		sess.queueOut(InfoNoAction(del.Id, t.original(sess.uid), now))
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
		sess.queueOut(ErrPermissionDenied(del.Id, t.original(sess.uid), now))
		return err
	}

	// Delete user's subscription from the database
	if err := store.Subs.Delete(t.name, uid); err != nil {
		sess.queueOut(ErrUnknown(del.Id, t.original(sess.uid), now))

		return err
	}

	sess.queueOut(NoErr(del.Id, t.original(sess.uid), now))

	t.evictUser(uid, true, "")

	return nil
}

func (t *Topic) replyLeaveUnsub(h *Hub, sess *Session, id string) error {
	now := types.TimeNow()

	if t.owner == sess.uid {
		if id != "" {
			sess.queueOut(ErrPermissionDenied(id, t.original(sess.uid), now))
		}
		return errors.New("replyLeaveUnsub: owner cannot unsubscribe")
	}

	// Delete user's subscription from the database
	if err := store.Subs.Delete(t.name, sess.uid); err != nil {
		if id != "" {
			sess.queueOut(ErrUnknown(id, t.original(sess.uid), now))
		}

		return err
	}

	if id != "" {
		sess.queueOut(NoErr(id, t.original(sess.uid), now))
	}

	// Evict all user's sessions and clear cached data
	t.evictUser(sess.uid, true, sess.sid)

	return nil
}

// evictUser evicts given user's sessions from the topic and clears user's cached data, if requested
func (t *Topic) evictUser(uid types.Uid, unsub bool, skip string) {
	log.Println("evictUser", uid, unsub, skip)

	now := types.TimeNow()

	pud := t.perUser[uid]

	// First notify topic subscribers that the user has left the topic
	if t.cat == types.TopicCatGrp {
		if unsub {
			// Let admins know, exclude the user himself
			t.presSubsOnline("acs", uid.UserId(),
				&presParams{
					actor:  skip,
					target: uid.UserId(),
					dWant:  types.ModeNone.String(),
					dGiven: types.ModeNone.String()},
				&presFilters{
					filterIn:    types.ModeCSharer,
					excludeUser: uid.UserId()},
				skip)

			// Let affected user know
			t.presSingleUserOffline(uid, "gone", nilPresParams, skip, false)
		}

		if pud.online > 0 {
			// Let all 'R' subscribers know that the user is gone.
			t.presSubsOnline("off", uid.UserId(), nilPresParams,
				&presFilters{
					filterIn:    types.ModeRead,
					excludeUser: uid.UserId()},
				skip)
		}

	} else if t.cat == types.TopicCatP2P && unsub {
		// Notify user's own sessions that the topic is gone and remove the other user from
		// perSubs.
		t.presSingleUserOffline(uid, "gone", nilPresParams, skip, false)

		if len(t.perUser) == 2 {
			// Tell user2 to stop sending updates to user1
			presSingleUserOfflineOffline(t.p2pOtherUser(uid), uid.UserId(), "?none+rem", nilPresParams, "")
		}
	}

	// Save topic name. It won't be available later
	original := t.original(uid)

	// Second - detach user from topic
	if unsub {
		// Delete per-user data
		delete(t.perUser, uid)
	} else {
		// Clear online status
		pud.online = 0
		t.perUser[uid] = pud
	}

	// Detach all user's sessions
	for sess := range t.sessions {
		if sess.uid == uid {
			delete(t.sessions, sess)
			sess.detach <- t.name
			if sess.sid != skip {
				sess.queueOut(NoErrEvicted("", original, now))
			}
		}
	}
}

// Prepares a payload to be delivered to a mobile device as a push notification.
func (t *Topic) makePushReceipt(data *MsgServerData) *pushReceipt {
	idx := make(map[types.Uid]int, len(t.perUser))
	receipt := push.Receipt{
		To: make([]push.Recipient, len(t.perUser)),
		Payload: push.Payload{
			Topic:     data.Topic,
			From:      data.From,
			Timestamp: data.Timestamp,
			SeqId:     data.SeqId,
			Content:   data.Content}}

	i := 0
	for uid, pud := range t.perUser {
		if (pud.modeWant & pud.modeGiven).IsPresencer() {
			// Only send to those users who have notifications enabled
			receipt.To[i].User = uid
			idx[uid] = i
			i++
		}
	}

	return &pushReceipt{rcpt: &receipt, uidMap: idx}
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

func (t *Topic) suspend() {
	atomic.StoreInt32((*int32)(&t.suspended), 1)
}

func (t *Topic) resume() {
	atomic.StoreInt32((*int32)(&t.suspended), 0)
}

func (t *Topic) isSuspended() bool {
	return atomic.LoadInt32((*int32)(&t.suspended)) != 0
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

// Get topic name suitable for the given client
func (t *Topic) p2pOtherUser(uid types.Uid) types.Uid {
	if t.cat == types.TopicCatP2P {
		for u2 := range t.perUser {
			if u2.Compare(uid) != 0 {
				return u2
			}
		}
		panic("Invalid P2P topic")
	}
	panic("Not P2P topic")
}

func (t *Topic) accessFor(authLvl auth.Level) types.AccessMode {
	return selectAccessMode(authLvl, t.accessAnon, t.accessAuth, getDefaultAccess(t.cat, true))
}

func topicCat(name string) types.TopicCat {
	return types.GetTopicCat(name)
}

// Generate random string as a name of the group topic
func genTopicName() string {
	return "grp" + store.GetUidString()
}
