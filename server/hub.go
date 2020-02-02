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
	"container/list"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Request to hub to subscribe session to topic
type sessionJoin struct {
	// Routable (expanded) name of the topic to subscribe to.
	topic string
	// Packet, containing request details.
	pkt *ClientComMessage
	// Session to subscribe.
	sess *Session
	// True if this topic was just created.
	// In case of p2p topics, it's true if the other user's subscription was
	// created (as a part of new topic creation or just alone).
	created bool
	// True if this is a new subscription.
	newsub bool
	// True if this topic is created internally.
	internal bool
}

// Session wants to leave the topic
type sessionLeave struct {
	// User ID of the user sent the request
	userId types.Uid
	// Topic to report success of failure on
	topic string
	// Session which initiated the request
	sess *Session
	// Leave and unsubscribe
	unsub bool
	// ID of originating request, if any
	id string
}

// Request to hub to remove the topic
type topicUnreg struct {
	// Routable name of the topic to drop
	topic string
	// UID of the user being deleted
	forUser types.Uid
	// Session making the request, could be nil
	sess *Session
	// Original request, could be nil
	pkt *ClientComMessage
	// Unregister then delete the topic
	del bool
	// Channel for reporting operation completion when deleting topics for a user
	done chan<- bool
}

type metaReq struct {
	// Routable name of the topic to update or query. Either topic or forUser must be defined.
	topic string
	// UID of the user being affected. Either topic or forUser must be defined.
	forUser types.Uid
	// Packet containing details of the Get/Set/Del request.
	pkt *ClientComMessage
	// Session which originated the request.
	sess *Session
	// What is being requested: constMsgMetaSub, constMsgMetaDesc, constMsgMetaTags, etc.
	what int
	// New topic state value. Only types.StateSuspended is supported at this time.
	state types.ObjState
}

// Hub is the core structure which holds topics.
type Hub struct {

	// Topics must be indexed by name
	topics *sync.Map

	// Channel for routing messages between topics, buffered at 4096
	route chan *ServerComMessage

	// subscribe session to topic, possibly creating a new topic, unbuffered
	join chan *sessionJoin

	// Remove topic from hub, possibly deleting it afterwards, unbuffered
	unreg chan *topicUnreg

	// Cluster request to rehash topics, unbuffered
	rehash chan bool

	// Process get.info requests for topic not subscribed to, buffered 128
	meta chan *metaReq

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
		topics: &sync.Map{}, //make(map[string]*Topic),
		// this needs to be buffered - hub generates invites and adds them to this queue
		route:    make(chan *ServerComMessage, 4096),
		join:     make(chan *sessionJoin),
		unreg:    make(chan *topicUnreg),
		rehash:   make(chan bool),
		meta:     make(chan *metaReq, 128),
		shutdown: make(chan chan<- bool),
	}

	statsRegisterInt("LiveTopics")
	statsRegisterInt("TotalTopics")

	go h.run()

	if !globals.cluster.isRemoteTopic("sys") {
		// Initialize system 'sys' topic. There is only one sys topic per cluster.
		h.join <- &sessionJoin{topic: "sys", internal: true, pkt: &ClientComMessage{topic: "sys"}}
	}

	return h
}

func (h *Hub) run() {

	for {
		select {
		case sreg := <-h.join:
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
			t := h.topicGet(sreg.topic)
			if t == nil {
				// Topic does not exist or not loaded.
				t = &Topic{name: sreg.topic,
					xoriginal: sreg.pkt.topic,
					sessions:  make(map[*Session]perSessionData),
					broadcast: make(chan *ServerComMessage, 256),
					reg:       make(chan *sessionJoin, 32),
					unreg:     make(chan *sessionLeave, 32),
					meta:      make(chan *metaReq, 32),
					defrNotif: new(list.List),
					perUser:   make(map[types.Uid]perUserData),
					exit:      make(chan *shutDown, 1),
				}
				// Topic is created in suspended state because it's not yet configured.
				t.markPaused(true)
				// Save topic now to prevent race condition.
				h.topicPut(sreg.topic, t)

				// Configure the topic.
				go topicInit(t, sreg, h)

			} else {
				// Topic found.
				// Topic will check access rights and send appropriate {ctrl}
				t.reg <- sreg
			}

		case msg := <-h.route:
			// This is a message from a connection not subscribed to topic
			// Route incoming message to topic if topic permits such routing

			if dst := h.topicGet(msg.rcptto); dst != nil {
				// Everything is OK, sending packet to known topic
				if dst.broadcast != nil {
					select {
					case dst.broadcast <- msg:
					default:
						log.Println("hub: topic's broadcast queue is full", dst.name)
					}
				}
			} else if (strings.HasPrefix(msg.rcptto, "usr") || strings.HasPrefix(msg.rcptto, "grp")) && globals.cluster.isRemoteTopic(msg.rcptto) {
				// It is a remote topic.
				if err := globals.cluster.routeToTopicIntraCluster(msg.rcptto, msg); err != nil {
					log.Printf("hub: routing to '%s' failed", msg.rcptto)
				}
			} else if msg.Pres == nil && msg.Info == nil {
				// Topic is unknown or offline.
				// Pres & Info are silently ignored, all other messages are reported as invalid.

				// TODO(gene): validate topic name, discarding invalid topics

				log.Printf("Hub. Topic[%s] is unknown or offline", msg.rcptto)

				msg.sess.queueOut(NoErrAccepted(msg.id, msg.rcptto, types.TimeNow()))
			}

		case meta := <-h.meta:
			// Suspend/activate user's topics
			if !meta.forUser.IsZero() {
				go h.topicsStateForUser(meta.forUser, meta.state == types.StateSuspended)
			} else {
				// Metadata read or update from a user who is not attached to the topic.
				if dst := h.topicGet(meta.topic); dst != nil {
					// If topic is already in memory, pass request to the topic.
					dst.meta <- meta
				} else {
					// Topic is not in memory. Read or update the DB record and reply here.
					if meta.pkt.Get != nil {
						if meta.what == constMsgMetaDesc {
							go replyOfflineTopicGetDesc(meta.sess, meta.topic, meta.pkt)
						} else {
							go replyOfflineTopicGetSub(meta.sess, meta.topic, meta.pkt)
						}
					} else if meta.pkt.Set != nil {
						go replyOfflineTopicSetSub(meta.sess, meta.topic, meta.pkt)
					}
				}
			}

		case unreg := <-h.unreg:
			reason := StopNone
			if unreg.del {
				reason = StopDeleted
			}
			if unreg.forUser.IsZero() {
				// The topic is being garbage collected or deleted.
				if err := h.topicUnreg(unreg.sess, unreg.topic, unreg.pkt, reason); err != nil {
					log.Println("hub.topicUnreg failed:", err)
				}
			} else {
				go h.stopTopicsForUser(unreg.forUser, reason, unreg.done)
			}

		case <-h.rehash:
			// Cluster rehashing. Some previously local topics became remote.
			// Such topics must be shut down at this node.
			h.topics.Range(func(_, t interface{}) bool {
				topic := t.(*Topic)
				if globals.cluster.isRemoteTopic(topic.name) {
					h.topicUnreg(nil, topic.name, nil, StopRehashing)
				}
				return true
			})

			// Check if 'sys' topic has migrated to this node.
			if h.topicGet("sys") == nil && !globals.cluster.isRemoteTopic("sys") {
				// Yes, 'sys' has migrated here. Initialize it.
				// The h.join is unbuffered. We must call from another goroutine. Otherwise deadlock.
				go func() {
					h.join <- &sessionJoin{topic: "sys", internal: true, pkt: &ClientComMessage{topic: "sys"}}
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
		if topic.cat == types.TopicCatMe || topic.cat == types.TopicCatGrp {
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
		asUid := types.ParseUserId(msg.from)
		// Case 1 (unregister and delete)
		if t := h.topicGet(topic); t != nil {
			// Case 1.1: topic is online
			if t.owner == asUid || (t.cat == types.TopicCatP2P && t.subsCount() < 2) {
				// Case 1.1.1: requester is the owner or last sub in a p2p topic

				t.markPaused(true)
				if err := store.Topics.Delete(topic, true); err != nil {
					t.markPaused(false)
					sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
					return err
				}

				sess.queueOut(NoErr(msg.id, msg.topic, now))

				h.topicDel(topic)
				t.markDeleted()
				t.exit <- &shutDown{reason: StopDeleted}
				statsInc("LiveTopics", -1)
			} else {
				// Case 1.1.2: requester is NOT the owner
				t.meta <- &metaReq{
					topic: topic,
					pkt:   msg,
					sess:  sess,
					what:  constMsgDelTopic}
			}

		} else {
			// Case 1.2: topic is offline.

			asUid := types.ParseUserId(msg.from)
			// Get all subscribers: we need to know how many are left and notify them.
			subs, err := store.Topics.GetSubs(topic, nil)
			if err != nil {
				sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
				return err
			}

			if len(subs) == 0 {
				sess.queueOut(InfoNoAction(msg.id, msg.topic, now))
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
				sess.queueOut(InfoNoAction(msg.id, msg.topic, now))
				return nil
			}

			tcat := topicCat(topic)
			if !(sub.ModeGiven & sub.ModeWant).IsOwner() {
				// Case 1.2.2.1 Not the owner, but possibly last subscription in a P2P topic.

				if tcat == types.TopicCatP2P && len(subs) < 2 {
					// This is a P2P topic and fewer than 2 subscriptions, delete the entire topic
					if err := store.Topics.Delete(topic, true); err != nil {
						sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
						return err
					}

				} else if err := store.Subs.Delete(topic, asUid); err != nil {
					// Not P2P or more than 1 subscription left.
					// Delete user's own subscription only
					if err == types.ErrNotFound {
						sess.queueOut(InfoNoAction(msg.id, msg.topic, now))
						err = nil
					} else {
						sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
					}
					return err
				}

				// Notify user's other sessions that the subscription is gone
				presSingleUserOfflineOffline(asUid, msg.topic, "gone", nilPresParams, sess.sid)
				if tcat == types.TopicCatP2P && len(subs) == 2 {
					uname1 := asUid.UserId()
					uid2 := types.ParseUserId(msg.topic)
					// Tell user1 to stop sending updates to user2 without passing change to user1's sessions.
					presSingleUserOfflineOffline(asUid, uid2.UserId(), "?none+rem", nilPresParams, "")
					// Don't change the online status of user1, just ask user2 to stop notification exchange.
					// Tell user2 that user1 is offline but let him keep sending updates in case user1 resubscribes.
					presSingleUserOfflineOffline(uid2, uname1, "off", nilPresParams, "")
				}

			} else {
				// Case 1.2.1.1: owner, delete the group topic from db.
				// Only group topics have owners.
				if err := store.Topics.Delete(topic, true); err != nil {
					sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
					return err
				}

				// Notify subscribers that the group topic is gone.
				presSubsOfflineOffline(msg.topic, tcat, subs, "gone", &presParams{}, sess.sid)
			}

			sess.queueOut(NoErr(msg.id, msg.topic, now))
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
			sess.queueOut(NoErr(msg.id, msg.topic, now))
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
func replyOfflineTopicGetDesc(sess *Session, topic string, msg *ClientComMessage) {
	now := types.TimeNow()
	desc := &MsgTopicDesc{}
	asUid := types.ParseUserId(msg.from)

	if strings.HasPrefix(topic, "grp") {
		stopic, err := store.Topics.Get(topic)
		if err != nil {
			log.Println("replyOfflineTopicGetDesc", err)
			sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
			return
		}
		if stopic == nil {
			sess.queueOut(ErrTopicNotFound(msg.id, msg.topic, now))
			return
		}

		desc.CreatedAt = &stopic.CreatedAt
		desc.UpdatedAt = &stopic.UpdatedAt
		desc.Public = stopic.Public
		if stopic.Owner == msg.from {
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
			sess.queueOut(ErrMalformed(msg.id, msg.topic, now))
			return
		}

		suser, err := store.Users.Get(uid)
		if err != nil {
			sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
			return
		}
		if suser == nil {
			sess.queueOut(ErrUserNotFound(msg.id, msg.topic, now))
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
		sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
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
		Meta: &MsgServerMeta{Id: msg.id, Topic: msg.topic, Timestamp: &now, Desc: desc}})
}

// replyOfflineTopicGetSub reads user's subscription from the database.
// Only own subscription is available.
// The requester must be subscribed but need not be attached.
func replyOfflineTopicGetSub(sess *Session, topic string, msg *ClientComMessage) {
	now := types.TimeNow()

	if msg.Get.Sub != nil && msg.Get.Sub.User != "" && msg.Get.Sub.User != msg.from {
		sess.queueOut(ErrPermissionDenied(msg.id, msg.topic, now))
		return
	}

	ssub, err := store.Subs.Get(topic, types.ParseUserId(msg.from))
	if err != nil {
		log.Println("replyOfflineTopicGetSub:", err)
		sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
		return
	}

	if ssub == nil {
		sess.queueOut(ErrNotFound(msg.id, msg.topic, now))
		return
	}

	sub := MsgTopicSub{}
	if ssub.DeletedAt == nil {
		sub.UpdatedAt = &ssub.UpdatedAt
		sub.Acs = MsgAccessMode{
			Want:  ssub.ModeWant.String(),
			Given: ssub.ModeGiven.String(),
			Mode:  (ssub.ModeGiven & ssub.ModeWant).String()}
		sub.Private = ssub.Private
		sub.User = ssub.User

		if (ssub.ModeGiven & ssub.ModeWant).IsReader() && (ssub.ModeWant & ssub.ModeGiven).IsJoiner() {
			sub.DelId = ssub.DelId
			sub.ReadSeqId = ssub.ReadSeqId
			sub.RecvSeqId = ssub.RecvSeqId
		}
	}

	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{Id: msg.id, Topic: msg.topic, Timestamp: &now, Sub: []MsgTopicSub{sub}}})
}

// replyOfflineTopicSetSub updates Desc.Private and Sub.Mode when the topic is not loaded in memory.
// Only Private and Mode are updated and only for the requester. The requester must be subscribed to the
// topic but does not need to be attached.
func replyOfflineTopicSetSub(sess *Session, topic string, msg *ClientComMessage) {
	now := types.TimeNow()

	if (msg.Set.Desc == nil || msg.Set.Desc.Private == nil) && (msg.Set.Sub == nil || msg.Set.Sub.Mode == "") {
		sess.queueOut(InfoNotModified(msg.id, msg.topic, now))
		return
	}

	if msg.Set.Sub != nil && msg.Set.Sub.User != "" && msg.Set.Sub.User != msg.from {
		sess.queueOut(ErrPermissionDenied(msg.id, msg.topic, now))
		return
	}

	asUid := types.ParseUserId(msg.from)

	sub, err := store.Subs.Get(topic, asUid)
	if err != nil {
		log.Println("replyOfflineTopicSetSub get sub:", err)
		sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
		return
	}

	if sub == nil || sub.DeletedAt != nil {
		sess.queueOut(ErrNotFound(msg.id, msg.topic, now))
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
			sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
			return
		}

		if modeWant.IsOwner() != sub.ModeWant.IsOwner() {
			// No ownership changes here.
			sess.queueOut(ErrPermissionDenied(msg.id, msg.topic, now))
			return
		}

		if types.GetTopicCat(topic) == types.TopicCatP2P {
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
		err = store.Subs.Update(topic, asUid, update, true)
		if err != nil {
			log.Println("replyOfflineTopicSetSub update:", err)
			sess.queueOut(decodeStoreError(err, msg.id, msg.topic, now, nil))
		} else {
			var params interface{}
			if update["ModeWant"] != nil {
				params = map[string]interface{}{"acs": MsgAccessMode{
					Given: sub.ModeGiven.String(),
					Want:  sub.ModeWant.String(),
					Mode:  (sub.ModeGiven & sub.ModeWant).String()}}
			}
			sess.queueOut(NoErrParams(msg.id, msg.topic, now, params))
		}
	} else {
		sess.queueOut(InfoNotModified(msg.id, msg.topic, now))
	}
}
