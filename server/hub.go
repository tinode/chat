/******************************************************************************
 *
 *  Description :
 *
 *    Create/tear down conversation topics, route messages between topics.
 *
 *****************************************************************************/

package main

import (
	"expvar"
	"log"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Request to hub to subscribe session to topic
type sessionJoin struct {
	// Routable (expanded) name of the topic to subscribe to
	topic string
	// Packet, containing request details
	pkt *MsgClientSub
	// Session to subscribe
	sess *Session
	// If this topic was just created
	created bool
	// If the topic was just loaded
	loaded bool
}

// Request to hub to remove the topic
type topicUnreg struct {
	// Name of the topic to drop
	topic string
	// Session making the request, could be nil
	sess *Session
	// Original request, could be nil
	msg *MsgClientDel
	// Unregister then delete the topic
	del bool
}

type metaReq struct {
	// Routable name of the topic to get info for
	topic string
	// packet containing details of the Get/Set request
	pkt *ClientComMessage
	// Session which originated the request
	sess *Session
	// what is being requested, constMsgGetInfo, constMsgGetSub, constMsgGetData
	what int
}

type Hub struct {

	// Topics must be indexed by appid!name
	topics map[string]*Topic

	// Channel for routing messages between topics, buffered at 2048
	route chan *ServerComMessage

	// subscribe session to topic, possibly creating a new topic
	join chan *sessionJoin

	// Remove topic from hub, possibly deleting it afterwards
	unreg chan *topicUnreg

	// process get.info requests for topic not subscribed to
	meta chan *metaReq

	// Request to shutdown
	shutdown chan chan<- bool

	// Exported counter of live topics
	topicsLive *expvar.Int
}

func (h *Hub) topicGet(name string) *Topic {
	return h.topics[name]
}

func (h *Hub) topicPut(name string, t *Topic) {
	h.topics[name] = t
}

func (h *Hub) topicDel(name string) {
	delete(h.topics, name)
}

func newHub() *Hub {
	var h = &Hub{
		topics: make(map[string]*Topic),
		// this needs to be buffered - hub generates invites and adds them to this queue
		route:      make(chan *ServerComMessage, 2048),
		join:       make(chan *sessionJoin),
		unreg:      make(chan *topicUnreg),
		meta:       make(chan *metaReq, 32),
		shutdown:   make(chan chan<- bool),
		topicsLive: new(expvar.Int)}

	expvar.Publish("LiveTopics", h.topicsLive)

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

			timestamp := time.Now().UTC().Round(time.Millisecond)
			if dst := h.topicGet(msg.rcptto); dst != nil {
				// Everything is OK, sending packet to known topic
				//log.Printf("Hub. Sending message to '%s'", dst.name)

				if dst.broadcast != nil {
					select {
					case dst.broadcast <- msg:
					default:
						log.Printf("hub: topic's broadcast queue is full '%s'", dst.name)
					}
				}
			} else {
				if msg.Data != nil {
					// Normally the message is persisted at the topic. If the topic is offline,
					// persist message here. The only case of sending to offline topics is invites/info to 'me'
					// The 'me' must receive them, so ignore access settings

					if err := store.Messages.Save(&types.Message{
						ObjHeader: types.ObjHeader{CreatedAt: msg.Data.Timestamp},
						Topic:     msg.rcptto,
						// SeqId is assigned by the store.Mesages.Save
						From:    types.ParseUserId(msg.Data.From).String(),
						Content: msg.Data.Content}); err != nil {

						msg.sessFrom.queueOut(ErrUnknown(msg.id, msg.Data.Topic, timestamp))
						return
					}

					// TODO(gene): validate topic name, discarding invalid topics
					log.Printf("Hub. Topic[%s] is unknown or offline", msg.rcptto)

					msg.sessFrom.queueOut(NoErrAccepted(msg.id, msg.rcptto, timestamp))
				}
			}

		case meta := <-h.meta:
			log.Println("hub.meta: got message")
			// Request for topic info from a user who is not subscribed to the topic
			if dst := h.topicGet(meta.topic); dst != nil {
				// If topic is already in memory, pass request to topic
				dst.meta <- meta
			} else if meta.pkt.Get != nil {
				// If topic is not in memory, fetch requested description from DB and reply here
				go replyTopicDescBasic(meta.sess, meta.topic, meta.pkt.Get)
			}

		case unreg := <-h.unreg:
			// The topic is being garbage collected or deleted.
			h.topicUnreg(unreg.sess, unreg.topic, unreg.msg, unreg.del)

		case hubdone := <-h.shutdown:
			topicsdone := make(chan bool)
			for _, topic := range h.topics {
				topic.exit <- &shutDown{done: topicsdone}
			}

			for i := 0; i < len(h.topics); i++ {
				<-topicsdone
			}

			log.Printf("Hub shutdown: terminated %d topics", len(h.topics))

			// let the main goroutine know we are done with the cleanup
			hubdone <- true

			return

		case <-time.After(IDLETIMEOUT):
		}
	}
}

// topicInit reads an existing topic from database or creates a new topic
func topicInit(sreg *sessionJoin, h *Hub) {
	var t *Topic

	timestamp := time.Now().UTC().Round(time.Millisecond)

	t = &Topic{name: sreg.topic,
		x_original: sreg.pkt.Topic,
		sessions:   make(map[*Session]bool),
		broadcast:  make(chan *ServerComMessage, 256),
		reg:        make(chan *sessionJoin, 32),
		unreg:      make(chan *sessionLeave, 32),
		meta:       make(chan *metaReq, 32),
		perUser:    make(map[types.Uid]perUserData),
		exit:       make(chan *shutDown, 1),
	}

	// Helper function to parse access mode from string, handling errors and setting default value
	parseMode := func(modeString string, defaultMode types.AccessMode) types.AccessMode {
		mode := defaultMode
		if err := mode.UnmarshalText([]byte(modeString)); err != nil {
			log.Println("hub: invalid access mode for topic[" + t.x_original + "]: '" + modeString + "'")
		}

		return mode
	}

	// Request to load a 'me' topic. The topic always exists.
	if t.x_original == "me" {
		log.Println("hub: loading 'me' topic")

		t.cat = types.TopicCat_Me

		// 'me' has no owner, t.owner = nil

		user, err := store.Users.Get(sreg.sess.uid)
		if err != nil {
			log.Println("hub: cannot load user object for 'me'='" + t.name + "' (" + err.Error() + ")")
			// Log out the session
			sreg.sess.uid = types.ZeroUid
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		} else if user == nil {
			log.Println("hub: user's account unexpectedly not found (deleted?)")
			// Log out the session
			sreg.sess.uid = types.ZeroUid
			sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		// User's default access for p2p topics
		t.accessAuth = user.Access.Auth
		t.accessAnon = user.Access.Anon

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		t.public = user.Public

		t.created = user.CreatedAt
		t.updated = user.UpdatedAt

		t.lastId = user.SeqId
		t.clearId = user.ClearId

		// Initiate User Agent with the UA of the creating session to report it later
		t.userAgent = sreg.sess.userAgent
		// Initialize channel for receiving user agent updates
		t.uaChange = make(chan string, 32)

		// Request to load a 'find' topic. The topic always exists.
	} else if t.x_original == "fnd" {
		log.Println("hub: loading 'fnd' topic")

		t.cat = types.TopicCat_Fnd

		// 'fnd' has no owner, t.owner = nil

		// Make sure no one can join the topic.
		t.accessAuth = getDefaultAccess(t.cat, true)
		t.accessAnon = getDefaultAccess(t.cat, false)

		user, err := store.Users.Get(sreg.sess.uid)
		if err != nil {
			log.Println("hub: cannot load user object for 'fnd'='" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		} else if user == nil {
			log.Println("hub: user's account unexpectedly not found (deleted?)")
			sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		t.public = user.Tags

		t.created = user.CreatedAt
		t.updated = user.UpdatedAt

		// Publishing to fnd is not supported
		// t.lastId = 0

		// Request to load an existing or create a new p2p topic, then attach to it.
	} else if strings.HasPrefix(t.x_original, "usr") || strings.HasPrefix(t.x_original, "p2p") {
		log.Println("hub: loading or creating p2p topic")

		// Handle the following cases:
		// 1. Neither topic nor subscriptions exist: create a new p2p topic & subscriptions.
		// 2. Topic exists, one of the subscriptions is missing:
		// 2.1 Requester's subscription is missing, recreate it.
		// 2.2 Other user's subscription is missing, treat like a new request for user 2.
		// 3. Topic exists, both subscriptions are missing: should not happen, fail.
		// 4. Topic and both subscriptions exist: attach to topic

		t.cat = types.TopicCat_P2P

		// Check if the topic already exists
		stopic, err := store.Topics.Get(t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		// If topic exists, load subscriptions
		var subs []types.Subscription
		if stopic != nil {
			// Subs already have Public swapped
			if subs, err = store.Topics.GetSubs(t.name); err != nil {
				log.Println("hub: cannot load subscritions for '" + t.name + "' (" + err.Error() + ")")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
				return
			}

			// Case 3, fail
			if subs == nil || len(subs) == 0 {
				log.Println("hub: missing both subscriptions for '" + t.name + "' (SHOULD NEVER HAPPEN!)")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
				return
			}

			t.created = stopic.CreatedAt
			t.updated = stopic.UpdatedAt

			t.lastId = stopic.SeqId
			t.clearId = stopic.ClearId
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

			log.Println("hub: existing p2p topic")

			for i := 0; i < 2; i++ {
				uid := types.ParseUid(subs[i].User)
				t.perUser[uid] = perUserData{
					// Adapter already swapped the public values
					public:  subs[i].GetPublic(),
					private: subs[i].Private,
					// lastSeenTag: subs[i].LastSeen,
					modeWant:  subs[i].ModeWant,
					modeGiven: subs[i].ModeGiven,
					clearId:   subs[i].ClearId}
			}

		} else {
			// Cases 1 (new topic), 2 (one of the two subscriptions is missing: either it's a new request
			// or the subscription was deleted)

			log.Println("hub: p2p new topic or one of the subs is missing")

			var userData perUserData

			// Fetching records for both users.
			// Requester.
			userId1 := sreg.sess.uid
			// The other user.
			userId2 := types.ParseUserId(t.x_original)
			// User index: u1 - requester, u2 - the other user
			var u1, u2 int
			users, err := store.Users.GetAll(userId1, userId2)
			if err != nil {
				log.Println("hub: failed to load users for '" + t.name + "' (" + err.Error() + ")")
				sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
				return
			} else if users == nil || len(users) != 2 {
				// Invited user does not exist
				log.Println("hub: missing user for '" + t.name + "'")
				sreg.sess.queueOut(ErrUserNotFound(sreg.pkt.Id, t.x_original, timestamp))
				return
			} else {
				// User records are unsorted, make sure we know who is who.
				if users[0].Uid() == userId1 {
					u1, u2 = 0, 1
				} else {
					u1, u2 = 1, 0
				}
			}

			// Figure out which subscriptions are missing: User1's, User2's or both.
			var sub1, sub2 *types.Subscription
			// Set to true if only requester's subscription has to be created.
			var user1only bool
			if len(subs) == 1 {
				if subs[0].Uid() == userId1 {
					// User2's subscription is missing, user1's exists
					sub1 = &subs[0]
				} else {
					// User1's is missing, user2's exists
					sub2 = &subs[0]
					user1only = true
				}
			}

			// Other user's subscription is missing
			if sub2 == nil {
				sub2 = &types.Subscription{
					User:    userId2.String(),
					Topic:   t.name,
					Private: nil}

				// Assign user2's ModeGiven based on what user1 has provided
				if sreg.pkt.Set != nil && sreg.pkt.Set.Desc != nil && sreg.pkt.Set.Desc.DefaultAcs != nil {
					// Use provided DefaultAcs as non-default modeGiven for the other user.
					// The other user is assumed to have auth level "Auth".
					sub2.ModeGiven = parseMode(sreg.pkt.Set.Desc.DefaultAcs.Auth, users[u1].Access.Auth) &
						types.ModeCP2P
				} else {
					// Use user1.Auth as modeGiven for the other user
					sub2.ModeGiven = users[u1].Access.Auth
				}

				// Swap Public to match swapped Public in subs returned from store.Topics.GetSubs
				sub2.SetPublic(users[u1].Public)
			}

			// Requester's subscription is missing:
			// a. requester is starting a new topic
			// b. requester's subscription is missing: deleted or creation failed
			if sub1 == nil {
				// Set user1's ModeGiven from user2's default values
				userData.modeGiven = selectAccessMode(sreg.sess.authLvl,
					users[u2].Access.Anon,
					users[u2].Access.Auth,
					types.ModeCP2P)

				// By default assign the same mode that user1 gave to user2 (could be changed below)
				userData.modeWant = sub2.ModeGiven

				if sreg.pkt.Set != nil {
					if sreg.pkt.Set.Sub != nil {
						uid := sreg.sess.uid
						if sreg.pkt.Set.Sub.User != "" {
							uid = types.ParseUserId(sreg.pkt.Set.Sub.User)
						}

						if uid != sreg.sess.uid {
							// Report the error and ignore the value
							log.Println("hub: setting mode for another user is not supported '" + t.name + "'")
						} else {
							// user1 is setting non-default modeWant
							userData.modeWant = parseMode(sreg.pkt.Set.Sub.Mode, userData.modeWant) &
								types.ModeCP2P
						}

						// Since user1 issued a {sub} request, make sure the user can join
						userData.modeWant |= types.ModeJoin
					}

					// user1 sets non-default Private
					if sreg.pkt.Set.Desc != nil {
						if !isNullValue(sreg.pkt.Set.Desc.Private) {
							userData.private = sreg.pkt.Set.Desc.Private
						}
						// Public, if present, is ignored
					}
				}

				sub1 = &types.Subscription{
					User:      userId1.String(),
					Topic:     t.name,
					ModeWant:  userData.modeWant,
					ModeGiven: userData.modeGiven,
					Private:   userData.private}
				// Swap Public to match swapped Public in subs returned from store.Topics.GetSubs
				sub1.SetPublic(users[u2].Public)
			}

			if !user1only {
				// sub2 is being created, assign sub2.modeWant to what user2 gave to user1 (sub1.modeGiven)
				sub2.ModeWant = selectAccessMode(sreg.sess.authLvl,
					users[u2].Access.Anon,
					users[u2].Access.Auth,
					types.ModeCP2P)
			}

			// Create everything
			if stopic == nil {
				if err = store.Topics.CreateP2P(sub1, sub2); err != nil {
					log.Println("hub: databse error in creating subscriptions '" + t.name + "' (" + err.Error() + ")")
					sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
					return
				}

				t.created = sub1.CreatedAt
				t.updated = sub1.UpdatedAt

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
					sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
					return
				}
			}

			// t.clearId is not currently used for p2p topics

			// Publics is already swapped
			userData.public = sub1.GetPublic()
			userData.modeWant = sub1.ModeWant
			userData.modeGiven = sub1.ModeGiven
			userData.clearId = sub2.ClearId
			t.perUser[userId1] = userData

			t.perUser[userId2] = perUserData{
				public:    sub2.GetPublic(),
				modeWant:  sub2.ModeWant,
				modeGiven: sub2.ModeGiven,
				clearId:   sub2.ClearId}

			sreg.created = !user1only
		}
        
        // make sure modeWant & modeGiven always have 'A' permission for both subscribers whether in existing or new P2P topic
        for uid, userData := range t.perUser {
            shouldUpdateSubscription := false
            modes := []*types.AccessMode{&userData.modeWant, &userData.modeGiven}
            for _, mode := range modes {
                (*mode) &= types.ModeCP2P
                
                // check if resulted permission not contain 'A' permission unless it's 'N'
                if *mode != types.ModeNone && (*mode & types.ModeApprove == types.ModeNone) {
                    shouldUpdateSubscription = true
                    *mode |= types.ModeApprove
                }
            }
            
            if shouldUpdateSubscription {
                // save update to db
                update := map[string]interface{}{
                    "ModeGiven": int(userData.modeGiven),
                    "ModeWant": int(userData.modeWant),
                }
                if err = store.Subs.Update(t.name, uid, update); err != nil {
                    log.Println("hub: databse error in updating user subscription '" + t.name + "' (" + err.Error() + ")")
                    sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
                    return
                }
                
                // modify current mode
                t.perUser[uid] = userData
            }
        }

		// Clear original topic name.
		t.x_original = ""

		// Processing request to create a new generic (group) topic:
	} else if strings.HasPrefix(t.x_original, "new") {
		log.Println("hub: new group topic")

		t.cat = types.TopicCat_Grp

		// Generic topics have parameters stored in the topic object
		t.owner = sreg.sess.uid

		t.accessAuth = getDefaultAccess(t.cat, true)
		t.accessAnon = getDefaultAccess(t.cat, false)

		// Owner/creator gets full access to the topic. Owner may change the default modeWant through 'set'.
		userData := perUserData{
			modeGiven: types.ModeCFull,
			modeWant:  types.ModeCFull}

		if sreg.pkt.Set != nil {
			// User sent initialization parameters
			if sreg.pkt.Set.Desc != nil {
				if !isNullValue(sreg.pkt.Set.Desc.Public) {
					t.public = sreg.pkt.Set.Desc.Public
				}
				if !isNullValue(sreg.pkt.Set.Desc.Private) {
					userData.private = sreg.pkt.Set.Desc.Private
				}

				// set default access
				if sreg.pkt.Set.Desc.DefaultAcs != nil {
					if auth, anon, err := parseTopicAccess(sreg.pkt.Set.Desc.DefaultAcs, t.accessAuth, t.accessAnon); err != nil {
						// Invalid access for one or both. Make it explicitly None
						if auth.IsInvalid() {
							t.accessAuth = types.ModeNone
						} else {
							t.accessAuth = auth
						}
						if anon.IsInvalid() {
							t.accessAnon = types.ModeNone
						} else {
							t.accessAnon = anon
						}
						log.Println("hub: invalid access mode for topic '" + t.name + "': '" + err.Error() + "'")
					} else if auth.IsOwner() || anon.IsOwner() {
						log.Println("hub: OWNER default access in topic '" + t.name)
						t.accessAuth, t.accessAnon = auth & ^types.ModeOwner, anon & ^types.ModeOwner
					} else {
						t.accessAuth, t.accessAnon = auth, anon
					}
				}
			}

			// Owner/creator may restrict own access to topic
			if sreg.pkt.Set.Sub != nil && sreg.pkt.Set.Sub.Mode != "" {
				userData.modeWant = parseMode(sreg.pkt.Set.Sub.Mode, types.ModeCFull)
				// User must not unset ModeJoin or the owner flags
				userData.modeWant |= types.ModeJoin | types.ModeOwner
			}
		}

		t.perUser[t.owner] = userData

		t.created = timestamp
		t.updated = timestamp

		// t.lastId & t.clearId are not set for new topics

		stopic := &types.Topic{
			ObjHeader: types.ObjHeader{Id: sreg.topic, CreatedAt: timestamp},
			Access:    types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon},
			Public:    t.public}
		// store.Topics.Create will add a subscription record for the topic creator
		stopic.GiveAccess(t.owner, userData.modeWant, userData.modeGiven)
		err := store.Topics.Create(stopic, t.owner, t.perUser[t.owner].private)
		if err != nil {
			log.Println("hub: cannot save new topic '" + t.name + "' (" + err.Error() + ")")
			// Error sent on "newWHATEVER" topic
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		t.x_original = t.name // keeping 'new' as original has no value to the client
		sreg.created = true

	} else if strings.HasPrefix(t.x_original, "grp") {
		log.Println("hub: existing group topic")

		t.cat = types.TopicCat_Grp

		// TODO(gene): check and validate topic name
		stopic, err := store.Topics.Get(t.name)
		if err != nil {
			log.Println("hub: error while loading topic '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		} else if stopic == nil {
			log.Println("hub: topic '" + t.name + "' does not exist")
			sreg.sess.queueOut(ErrTopicNotFound(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		if err = t.loadSubscribers(); err != nil {
			log.Println("hub: cannot load subscribers for '" + t.name + "' (" + err.Error() + ")")
			sreg.sess.queueOut(ErrUnknown(sreg.pkt.Id, t.x_original, timestamp))
			return
		}

		// t.owner is set by loadSubscriptions

		t.accessAuth = stopic.Access.Auth
		t.accessAnon = stopic.Access.Anon

		t.public = stopic.Public

		t.created = stopic.CreatedAt
		t.updated = stopic.UpdatedAt

		t.lastId = stopic.SeqId
		t.clearId = stopic.ClearId

	} else {
		// Unrecognized topic name
		sreg.sess.queueOut(ErrTopicNotFound(sreg.pkt.Id, t.x_original, timestamp))
		return
	}

	log.Println("hub: topic created or loaded: " + t.name)

	h.topicPut(t.name, t)
	h.topicsLive.Add(1)
	go t.run(h)

	sreg.loaded = true
	// Topic will check access rights, send invite to p2p user, send {ctrl} message to the initiator session
	t.reg <- sreg
}

// loadSubscribers loads topic subscribers, sets topic owner
func (t *Topic) loadSubscribers() error {
	subs, err := store.Topics.GetSubs(t.name)
	if err != nil {
		return err
	}

	if subs == nil {
		return nil
	}

	for _, sub := range subs {
		uid := types.ParseUid(sub.User)
		t.perUser[uid] = perUserData{
			created:   sub.CreatedAt,
			updated:   sub.UpdatedAt,
			clearId:   sub.ClearId,
			readId:    sub.ReadSeqId,
			recvId:    sub.RecvSeqId,
			private:   sub.Private,
			modeWant:  sub.ModeWant,
			modeGiven: sub.ModeGiven}

		if (sub.ModeGiven & sub.ModeWant).IsOwner() {
			log.Printf("hub.loadSubscriptions: %s set owner to %s", t.name, uid.String())
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

// 2. Topic is just being unregistered (topic is going offline)
// 2.1 Unregister it with no further action
//
func (h *Hub) topicUnreg(sess *Session, topic string, msg *MsgClientDel, del bool) {
	now := time.Now().UTC().Round(time.Millisecond)

	if del {
		// Case 1 (unregister and delete)
		if t := h.topicGet(topic); t != nil {
			// Case 1.1: topic is online
			if t.owner == sess.uid || (t.cat == types.TopicCat_P2P && len(t.perUser) < 2) {
				// Case 1.1.1: requester is the owner or last sub in a p2p topic

				t.suspend()

				if err := store.Topics.Delete(topic); err != nil {
					t.resume()
					log.Println("topicUnreg delete failed (1):", err)
					sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
					return
				}

				t.meta <- &metaReq{
					topic: topic,
					pkt:   &ClientComMessage{Del: msg},
					sess:  sess,
					what:  constMsgDelTopic}

				if sess != nil && msg != nil {
					sess.queueOut(NoErr(msg.Id, msg.Topic, now))
				}

				h.topicDel(topic)
				t.exit <- &shutDown{del: true}
				h.topicsLive.Add(-1)
			} else {
				// Case 1.1.2: requester is NOT the owner
				t.meta <- &metaReq{
					topic: topic,
					pkt:   &ClientComMessage{Del: msg},
					sess:  sess,
					what:  constMsgDelTopic}
			}

		} else {
			// Case 1.2: topic is offline.
			if sub, err := store.Subs.Get(topic, sess.uid); err != nil {
				log.Println("topicUnreg failed to load offline topic:", err)
				sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
				return
			} else if sub == nil {
				// If user has no subscription, tell him all is fine
				sess.queueOut(InfoNoAction(msg.Id, msg.Topic, now))
				return
			} else if !(sub.ModeGiven & sub.ModeWant).IsOwner() {
				// Case 1.2.2.1 Not the owner, but possibly last subscription in a P2P topic:
				if topicCat(topic) == types.TopicCat_P2P {
					// If this is a P2P topic, check how many subscriptions are left
					if subs, err := store.Topics.GetSubs(topic); err != nil {
						log.Println("topicUnreg failed to load subscribers:", err)
						sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
						return
					} else if subs != nil && len(subs) < 2 {
						// Fewer than 2 subscriptions, delete the entire topic
						if err := store.Topics.Delete(topic); err != nil {
							log.Println("topicUnreg delete failed (2):", err)
							sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
							return
						}
						sess.queueOut(NoErr(msg.Id, msg.Topic, now))
						return
					}
				}

				// Not owner or more than one subscription left in a P2P topic
				if err := store.Subs.Delete(topic, sess.uid); err != nil {
					log.Println("topicUnreg failed (3):", err)
					sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
					return
				}

			} else {
				// Case 1.2.1.1: owner, delete the topic from db
				if err := store.Topics.Delete(topic); err != nil {
					log.Println("topicUnreg failed (4):", err)
					sess.queueOut(ErrUnknown(msg.Id, msg.Topic, now))
					return
				}
			}

			if sess != nil && msg != nil {
				sess.queueOut(NoErr(msg.Id, msg.Topic, now))
			}
		}

	} else {
		// Case 2: just unregister.
		// If t is nil, it's not registered, no action is needed
		if t := h.topicGet(topic); t != nil {
			t.suspend()
			h.topicDel(topic)
			t.exit <- &shutDown{del: false}
			h.topicsLive.Add(-1)
		}

		// sess && msg could be nil if the topic is being killed by timer
		if sess != nil && msg != nil {
			sess.queueOut(NoErr(msg.Id, msg.Topic, now))
		}
	}
}

// replyTopicDescBasic loads minimal topic Desc when the requester is not subscribed to the topic
func replyTopicDescBasic(sess *Session, topic string, get *MsgClientGet) {
	log.Printf("hub.replyTopicDescBasic: topic %s", topic)
	now := time.Now().UTC().Round(time.Millisecond)
	desc := &MsgTopicDesc{}

	if strings.HasPrefix(topic, "grp") {
		stopic, err := store.Topics.Get(topic)
		if err != nil {
			sess.queueOut(ErrUnknown(get.Id, get.Topic, now))
			return
		} else if stopic == nil {
			sess.queueOut(ErrTopicNotFound(get.Id, get.Topic, now))
			return
		} else {
			desc.CreatedAt = &stopic.CreatedAt
			desc.UpdatedAt = &stopic.UpdatedAt
			desc.Public = stopic.Public
		}
	} else {
		// 'me' and p2p topics
		var uid types.Uid
		if strings.HasPrefix(topic, "usr") {
			// User specified as usrXXX
			uid = types.ParseUserId(topic)
		} else if strings.HasPrefix(topic, "p2p") {
			// User specified as p2pXXXYYY
			uid1, uid2, _ := types.ParseP2P(topic)
			if uid1 == sess.uid {
				uid = uid2
			} else if uid2 == sess.uid {
				uid = uid1
			}
		}

		if uid.IsZero() {
			sess.queueOut(ErrMalformed(get.Id, get.Topic, now))
			return
		}

		suser, err := store.Users.Get(uid)
		if err != nil {
			log.Printf("hub.replyTopicInfoBasic: sending  error 3")
			sess.queueOut(ErrUnknown(get.Id, get.Topic, now))
			return
		} else if suser == nil {
			sess.queueOut(ErrUserNotFound(get.Id, get.Topic, now))
			return
		} else {
			desc.CreatedAt = &suser.CreatedAt
			desc.UpdatedAt = &suser.UpdatedAt
			desc.Public = suser.Public
		}
	}

	log.Printf("hub.replyTopicDescBasic: sending desc -- OK")
	sess.queueOut(&ServerComMessage{
		Meta: &MsgServerMeta{Id: get.Id, Topic: get.Topic, Timestamp: &now, Desc: desc}})
}

// Parse topic access parameters
func parseTopicAccess(acs *MsgDefaultAcsMode, defAuth, defAnon types.AccessMode) (auth, anon types.AccessMode,
	err error) {

	auth, anon = defAuth, defAnon

	if acs.Auth != "" {
		if err = auth.UnmarshalText([]byte(acs.Auth)); err != nil {
			log.Println("hub: invalid default auth access mode '" + acs.Auth + "'")
		}
	}

	if acs.Anon != "" {
		if err = anon.UnmarshalText([]byte(acs.Anon)); err != nil {
			log.Println("hub: invalid default anon access mode '" + acs.Anon + "'")
		}
	}

	return
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
