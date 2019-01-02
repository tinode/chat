/******************************************************************************
 *
 *  Description :
 *
 *    Create/tear down conversation topics, route messages between topics.
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

// Request to hub to subscribe session to topic
type sessionJoin struct {
	// Routable (expanded) name of the topic to subscribe to
	topic string
	// Packet, containing request details
	pkt *ClientComMessage
	// Session to subscribe
	sess *Session
	// True if this topic was just created.
	// In case of p2p topics, it's true if the other user's subscription was
	// created (as a part of new topic creation or just alone).
	created bool
	// True if the topic was just activated (loaded into memory)
	loaded bool
	// True if this is a new subscription
	newsub bool
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
	reqID string
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
	// Routable name of the topic to get info for
	topic string
	// packet containing details of the Get/Set/Del request
	pkt *ClientComMessage
	// Session which originated the request
	sess *Session
	// what is being requested, constMsgGetInfo, constMsgGetSub, constMsgGetData
	what int
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

	// Flag for indicating that system shutdown is in progress
	isShutdownInProgress bool
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

			t := h.topicGet(sreg.topic) // is the topic already loaded?
			if t == nil {
				// Topic does not exist or not loaded
				go topicInit(sreg, h)
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
			} else if msg.Pres == nil && msg.Info == nil {
				// Topic is unknown or offline.
				// Pres & Info are silently ignored, all other messages are reported as invalid.

				// TODO(gene): validate topic name, discarding invalid topics

				log.Printf("Hub. Topic[%s] is unknown or offline", msg.rcptto)

				msg.sess.queueOut(NoErrAccepted(msg.id, msg.rcptto, types.TimeNow()))
			}

		case meta := <-h.meta:
			// Request for topic info from a user who is not subscribed to the topic
			if dst := h.topicGet(meta.topic); dst != nil {
				// If topic is already in memory, pass request to topic
				dst.meta <- meta
			} else if meta.pkt.Get != nil {
				// If topic is not in memory, fetch requested description from DB and reply here
				go replyTopicDescBasic(meta.sess, meta.topic, meta.pkt)
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
			h.topics.Range(func(_, t interface{}) bool {
				topic := t.(*Topic)
				if globals.cluster.isRemoteTopic(topic.name) {
					h.topicUnreg(nil, topic.name, nil, StopRehashing)
				}
				return true
			})

		case hubdone := <-h.shutdown:
			// mark immediately to prevent more topics being added to hub.topics
			h.isShutdownInProgress = true

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

// topicInit reads an existing topic from database or creates a new topic
func topicInit(sreg *sessionJoin, h *Hub) {
	var t *Topic

	timestamp := types.TimeNow()
	pktsub := sreg.pkt.Sub

	t = &Topic{name: sreg.topic,
		xoriginal: sreg.pkt.topic,
		sessions:  make(map[*Session]types.UidSlice),
		broadcast: make(chan *ServerComMessage, 256),
		reg:       make(chan *sessionJoin, 32),
		unreg:     make(chan *sessionLeave, 32),
		meta:      make(chan *metaReq, 32),
		perUser:   make(map[types.Uid]perUserData),
		exit:      make(chan *shutDown, 1),
	}

	// Helper function to parse access mode from string, handling errors and setting default value
	parseMode := func(modeString string, defaultMode types.AccessMode) types.AccessMode {
		mode := defaultMode
		if err := mode.UnmarshalText([]byte(modeString)); err != nil {
			log.Println("hub: invalid access mode for topic[" + t.xoriginal + "]: '" + modeString + "'")
		}

		return mode
	}

	// Request to load a 'me' topic. The topic always exists, the subscription is never new.
	if t.xoriginal == "me" {

		t.cat = types.TopicCatMe

		// 'me' has no owner, t.owner = nil
		user, err := store.Users.Get(sreg.sess.uid)
		if err != nil {
			log.Println("hub: cannot load 'me' user object", t.name, err)
			// Log out the session
			sreg.sess.uid = types.ZeroUid
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		} else if user == nil {
			log.Println("hub: user's account not found", t.name)
			// Log out the session
			// FIXME: this is a race condition
			sreg.sess.uid = types.ZeroUid
			sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		// User's default access for p2p topics
		t.accessAuth = user.Access.Auth
		t.accessAnon = user.Access.Anon

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		t.public = user.Public

		t.created = user.CreatedAt
		t.updated = user.UpdatedAt

		// The following values are exlicitly not set for 'me'.
		// t.touched, t.lastId, t.delId

		// Initiate User Agent with the UA of the creating session to report it later
		t.userAgent = sreg.sess.userAgent
		// Initialize channel for receiving user agent updates
		t.uaChange = make(chan string, 32)
		// Allocate storage for contacts.
		t.perSubs = make(map[string]perSubsData)

		// Request to load a 'find' topic. The topic always exists, the subscription is never new.
	} else if t.xoriginal == "fnd" {

		t.cat = types.TopicCatFnd

		// 'fnd' has no owner, t.owner = nil

		user, err := store.Users.Get(sreg.sess.uid)
		if err != nil {
			log.Println("hub: cannot load user object for 'fnd'='" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		} else if user == nil {
			log.Println("hub: user's account unexpectedly not found (deleted?)")
			// FIXME: this is a race condition
			sreg.sess.uid = types.ZeroUid
			sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		// Make sure no one can join the topic.
		t.accessAuth = getDefaultAccess(t.cat, true)
		t.accessAnon = getDefaultAccess(t.cat, false)

		// Assign tags
		t.tags = user.Tags

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		t.created = user.CreatedAt
		t.updated = user.UpdatedAt

		// Publishing to fnd is not supported
		// t.lastId = 0, t.delId = 0, t.touched = nil

		// Request to load an existing or create a new p2p topic, then attach to it.
	} else if strings.HasPrefix(t.xoriginal, "usr") || strings.HasPrefix(t.xoriginal, "p2p") {

		// Handle the following cases:
		// 1. Neither topic nor subscriptions exist: create a new p2p topic & subscriptions.
		// 2. Topic exists, one of the subscriptions is missing:
		// 2.1 Requester's subscription is missing, recreate it.
		// 2.2 Other user's subscription is missing, treat like a new request for user 2.
		// 3. Topic exists, both subscriptions are missing: should not happen, fail.
		// 4. Topic and both subscriptions exist: attach to topic

		t.cat = types.TopicCatP2P

		// Check if the topic already exists
		stopic, err := store.Topics.Get(t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		// If topic exists, load subscriptions
		var subs []types.Subscription
		if stopic != nil {
			// Subs already have Public swapped
			if subs, err = store.Topics.GetUsers(t.name, nil); err != nil {
				log.Println("hub: cannot load subscriptions for '" + t.name + "' (" + err.Error() + ")")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
				return
			}

			// Case 3, fail
			if len(subs) == 0 {
				log.Println("hub: missing both subscriptions for '" + t.name + "' (SHOULD NEVER HAPPEN!)")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
				return
			}

			t.created = stopic.CreatedAt
			t.updated = stopic.UpdatedAt
			if stopic.TouchedAt != nil {
				t.touched = *stopic.TouchedAt
			}
			t.lastID = stopic.SeqId
			t.delID = stopic.DelId
		}

		// t.owner is blank for p2p topics

		// Default user access to P2P topics is not set because it's unused.
		// Other users cannot join the topic because of how topic name is constructed.
		// The two participants set each other's access instead.
		// t.accessAuth = getDefaultAccess(t.cat, true)
		// t.accessAnon = getDefaultAccess(t.cat, false)

		// t.public is not used for p2p topics since each user get a different public

		if stopic != nil && len(subs) == 2 {
			// Case 4.
			for i := 0; i < 2; i++ {

				uid := types.ParseUid(subs[i].User)
				t.perUser[uid] = perUserData{
					// Adapter already swapped the public values
					public:    subs[i].GetPublic(),
					topicName: types.ParseUid(subs[(i+1)%2].User).UserId(),

					private:   subs[i].Private,
					modeWant:  subs[i].ModeWant,
					modeGiven: subs[i].ModeGiven,
					delID:     subs[i].DelId,
					recvID:    subs[i].RecvSeqId,
					readID:    subs[i].ReadSeqId,
				}
			}

		} else {
			// Cases 1 (new topic), 2 (one of the two subscriptions is missing: either it's a new request
			// or the subscription was deleted)
			var userData perUserData

			// Fetching records for both users.
			// Requester.
			userID1 := types.ParseUserId(sreg.pkt.from)
			// The other user.
			userID2 := types.ParseUserId(t.xoriginal)
			// User index: u1 - requester, u2 - responder, the other user

			var u1, u2 int
			users, err := store.Users.GetAll(userID1, userID2)
			if err != nil {
				log.Println("hub: failed to load users for '" + t.name + "' (" + err.Error() + ")")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
				return
			}
			if len(users) != 2 {
				// Invited user does not exist
				log.Println("hub: missing user for '" + t.name + "'")
				sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.id, t.xoriginal, timestamp))
				return
			}
			// User records are unsorted, make sure we know who is who.
			if users[0].Uid() == userID1 {
				u1, u2 = 0, 1
			} else {
				u1, u2 = 1, 0
			}

			// Figure out which subscriptions are missing: User1's, User2's or both.
			var sub1, sub2 *types.Subscription
			// Set to true if only requester's subscription has to be created.
			var user1only bool
			if len(subs) == 1 {
				if subs[0].User == userID1.String() {
					// User2's subscription is missing, user1's exists
					sub1 = &subs[0]
				} else {
					// User1's is missing, user2's exists
					sub2 = &subs[0]
					user1only = true
				}
			}

			// Other user's (responder's) subscription is missing
			if sub2 == nil {
				sub2 = &types.Subscription{
					User:    userID2.String(),
					Topic:   t.name,
					Private: nil}

				// Assign user2's ModeGiven based on what user1 has provided.
				// We don't know access mode for user2, assume it's Auth.
				if pktsub.Set != nil && pktsub.Set.Desc != nil && pktsub.Set.Desc.DefaultAcs != nil {
					// Use provided DefaultAcs as non-default modeGiven for the other user.
					// The other user is assumed to have auth level "Auth".
					sub2.ModeGiven = parseMode(pktsub.Set.Desc.DefaultAcs.Auth, users[u1].Access.Auth) &
						types.ModeCP2P
				} else {
					// Use user1.Auth as modeGiven for the other user
					sub2.ModeGiven = users[u1].Access.Auth
				}

				// Swap Public to match swapped Public in subs returned from store.Topics.GetSubs
				sub2.SetPublic(users[u1].Public)

				// Mark the entire topic as new.
				sreg.created = true
			}

			// Requester's subscription is missing:
			// a. requester is starting a new topic
			// b. requester's subscription is missing: deleted or creation failed
			if sub1 == nil {
				// Set user1's ModeGiven from user2's default values
				userData.modeGiven = selectAccessMode(auth.Level(sreg.pkt.authLvl),
					users[u2].Access.Anon,
					users[u2].Access.Auth,
					types.ModeCP2P)

				// By default assign the same mode that user1 gave to user2 (could be changed below)
				userData.modeWant = sub2.ModeGiven

				if pktsub.Set != nil {
					if pktsub.Set.Sub != nil {
						uid := userID1
						if pktsub.Set.Sub.User != "" {
							uid = types.ParseUserId(pktsub.Set.Sub.User)
						}

						if uid != userID1 {
							// Report the error and ignore the value
							log.Println("hub: setting mode for another user is not supported '" + t.name + "'")
						} else {
							// user1 is setting non-default modeWant
							userData.modeWant = parseMode(pktsub.Set.Sub.Mode, userData.modeWant) &
								types.ModeCP2P
						}

						// Since user1 issued a {sub} request, make sure the user can join
						userData.modeWant |= types.ModeJoin
					}

					// user1 sets non-default Private
					if pktsub.Set.Desc != nil {
						if !isNullValue(pktsub.Set.Desc.Private) {
							userData.private = pktsub.Set.Desc.Private
						}
						// Public, if present, is ignored
					}
				}

				sub1 = &types.Subscription{
					User:      userID1.String(),
					Topic:     t.name,
					ModeWant:  userData.modeWant,
					ModeGiven: userData.modeGiven,
					Private:   userData.private}
				// Swap Public to match swapped Public in subs returned from store.Topics.GetSubs
				sub1.SetPublic(users[u2].Public)

				// Mark this subscription as new
				sreg.newsub = true
			}

			if !user1only {
				// sub2 is being created, assign sub2.modeWant to what user2 gave to user1 (sub1.modeGiven)
				sub2.ModeWant = selectAccessMode(auth.Level(sreg.pkt.authLvl),
					users[u2].Access.Anon,
					users[u2].Access.Auth,
					types.ModeCP2P)
			}

			// Create everything
			if stopic == nil {
				if err = store.Topics.CreateP2P(sub1, sub2); err != nil {
					log.Println("hub: databse error in creating subscriptions '" + t.name + "' (" + err.Error() + ")")
					sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
					return
				}

				t.created = sub1.CreatedAt
				t.updated = sub1.UpdatedAt
				t.touched = t.updated

				// t.lastId is not set (default 0) for new topics

			} else {
				// TODO possibly update subscription, if changed

				// Recreate one of the subscriptions
				var subToMake *types.Subscription
				if user1only {
					subToMake = sub1
				} else {
					subToMake = sub2
				}
				if err = store.Subs.Create(subToMake); err != nil {
					log.Println("hub: databse error in re-subscribing user '" + t.name + "' (" + err.Error() + ")")
					sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
					return
				}
			}

			// Publics is already swapped
			userData.public = sub1.GetPublic()
			userData.topicName = userID2.UserId()
			userData.modeWant = sub1.ModeWant
			userData.modeGiven = sub1.ModeGiven
			userData.delID = sub1.DelId
			userData.readID = sub1.ReadSeqId
			userData.recvID = sub1.RecvSeqId
			t.perUser[userID1] = userData

			t.perUser[userID2] = perUserData{
				public:    sub2.GetPublic(),
				topicName: userID1.UserId(),
				modeWant:  sub2.ModeWant,
				modeGiven: sub2.ModeGiven,
				delID:     sub2.DelId,
				readID:    sub2.ReadSeqId,
				recvID:    sub2.RecvSeqId,
			}
		}

		// Clear original topic name.
		t.xoriginal = ""

		// Processing request to create a new generic (group) topic:
	} else if strings.HasPrefix(t.xoriginal, "new") {

		t.cat = types.TopicCatGrp

		// Generic topics have parameters stored in the topic object
		t.owner = types.ParseUserId(sreg.pkt.from)

		t.accessAuth = getDefaultAccess(t.cat, true)
		t.accessAnon = getDefaultAccess(t.cat, false)

		// Owner/creator gets full access to the topic. Owner may change the default modeWant through 'set'.
		userData := perUserData{
			modeGiven: types.ModeCFull,
			modeWant:  types.ModeCFull}

		var tags []string
		if pktsub.Set != nil {
			// User sent initialization parameters
			if pktsub.Set.Desc != nil {
				if !isNullValue(pktsub.Set.Desc.Public) {
					t.public = pktsub.Set.Desc.Public
				}
				if !isNullValue(pktsub.Set.Desc.Private) {
					userData.private = pktsub.Set.Desc.Private
				}

				// set default access
				if pktsub.Set.Desc.DefaultAcs != nil {
					if authMode, anonMode, err := parseTopicAccess(pktsub.Set.Desc.DefaultAcs,
						t.accessAuth, t.accessAnon); err != nil {

						// Invalid access for one or both. Make it explicitly None
						if authMode.IsInvalid() {
							t.accessAuth = types.ModeNone
						} else {
							t.accessAuth = authMode
						}
						if anonMode.IsInvalid() {
							t.accessAnon = types.ModeNone
						} else {
							t.accessAnon = anonMode
						}
						log.Println("hub: invalid access mode for topic '" + t.name + "': '" + err.Error() + "'")
					} else if authMode.IsOwner() || anonMode.IsOwner() {
						log.Println("hub: OWNER default access in topic '" + t.name)
						t.accessAuth, t.accessAnon = authMode & ^types.ModeOwner, anonMode & ^types.ModeOwner
					} else {
						t.accessAuth, t.accessAnon = authMode, anonMode
					}
				}
			}

			// Owner/creator may restrict own access to topic
			if pktsub.Set.Sub != nil && pktsub.Set.Sub.Mode != "" {
				userData.modeWant = parseMode(pktsub.Set.Sub.Mode, types.ModeCFull)
				// User must not unset ModeJoin or the owner flags
				userData.modeWant |= types.ModeJoin | types.ModeOwner
			}

			tags = normalizeTags(pktsub.Set.Tags)
			if !restrictedTagsEqual(tags, nil, globals.immutableTagNS) {
				log.Println("hub: attempt to directly set restricted tags")
				sreg.sess.queueOut(ErrPermissionDenied(sreg.pkt.id, t.xoriginal, timestamp))
				return
			}
		}

		t.perUser[t.owner] = userData

		// Assign tags
		t.tags = tags

		t.created = timestamp
		t.updated = timestamp
		t.touched = timestamp

		// t.lastId & t.delId are not set for new topics

		stopic := &types.Topic{
			ObjHeader: types.ObjHeader{Id: sreg.topic, CreatedAt: timestamp},
			Access:    types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon},
			Tags:      tags,
			Public:    t.public}

		// store.Topics.Create will add a subscription record for the topic creator
		stopic.GiveAccess(t.owner, userData.modeWant, userData.modeGiven)
		err := store.Topics.Create(stopic, t.owner, t.perUser[t.owner].private)
		if err != nil {
			log.Println("hub: cannot save new topic '" + t.name + "' (" + err.Error() + ")")
			// Send the error on the original "newWHATEVER" topic.
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		t.xoriginal = t.name // keeping 'new' as original has no value to the client
		sreg.created = true
		sreg.newsub = true

	} else if strings.HasPrefix(t.xoriginal, "grp") {
		t.cat = types.TopicCatGrp

		// TODO(gene): check and validate topic name
		stopic, err := store.Topics.Get(t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		} else if stopic == nil {
			log.Println("hub: topic '" + t.name + "' does not exist")
			sreg.sess.queueOut(ErrTopicNotFound(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.id, t.xoriginal, timestamp))
			return
		}

		// t.owner is set by loadSubscriptions

		t.accessAuth = stopic.Access.Auth
		t.accessAnon = stopic.Access.Anon

		// Assign tags
		t.tags = stopic.Tags

		t.public = stopic.Public

		t.created = stopic.CreatedAt
		t.updated = stopic.UpdatedAt
		if stopic.TouchedAt != nil {
			t.touched = *stopic.TouchedAt
		}
		t.lastID = stopic.SeqId
		t.delID = stopic.DelId

	} else {
		// Unrecognized topic name
		sreg.sess.queueOut(ErrTopicNotFound(sreg.pkt.id, t.xoriginal, timestamp))
		return
	}

	// prevent newly initialized topics to live while shutdown in progress
	if h.isShutdownInProgress {
		return
	}

	h.topicPut(t.name, t)

	statsInc("LiveTopics", 1)
	statsInc("TotalTopics", 1)

	go t.run(h)

	log.Println("hub: started", t.name, "created=", sreg.created)

	sreg.loaded = true
	// Topic will check access rights, send invite to p2p user, send {ctrl} message to the initiator session
	t.reg <- sreg
}

// loadSubscribers loads topic subscribers, sets topic owner
func (t *Topic) loadSubscribers() error {
	subs, err := store.Topics.GetSubs(t.name, nil)
	if err != nil {
		return err
	}

	if subs == nil {
		return nil
	}

	for i := range subs {
		sub := &subs[i]
		uid := types.ParseUid(sub.User)
		t.perUser[uid] = perUserData{
			created:   sub.CreatedAt,
			updated:   sub.UpdatedAt,
			delID:     sub.DelId,
			readID:    sub.ReadSeqId,
			recvID:    sub.RecvSeqId,
			private:   sub.Private,
			modeWant:  sub.ModeWant,
			modeGiven: sub.ModeGiven}

		if (sub.ModeGiven & sub.ModeWant).IsOwner() {
			t.owner = uid
		}
	}

	return nil
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

				t.suspend()

				if err := store.Topics.Delete(topic, true); err != nil {
					t.resume()
					sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
					return err
				}

				sess.queueOut(NoErr(msg.id, msg.topic, now))

				h.topicDel(topic)
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

					sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
					return err
				}

				// Notify user's other sessions that the subscription is gone
				presSingleUserOfflineOffline(asUid, msg.topic, "gone", nilPresParams, sess.sid)
				if tcat == types.TopicCatP2P && len(subs) == 2 {
					uname1 := asUid.UserId()
					uid2 := types.ParseUserId(msg.topic)
					// Tell user1 to stop sending updates to user2 without changing user2 online status.
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
			t.suspend()
			h.topicDel(topic)
			t.exit <- &shutDown{reason: reason}

			statsInc("LiveTopics", -1)
		}

		// sess && msg could be nil if the topic is being killed by timer
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

			topic.suspend()

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

// replyTopicDescBasic loads minimal topic Desc when the requester is not subscribed to the topic
func replyTopicDescBasic(sess *Session, topic string, msg *ClientComMessage) {
	log.Printf("hub.replyTopicDescBasic: topic %s", topic)
	now := types.TimeNow()
	desc := &MsgTopicDesc{}

	if strings.HasPrefix(topic, "grp") {
		stopic, err := store.Topics.Get(topic)
		if err != nil {
			sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
			return
		}
		if stopic == nil {
			sess.queueOut(ErrTopicNotFound(msg.id, msg.topic, now))
			return
		}
		desc.CreatedAt = &stopic.CreatedAt
		desc.UpdatedAt = &stopic.UpdatedAt
		desc.Public = stopic.Public

	} else {
		// 'me' and p2p topics
		uid := types.ZeroUid
		asUid := types.ParseUserId(msg.from)
		if strings.HasPrefix(topic, "usr") {
			// User specified as usrXXX
			uid = types.ParseUserId(topic)
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
			sess.queueOut(ErrMalformed(msg.id, msg.topic, now))
			return
		}

		suser, err := store.Users.Get(uid)
		if err != nil {
			sess.queueOut(ErrUnknown(msg.id, msg.topic, now))
			return
		}
		if suser == nil {
			sess.queueOut(ErrUserNotFound(msg.id, msg.topic, now))
			return
		}
		desc.CreatedAt = &suser.CreatedAt
		desc.UpdatedAt = &suser.UpdatedAt
		desc.Public = suser.Public
	}

	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{Id: msg.id, Topic: msg.topic, Timestamp: &now, Desc: desc}})
}
