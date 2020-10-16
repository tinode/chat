/******************************************************************************
 *
 *  Description :
 *
 *    Main hub for processing events such as creating/tearing down topics,
 *    routing messages between topics.
 *
 *****************************************************************************/

package main

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Request latency distribution bounds (in milliseconds).
// "var" because Go does not support array constants.
var RequestLatencyDistribution = []float64{1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130,
	160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000}

// Outgoing message size distribution bounds (in bytes).
var OutgoingMessageSizeDistribution = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 16384,
	65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296}

// Request to hub to subscribe session to topic
type sessionJoin struct {
	// Message, containing request details.
	pkt *ClientComMessage
	// Session to attach to topic.
	sess *Session
}

// Session wants to leave the topic
type sessionLeave struct {
	// Message, containing request details. Could be nil.
	pkt *ClientComMessage
	// Session which initiated the request
	sess *Session
}

// Request to hub to remove the topic
type topicUnreg struct {
	// Original request, could be nil,
	pkt *ClientComMessage
	// Session making the request, could be nil.
	sess *Session
	// Routable name of the topic to drop. Duplicated here because pkt could be nil.
	rcptTo string
	// UID of the user being deleted. Duplicated here because pkt could be nil.
	forUser types.Uid
	// Unregister then delete the topic.
	del bool
	// Channel for reporting operation completion when deleting topics for a user.
	done chan<- bool
}

type metaReq struct {
	// Packet containing details of the Get/Set/Del request.
	pkt *ClientComMessage
	// Session which originated the request.
	sess *Session
	// UID of the user being affected. Could be zero.
	forUser types.Uid
	// New topic state value. Only types.StateSuspended is supported at this time.
	state types.ObjState
}

// Hub is the core structure which holds topics.
type Hub struct {

	// Topics must be indexed by name
	topics *sync.Map

	// Channel for routing messages between topics, buffered at 4096
	route chan *ServerComMessage

	// subscribe session to topic, possibly creating a new topic, buffered at 32
	join chan *sessionJoin

	// Remove topic from hub, possibly deleting it afterwards, buffered at 32
	unreg chan *topicUnreg

	// Process get.info requests for topic not subscribed to, buffered 128
	meta chan *metaReq

	// Cluster request to rehash topics, unbuffered
	rehash chan bool

	// Request to shutdown, unbuffered
	shutdown chan chan<- bool
}

func (h *Hub) topicGet(name string) *Topic {
	if t, ok := h.topics.Load(name); ok {
		return t.(*Topic)
	}
	return nil
}

func (h *Hub) topicPut(name string, t *Topic) {
	h.topics.Store(name, t)
}

func (h *Hub) topicDel(name string) {
	h.topics.Delete(name)
}

func newHub() *Hub {
	var h = &Hub{
		topics: &sync.Map{},
		// this needs to be buffered - hub generates invites and adds them to this queue
		route:    make(chan *ServerComMessage, 4096),
		join:     make(chan *sessionJoin, 256),
		unreg:    make(chan *topicUnreg, 256),
		rehash:   make(chan bool),
		meta:     make(chan *metaReq, 128),
		shutdown: make(chan chan<- bool),
	}

	statsRegisterInt("LiveTopics")
	statsRegisterInt("TotalTopics")

	statsRegisterInt("IncomingMessagesWebsockTotal")
	statsRegisterInt("OutgoingMessagesWebsockTotal")

	statsRegisterInt("IncomingMessagesLongpollTotal")
	statsRegisterInt("OutgoingMessagesLongpollTotal")

	statsRegisterInt("IncomingMessagesGrpcTotal")
	statsRegisterInt("OutgoingMessagesGrpcTotal")

	statsRegisterInt("FileDownloadsTotal")
	statsRegisterInt("FileUploadsTotal")

	statsRegisterInt("CtrlCodesTotal2xx")
	statsRegisterInt("CtrlCodesTotal3xx")
	statsRegisterInt("CtrlCodesTotal4xx")
	statsRegisterInt("CtrlCodesTotal5xx")

	statsRegisterHistogram("RequestLatency", RequestLatencyDistribution)
	statsRegisterHistogram("OutgoingMessageSize", OutgoingMessageSizeDistribution)

	go h.run()

	if !globals.cluster.isRemoteTopic("sys") {
		// Initialize system 'sys' topic. There is only one sys topic per cluster.
		h.join <- &sessionJoin{pkt: &ClientComMessage{RcptTo: "sys", Original: "sys"}}
	}

	return h
}

func (h *Hub) run() {

	for {
		select {
		case join := <-h.join:
			// Handle a subscription request:
			// 1. Init topic
			// 1.1 If a new topic is requested, create it
			// 1.2 If a new subscription to an existing topic is requested:
			// 1.2.1 check if topic is already loaded
			// 1.2.2 if not, load it
			// 1.2.3 if it cannot be loaded (not found), fail
			// 2. Check access rights and reject, if appropriate
			// 3. Attach session to the topic
			// Is the topic already loaded?
			t := h.topicGet(join.pkt.RcptTo)
			if t == nil {
				// Topic does not exist or not loaded.
				t = &Topic{name: join.pkt.RcptTo,
					xoriginal: join.pkt.Original,
					// Indicates a proxy topic.
					isProxy:   globals.cluster.isRemoteTopic(join.pkt.RcptTo),
					sessions:  make(map[*Session]perSessionData),
					broadcast: make(chan *ServerComMessage, 256),
					reg:       make(chan *sessionJoin, 256),
					unreg:     make(chan *sessionLeave, 256),
					meta:      make(chan *metaReq, 64),
					perUser:   make(map[types.Uid]perUserData),
					exit:      make(chan *shutDown, 1),
				}
				if globals.cluster != nil {
					if t.isProxy {
						t.proxy = make(chan *ClusterResp, 32)
						t.masterNode = globals.cluster.ring.Get(t.name)
					} else {
						// It's a master topic. Make a channel for handling
						// direct messages from the proxy.
						t.master = make(chan *ClusterSessUpdate, 8)
					}
				}
				// Topic is created in suspended state because it's not yet configured.
				t.markPaused(true)
				// Save topic now to prevent race condition.
				h.topicPut(join.pkt.RcptTo, t)

				// Configure the topic.
				go topicInit(t, join, h)

			} else {
				// Topic found.
				// Topic will check access rights and send appropriate {ctrl}
				select {
				case t.reg <- join:
				default:
					if join.sess.inflightReqs != nil {
						join.sess.inflightReqs.Done()
					}
					join.sess.queueOut(ErrServiceUnavailableReply(join.pkt, join.pkt.Timestamp))
					log.Println("hub.join loop: topic's reg queue full", join.pkt.RcptTo, join.sess.sid, " - total queue len:", len(t.reg))
				}
			}

		case msg := <-h.route:
			// This is a message from a connection not subscribed to topic
			// Route incoming message to topic if topic permits such routing.

			if dst := h.topicGet(msg.RcptTo); dst != nil {
				// Everything is OK, sending packet to known topic
				if dst.broadcast != nil {
					select {
					case dst.broadcast <- msg:
					default:
						log.Println("hub: topic's broadcast queue is full", dst.name)
					}
				} else {
					log.Println("hub: invalid topic category for broadcast", dst.name)
				}
			} else if (strings.HasPrefix(msg.RcptTo, "usr") || strings.HasPrefix(msg.RcptTo, "grp")) &&
				globals.cluster.isRemoteTopic(msg.RcptTo) {
				// It is a remote topic.
				if err := globals.cluster.routeToTopicIntraCluster(msg.RcptTo, msg, msg.sess); err != nil {
					log.Printf("hub: routing to '%s' failed", msg.RcptTo)
				}
			} else if msg.Pres == nil && msg.Info == nil {
				// Topic is unknown or offline.
				// Pres & Info are silently ignored, all other messages are reported as invalid.

				// TODO(gene): validate topic name, discarding invalid topics

				log.Printf("Hub. Topic[%s] is unknown or offline", msg.RcptTo)

				msg.sess.queueOut(NoErrAcceptedExplicitTs(msg.Id, msg.RcptTo, types.TimeNow(), msg.Timestamp))
			}

		case meta := <-h.meta:
			// Suspend/activate user's topics
			if !meta.forUser.IsZero() {
				go h.topicsStateForUser(meta.forUser, meta.state == types.StateSuspended)
			} else {
				// Metadata read or update from a user who is not attached to the topic.
				if meta.pkt.Get != nil {
					if meta.pkt.MetaWhat == constMsgMetaDesc {
						go replyOfflineTopicGetDesc(meta.sess, meta.pkt)
					} else {
						go replyOfflineTopicGetSub(meta.sess, meta.pkt)
					}
				} else if meta.pkt.Set != nil {
					go replyOfflineTopicSetSub(meta.sess, meta.pkt)
				}
			}

		case unreg := <-h.unreg:
			reason := StopNone
			if unreg.del {
				reason = StopDeleted
			}
			if unreg.forUser.IsZero() {
				// The topic is being garbage collected or deleted.
				if err := h.topicUnreg(unreg.sess, unreg.rcptTo, unreg.pkt, reason); err != nil {
					log.Println("hub.topicUnreg failed:", err)
				}
			} else {
				go h.stopTopicsForUser(unreg.forUser, reason, unreg.done)
			}

		case <-h.rehash:
			// Cluster rehashing. Some previously local topics became remote,
			// and the other way round.
			// Such topics must be shut down at this node.
			h.topics.Range(func(_, t interface{}) bool {
				topic := t.(*Topic)
				// Handle two cases:
				// 1. Master topic has moved out to another node.
				// 2. Proxy topic is running on a new master node
				//    (i.e. the master topic has moved to this node).
				if topic.isProxy != globals.cluster.isRemoteTopic(topic.name) {
					h.topicUnreg(nil, topic.name, nil, StopRehashing)
				}
				return true
			})

			// Check if 'sys' topic has migrated to this node.
			if h.topicGet("sys") == nil && !globals.cluster.isRemoteTopic("sys") {
				// Yes, 'sys' has migrated here. Initialize it.
				// The h.join is unbuffered. We must call from another goroutine. Otherwise deadlock.
				go func() {
					h.join <- &sessionJoin{pkt: &ClientComMessage{RcptTo: "sys", Original: "sys"}}
				}()
			}

		case hubdone := <-h.shutdown:
			// start cleanup process
			topicsdone := make(chan bool)
			topicCount := 0
			h.topics.Range(func(_, topic interface{}) bool {
				topic.(*Topic).exit <- &shutDown{done: topicsdone}
				topicCount++
				return true
			})

			for i := 0; i < topicCount; i++ {
				<-topicsdone
			}

			log.Printf("Hub shutdown completed with %d topics", topicCount)

			// let the main goroutine know we are done with the cleanup
			hubdone <- true

			return

		case <-time.After(idleSessionTimeout):
		}
	}
}

// Update state of all topics associated with the given user:
// * all p2p topics with the given user
// * group topics where the given user is the owner.
// 'me' and fnd' are ignored here because they are direcly tied to the user object.
func (h *Hub) topicsStateForUser(uid types.Uid, suspended bool) {
	h.topics.Range(func(name interface{}, t interface{}) bool {
		topic := t.(*Topic)
		if topic.cat == types.TopicCatMe || topic.cat == types.TopicCatFnd {
			return true
		}

		if _, isMember := topic.perUser[uid]; (topic.cat == types.TopicCatP2P && isMember) || topic.owner == uid {
			topic.markReadOnly(suspended)

			// Don't send "off" notification on suspension. They will be sent when the user is evicted.
		}
		return true
	})
}

// topicUnreg deletes or unregisters the topic:
//
// Cases:
// 1. Topic being deleted
// 1.1 Topic is online
// 1.1.1 If the requester is the owner or if it's the last sub in a p2p topic:
// 1.1.1.1 Tell topic to stop accepting requests.
// 1.1.1.2 Hub deletes the topic from database
// 1.1.1.3 Hub unregisters the topic
// 1.1.1.4 Hub informs the origin of success or failure
// 1.1.1.5 Hub forwards request to topic
// 1.1.1.6 Topic evicts all sessions
// 1.1.1.7 Topic exits the run() loop
// 1.1.2 If the requester is not the owner
// 1.1.2.1 Send it to topic to be treated like {leave unsub=true}
//
// 1.2 Topic is offline
// 1.2.1 If requester is the owner
// 1.2.1.1 Hub deletes topic from database
// 1.2.2 If not the owner
// 1.2.2.1 Delete subscription from DB
// 1.2.3 Hub informs the origin of success or failure
// 1.2.4 Send notification to subscribers that the topic was deleted

// 2. Topic is just being unregistered (topic is going offline)
// 2.1 Unregister it with no further action
//
func (h *Hub) topicUnreg(sess *Session, topic string, msg *ClientComMessage, reason int) error {
	now := time.Now().UTC().Round(time.Millisecond)

	if reason == StopDeleted {
		asUid := types.ParseUserId(msg.AsUser)
		// Case 1 (unregister and delete)
		if t := h.topicGet(topic); t != nil {
			// Case 1.1: topic is online
			if t.owner == asUid || (t.cat == types.TopicCatP2P && t.subsCount() < 2) {
				// Case 1.1.1: requester is the owner or last sub in a p2p topic

				t.markPaused(true)
				if err := store.Topics.Delete(topic, msg.Del.Hard); err != nil {
					t.markPaused(false)
					sess.queueOut(ErrUnknownReply(msg, now))
					return err
				}

				sess.queueOut(NoErrReply(msg, now))

				h.topicDel(topic)
				t.markDeleted()
				t.exit <- &shutDown{reason: StopDeleted}
				statsInc("LiveTopics", -1)
			} else {
				// Case 1.1.2: requester is NOT the owner
				msg.MetaWhat = constMsgDelTopic
				t.meta <- &metaReq{
					pkt:  msg,
					sess: sess}
			}

		} else {
			// Case 1.2: topic is offline.

			asUid := types.ParseUserId(msg.AsUser)
			// Get all subscribers: we need to know how many are left and notify them.
			subs, err := store.Topics.GetSubs(topic, nil)
			if err != nil {
				sess.queueOut(ErrUnknownReply(msg, now))
				return err
			}

			if len(subs) == 0 {
				sess.queueOut(InfoNoActionReply(msg, now))
				return nil
			}

			var sub *types.Subscription
			user := asUid.String()
			for i := 0; i < len(subs); i++ {
				if subs[i].User == user {
					sub = &subs[i]
					break
				}
			}

			if sub == nil {
				// If user has no subscription, tell him all is fine
				sess.queueOut(InfoNoAction(msg.Id, msg.Original, now, msg.Timestamp))
				return nil
			}

			tcat := topicCat(topic)
			if !(sub.ModeGiven & sub.ModeWant).IsOwner() {
				// Case 1.2.2.1 Not the owner, but possibly last subscription in a P2P topic.

				if tcat == types.TopicCatP2P && len(subs) < 2 {
					// This is a P2P topic and fewer than 2 subscriptions, delete the entire topic
					if err := store.Topics.Delete(topic, msg.Del.Hard); err != nil {
						sess.queueOut(ErrUnknownReply(msg, now))
						return err
					}

				} else if err := store.Subs.Delete(topic, asUid); err != nil {
					// Not P2P or more than 1 subscription left.
					// Delete user's own subscription only
					if err == types.ErrNotFound {
						sess.queueOut(InfoNoActionReply(msg, now))
						err = nil
					} else {
						sess.queueOut(ErrUnknownReply(msg, now))
					}
					return err
				}

				// Notify user's other sessions that the subscription is gone
				presSingleUserOfflineOffline(asUid, msg.Original, "gone", nilPresParams, sess.sid)
				if tcat == types.TopicCatP2P && len(subs) == 2 {
					uname1 := asUid.UserId()
					uid2 := types.ParseUserId(msg.Original)
					// Tell user1 to stop sending updates to user2 without passing change to user1's sessions.
					presSingleUserOfflineOffline(asUid, uid2.UserId(), "?none+rem", nilPresParams, "")
					// Don't change the online status of user1, just ask user2 to stop notification exchange.
					// Tell user2 that user1 is offline but let him keep sending updates in case user1 resubscribes.
					presSingleUserOfflineOffline(uid2, uname1, "off", nilPresParams, "")
				}

			} else {
				// Case 1.2.1.1: owner, delete the group topic from db.
				// Only group topics have owners.
				if err := store.Topics.Delete(topic, msg.Del.Hard); err != nil {
					sess.queueOut(ErrUnknownReply(msg, now))
					return err
				}

				// Notify subscribers that the group topic is gone.
				presSubsOfflineOffline(msg.Original, tcat, subs, "gone", &presParams{}, sess.sid)
			}

			sess.queueOut(NoErrReply(msg, now))
		}

	} else {
		// Case 2: just unregister.
		// If t is nil, it's not registered, no action is needed
		if t := h.topicGet(topic); t != nil {
			t.markDeleted()
			h.topicDel(topic)

			t.exit <- &shutDown{reason: reason}

			statsInc("LiveTopics", -1)
		}

		// sess && msg could be nil if the topic is being killed by timer or due to rehashing.
		if sess != nil && msg != nil {
			sess.queueOut(NoErrReply(msg, now))
		}
	}

	return nil
}

// Terminate all topics associated with the given user:
// * all p2p topics with the given user
// * group topics where the given user is the owner.
// * user's 'me' and 'fnd' topics.
func (h *Hub) stopTopicsForUser(uid types.Uid, reason int, alldone chan<- bool) {
	var done chan bool
	if alldone != nil {
		done = make(chan bool, 128)
	}

	count := 0
	h.topics.Range(func(name interface{}, t interface{}) bool {
		topic := t.(*Topic)
		if _, isMember := topic.perUser[uid]; (topic.cat != types.TopicCatGrp && isMember) ||
			topic.owner == uid {

			topic.markDeleted()

			h.topics.Delete(name)

			// This call is non-blocking unless some other routine tries to stop it at the same time.
			topic.exit <- &shutDown{reason: reason, done: done}

			// Just send to p2p topics here.
			if topic.cat == types.TopicCatP2P && len(topic.perUser) == 2 {
				presSingleUserOfflineOffline(topic.p2pOtherUser(uid), uid.UserId(), "gone", nilPresParams, "")
			}
			count++
		}
		return true
	})

	statsInc("LiveTopics", -count)

	if alldone != nil {
		for i := 0; i < count; i++ {
			<-done
		}
		alldone <- true
	}
}

// replyOfflineTopicGetDesc reads a minimal topic Desc from the database.
// The requester may or maynot be subscribed to the topic.
func replyOfflineTopicGetDesc(sess *Session, msg *ClientComMessage) {
	now := types.TimeNow()
	desc := &MsgTopicDesc{}
	asUid := types.ParseUserId(msg.AsUser)
	topic := msg.RcptTo

	if strings.HasPrefix(topic, "grp") {
		stopic, err := store.Topics.Get(topic)
		if err != nil {
			log.Println("replyOfflineTopicGetDesc", err)
			sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
			return
		}
		if stopic == nil {
			sess.queueOut(ErrTopicNotFoundReply(msg, now))
			return
		}

		desc.CreatedAt = &stopic.CreatedAt
		desc.UpdatedAt = &stopic.UpdatedAt
		desc.Public = stopic.Public
		if stopic.Owner == msg.AsUser {
			desc.DefaultAcs = &MsgDefaultAcsMode{
				Auth: stopic.Access.Auth.String(),
				Anon: stopic.Access.Anon.String()}
		}

	} else {
		// 'me' and p2p topics
		uid := types.ZeroUid
		if strings.HasPrefix(topic, "usr") {
			// User specified as usrXXX
			uid = types.ParseUserId(topic)
			topic = asUid.P2PName(uid)
		} else if strings.HasPrefix(topic, "p2p") {
			// User specified as p2pXXXYYY
			uid1, uid2, _ := types.ParseP2P(topic)
			if uid1 == asUid {
				uid = uid2
			} else if uid2 == asUid {
				uid = uid1
			}
		}

		if uid.IsZero() {
			log.Println("replyOfflineTopicGetDesc: malformed p2p topic name")
			sess.queueOut(ErrMalformedReply(msg, now))
			return
		}

		suser, err := store.Users.Get(uid)
		if err != nil {
			sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
			return
		}
		if suser == nil {
			sess.queueOut(ErrUserNotFoundReply(msg, now))
			return
		}
		desc.CreatedAt = &suser.CreatedAt
		desc.UpdatedAt = &suser.UpdatedAt
		desc.Public = suser.Public
		if sess.authLvl == auth.LevelRoot {
			desc.State = suser.State.String()
		}
	}

	sub, err := store.Subs.Get(topic, asUid)
	if err != nil {
		log.Println("replyOfflineTopicGetDesc:", err)
		sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
		return
	}

	if sub != nil && sub.DeletedAt == nil {
		desc.Private = sub.Private
		// FIXME: suspended topics should get no AW access.
		desc.Acs = &MsgAccessMode{
			Want:  sub.ModeWant.String(),
			Given: sub.ModeGiven.String(),
			Mode:  (sub.ModeGiven & sub.ModeWant).String()}
	}

	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{Id: msg.Id, Topic: msg.Original, Timestamp: &now, Desc: desc}})
}

// replyOfflineTopicGetSub reads user's subscription from the database.
// Only own subscription is available.
// The requester must be subscribed but need not be attached.
func replyOfflineTopicGetSub(sess *Session, msg *ClientComMessage) {
	now := types.TimeNow()

	if msg.Get.Sub != nil && msg.Get.Sub.User != "" && msg.Get.Sub.User != msg.AsUser {
		sess.queueOut(ErrPermissionDeniedReply(msg, now))
		return
	}

	ssub, err := store.Subs.Get(msg.RcptTo, types.ParseUserId(msg.AsUser))
	if err != nil {
		log.Println("replyOfflineTopicGetSub:", err)
		sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
		return
	}

	if ssub == nil {
		sess.queueOut(ErrNotFound(msg.Id, msg.Original, now, msg.Timestamp))
		return
	}

	sub := MsgTopicSub{}
	if ssub.DeletedAt == nil {
		sub.UpdatedAt = &ssub.UpdatedAt
		sub.Acs = MsgAccessMode{
			Want:  ssub.ModeWant.String(),
			Given: ssub.ModeGiven.String(),
			Mode:  (ssub.ModeGiven & ssub.ModeWant).String()}
		// Fnd is asymmetric: desc.private is a string, but sub.private is a []string.
		if types.GetTopicCat(msg.RcptTo) != types.TopicCatFnd {
			sub.Private = ssub.Private
		}
		sub.User = types.ParseUid(ssub.User).UserId()

		if (ssub.ModeGiven & ssub.ModeWant).IsReader() && (ssub.ModeWant & ssub.ModeGiven).IsJoiner() {
			sub.DelId = ssub.DelId
			sub.ReadSeqId = ssub.ReadSeqId
			sub.RecvSeqId = ssub.RecvSeqId
		}
	}

	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{Id: msg.Id, Topic: msg.Original, Timestamp: &now, Sub: []MsgTopicSub{sub}}})
}

// replyOfflineTopicSetSub updates Desc.Private and Sub.Mode when the topic is not loaded in memory.
// Only Private and Mode are updated and only for the requester. The requester must be subscribed to the
// topic but does not need to be attached.
func replyOfflineTopicSetSub(sess *Session, msg *ClientComMessage) {
	now := types.TimeNow()

	if (msg.Set.Desc == nil || msg.Set.Desc.Private == nil) && (msg.Set.Sub == nil || msg.Set.Sub.Mode == "") {
		sess.queueOut(InfoNotModifiedReply(msg, now))
		return
	}

	if msg.Set.Sub != nil && msg.Set.Sub.User != "" && msg.Set.Sub.User != msg.AsUser {
		sess.queueOut(ErrPermissionDeniedReply(msg, now))
		return
	}

	asUid := types.ParseUserId(msg.AsUser)

	sub, err := store.Subs.Get(msg.RcptTo, asUid)
	if err != nil {
		log.Println("replyOfflineTopicSetSub get sub:", err)
		sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
		return
	}

	if sub == nil || sub.DeletedAt != nil {
		sess.queueOut(ErrNotFound(msg.Id, msg.Original, now, msg.Timestamp))
		return
	}

	update := make(map[string]interface{})
	if msg.Set.Desc != nil && msg.Set.Desc.Private != nil {
		private, ok := msg.Set.Desc.Private.(map[string]interface{})
		if !ok {
			update = map[string]interface{}{"Private": msg.Set.Desc.Private}
		} else if private, changed := mergeInterfaces(sub.Private, private); changed {
			update = map[string]interface{}{"Private": private}
		}
	}

	if msg.Set.Sub != nil && msg.Set.Sub.Mode != "" {
		var modeWant types.AccessMode
		if err = modeWant.UnmarshalText([]byte(msg.Set.Sub.Mode)); err != nil {
			log.Println("replyOfflineTopicSetSub mode:", err)
			sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
			return
		}

		if modeWant.IsOwner() != sub.ModeWant.IsOwner() {
			// No ownership changes here.
			sess.queueOut(ErrPermissionDeniedReply(msg, now))
			return
		}

		if types.GetTopicCat(msg.RcptTo) == types.TopicCatP2P {
			// For P2P topics ignore requests exceeding types.ModeCP2P and do not allow
			// removal of 'A' permission.
			modeWant = modeWant&types.ModeCP2P | types.ModeApprove
		}

		if modeWant != sub.ModeWant {
			update["ModeWant"] = modeWant
			// Cache it for later use
			sub.ModeWant = modeWant
		}
	}

	if len(update) > 0 {
		err = store.Subs.Update(msg.RcptTo, asUid, update, true)
		if err != nil {
			log.Println("replyOfflineTopicSetSub update:", err)
			sess.queueOut(decodeStoreErrorExplicitTs(err, msg.Id, msg.Original, now, msg.Timestamp, nil))
		} else {
			var params interface{}
			if update["ModeWant"] != nil {
				params = map[string]interface{}{"acs": MsgAccessMode{
					Given: sub.ModeGiven.String(),
					Want:  sub.ModeWant.String(),
					Mode:  (sub.ModeGiven & sub.ModeWant).String()}}
			}
			sess.queueOut(NoErrParamsReply(msg, now, params))
		}
	} else {
		sess.queueOut(InfoNotModifiedReply(msg, now))
	}
}
