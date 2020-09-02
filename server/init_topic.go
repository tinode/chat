/******************************************************************************
 *
 *  Description :
 *
 *    Topic initilization routines.
 *
 *****************************************************************************/

package main

import (
	"log"
	"strings"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// topicInit reads an existing topic from database or creates a new topic
func topicInit(t *Topic, join *sessionJoin, h *Hub) {
	var subscribeReqIssued bool
	defer func() {
		if !subscribeReqIssued && join.pkt.Sub != nil && join.sess.inflightReqs != nil {
			// If it was a client initiated subscribe request and we failed it.
			join.sess.inflightReqs.Done()
		}
	}()

	timestamp := types.TimeNow()

	var err error
	switch {
	case t.xoriginal == "me":
		// Request to load a 'me' topic. The topic always exists, the subscription is never new.
		err = initTopicMe(t, join)
	case t.xoriginal == "fnd":
		// Request to load a 'find' topic. The topic always exists, the subscription is never new.
		err = initTopicFnd(t, join)
	case strings.HasPrefix(t.xoriginal, "usr") || strings.HasPrefix(t.xoriginal, "p2p"):
		// Request to load an existing or create a new p2p topic, then attach to it.
		err = initTopicP2P(t, join)
	case strings.HasPrefix(t.xoriginal, "new"):
		// Processing request to create a new group topic.
		err = initTopicNewGrp(t, join, false)
	case strings.HasPrefix(t.xoriginal, "nch"):
		// Processing request to create a new channel.
		err = initTopicNewGrp(t, join, true)
	case strings.HasPrefix(t.xoriginal, "grp") || strings.HasPrefix(t.xoriginal, "chn"):
		// Load existing group topic (or channel).
		err = initTopicGrp(t, join)
	case t.xoriginal == "sys":
		// Initialize system topic.
		err = initTopicSys(t, join)
	default:
		// Unrecognized topic name
		err = types.ErrTopicNotFound
	}

	// Failed to create or load the topic.
	if err != nil {
		// Remove topic from cache to prevent hub from forwarding more messages to it.
		h.topicDel(join.pkt.RcptTo)

		log.Println("init_topic: failed to load or create topic:", join.pkt.RcptTo, err)
		join.sess.queueOut(decodeStoreErrorExplicitTs(err, join.pkt.Id, t.xoriginal, timestamp, join.pkt.Timestamp, nil))

		// Re-queue pending requests to join the topic.
		for len(t.reg) > 0 {
			reg := <-t.reg
			h.join <- reg
		}

		// Reject all other pending requests
		for len(t.broadcast) > 0 {
			msg := <-t.broadcast
			if msg.Id != "" {
				msg.sess.queueOut(ErrLockedExplicitTs(msg.Id, t.xoriginal, timestamp, join.pkt.Timestamp))
			}
		}
		for len(t.unreg) > 0 {
			msg := <-t.unreg
			if msg.pkt != nil {
				msg.sess.queueOut(ErrLockedReply(msg.pkt, timestamp))
			}
		}
		for len(t.meta) > 0 {
			msg := <-t.meta
			if msg.pkt.Id != "" {
				msg.sess.queueOut(ErrLockedReply(msg.pkt, timestamp))
			}
		}
		if len(t.exit) > 0 {
			msg := <-t.exit
			msg.done <- true
		}

		return
	}

	t.computePerUserAcsUnion()

	// prevent newly initialized topics to go live while shutdown in progress
	if globals.shuttingDown {
		h.topicDel(join.pkt.RcptTo)
		return
	}

	if t.isDeleted() {
		// Someone deleted the topic while we were trying to create it.
		return
	}

	statsInc("LiveTopics", 1)
	statsInc("TotalTopics", 1)
	usersRegisterTopic(t, true)

	// Topic will check access rights, send invite to p2p user, send {ctrl} message to the initiator session
	if join.pkt.Sub != nil {
		subscribeReqIssued = true
		t.reg <- join
	}

	t.markPaused(false)
	if t.cat == types.TopicCatFnd || t.cat == types.TopicCatSys {
		t.markLoaded()
	}

	go t.run(h)
}

// Initialize 'me' topic.
func initTopicMe(t *Topic, sreg *sessionJoin) error {
	t.cat = types.TopicCatMe

	user, err := store.Users.Get(types.ParseUserId(t.name))
	if err != nil {
		// Log out the session
		sreg.sess.uid = types.ZeroUid
		return err
	} else if user == nil {
		// Log out the session
		sreg.sess.uid = types.ZeroUid
		return types.ErrUserNotFound
	}

	// User's default access for p2p topics
	t.accessAuth = user.Access.Auth
	t.accessAnon = user.Access.Anon

	// Assign tags
	t.tags = user.Tags

	if err = t.loadSubscribers(); err != nil {
		return err
	}

	t.public = user.Public

	t.created = user.CreatedAt
	t.updated = user.UpdatedAt

	// The following values are exlicitly not set for 'me'.
	// t.touched, t.lastId, t.delId

	// 'me' has no owner, t.owner = nil

	// Initiate User Agent with the UA of the creating session to report it later
	t.userAgent = sreg.sess.userAgent
	// Initialize channel for receiving user agent and session online updates.
	t.supd = make(chan *sessionUpdate, 32)

	if !t.isProxy {
		// Allocate storage for contacts.
		t.perSubs = make(map[string]perSubsData)
	}

	return nil
}

// Initialize 'fnd' topic
func initTopicFnd(t *Topic, sreg *sessionJoin) error {
	t.cat = types.TopicCatFnd

	uid := types.ParseUserId(sreg.pkt.AsUser)
	if uid.IsZero() {
		return types.ErrNotFound
	}

	user, err := store.Users.Get(uid)
	if err != nil {
		return err
	} else if user == nil {
		if !sreg.sess.isMultiplex() {
			sreg.sess.uid = types.ZeroUid
		}
		return types.ErrNotFound
	}

	// Make sure no one can join the topic.
	t.accessAuth = getDefaultAccess(t.cat, true, false)
	t.accessAnon = getDefaultAccess(t.cat, false, false)

	if err = t.loadSubscribers(); err != nil {
		return err
	}

	t.created = user.CreatedAt
	t.updated = user.UpdatedAt

	// 'fnd' has no owner, t.owner = nil

	// Publishing to fnd is not supported
	// t.lastId = 0, t.delId = 0, t.touched = nil

	return nil
}

// Load or create a P2P topic.
// There is a reace condition when two users try to create a p2p topic at the same time.
func initTopicP2P(t *Topic, sreg *sessionJoin) error {
	pktsub := sreg.pkt.Sub

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
		return err
	}

	// If topic exists, load subscriptions
	var subs []types.Subscription
	if stopic != nil {
		// Subs already have Public swapped
		if subs, err = store.Topics.GetUsers(t.name, nil); err != nil {
			return err
		}

		// Case 3, fail
		if len(subs) == 0 {
			log.Println("hub: missing both subscriptions for '" + t.name + "' (SHOULD NEVER HAPPEN!)")
			return types.ErrInternal
		}

		t.created = stopic.CreatedAt
		t.updated = stopic.UpdatedAt
		if !stopic.TouchedAt.IsZero() {
			t.touched = stopic.TouchedAt
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
		userID1 := types.ParseUserId(sreg.pkt.AsUser)
		// The other user.
		userID2 := types.ParseUserId(t.xoriginal)
		// User index: u1 - requester, u2 - responder, the other user

		var u1, u2 int
		users, err := store.Users.GetAll(userID1, userID2)
		if err != nil {
			return err
		}
		if len(users) != 2 {
			// Invited user does not exist
			return types.ErrUserNotFound
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
				sub2.ModeGiven = users[u1].Access.Auth
				if err := sub2.ModeGiven.UnmarshalText([]byte(pktsub.Set.Desc.DefaultAcs.Auth)); err != nil {
					log.Println("hub: invalid access mode", t.xoriginal, pktsub.Set.Desc.DefaultAcs.Auth)
				}
			} else {
				// Use user1.Auth as modeGiven for the other user
				sub2.ModeGiven = users[u1].Access.Auth
			}
			// Sanity check
			sub2.ModeGiven = sub2.ModeGiven&types.ModeCP2P | types.ModeApprove

			// Swap Public to match swapped Public in subs returned from store.Topics.GetSubs
			sub2.SetPublic(users[u1].Public)

			// Mark the entire topic as new.
			pktsub.Created = true
		}

		// Requester's subscription is missing:
		// a. requester is starting a new topic
		// b. requester's subscription is missing: deleted or creation failed
		if sub1 == nil {

			// Set user1's ModeGiven from user2's default values
			userData.modeGiven = selectAccessMode(auth.Level(sreg.pkt.AuthLvl),
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
						if err := userData.modeWant.UnmarshalText([]byte(pktsub.Set.Sub.Mode)); err != nil {
							log.Println("hub: invalid access mode", t.xoriginal, pktsub.Set.Sub.Mode)
						}
						// Ensure sanity
						userData.modeWant = userData.modeWant&types.ModeCP2P | types.ModeApprove
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
			pktsub.Newsub = true
		}

		if !user1only {
			// sub2 is being created, assign sub2.modeWant to what user2 gave to user1 (sub1.modeGiven)
			sub2.ModeWant = selectAccessMode(auth.Level(sreg.pkt.AuthLvl),
				users[u2].Access.Anon,
				users[u2].Access.Auth,
				types.ModeCP2P)
			// Ensure sanity
			sub2.ModeWant = sub2.ModeWant&types.ModeCP2P | types.ModeApprove
		}

		// Create everything
		if stopic == nil {
			if err = store.Topics.CreateP2P(sub1, sub2); err != nil {
				return err
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
				return err
			}
		}

		// Publics are already swapped.
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

	return nil
}

// Create a new group topic
func initTopicNewGrp(t *Topic, sreg *sessionJoin, isChan bool) error {
	timestamp := types.TimeNow()
	pktsub := sreg.pkt.Sub

	t.cat = types.TopicCatGrp
	t.isChan = isChan

	// Generic topics have parameters stored in the topic object
	t.owner = types.ParseUserId(sreg.pkt.AsUser)

	t.accessAuth = getDefaultAccess(t.cat, true, isChan)
	t.accessAnon = getDefaultAccess(t.cat, false, isChan)

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
			userData.modeWant = types.ModeCFull
			if err := userData.modeWant.UnmarshalText([]byte(pktsub.Set.Sub.Mode)); err != nil {
				log.Println("hub: invalid access mode", t.xoriginal, pktsub.Set.Sub.Mode)
			}
			// User must not unset ModeJoin or the owner flags
			userData.modeWant |= types.ModeJoin | types.ModeOwner
		}

		tags = normalizeTags(pktsub.Set.Tags)
		if !restrictedTagsEqual(tags, nil, globals.immutableTagNS) {
			return types.ErrPermissionDenied
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
		ObjHeader: types.ObjHeader{Id: sreg.pkt.RcptTo, CreatedAt: timestamp},
		Access:    types.DefaultAccess{Auth: t.accessAuth, Anon: t.accessAnon},
		Tags:      tags,
		UseBt:     isChan,
		Public:    t.public}

	// store.Topics.Create will add a subscription record for the topic creator
	stopic.GiveAccess(t.owner, userData.modeWant, userData.modeGiven)
	err := store.Topics.Create(stopic, t.owner, t.perUser[t.owner].private)
	if err != nil {
		return err
	}

	t.xoriginal = t.name // keeping 'new' or 'nch' as original has no value to the client
	pktsub.Created = true
	pktsub.Newsub = true

	return nil
}

// Initialize existing group topic. There is a race condition when two users attempt to load
// the same topic at the same time. It's prevented at hub level.
func initTopicGrp(t *Topic, sreg *sessionJoin) error {
	t.cat = types.TopicCatGrp

	// TODO(gene): check and validate topic name
	stopic, err := store.Topics.Get(t.name)
	if err != nil {
		return err
	} else if stopic == nil {
		return types.ErrTopicNotFound
	}

	if err = t.loadSubscribers(); err != nil {
		return err
	}

	t.isChan = stopic.UseBt

	// t.owner is set by loadSubscriptions

	t.accessAuth = stopic.Access.Auth
	t.accessAnon = stopic.Access.Anon

	// Assign tags
	t.tags = stopic.Tags

	t.public = stopic.Public

	t.created = stopic.CreatedAt
	t.updated = stopic.UpdatedAt
	if !stopic.TouchedAt.IsZero() {
		t.touched = stopic.TouchedAt
	}
	t.lastID = stopic.SeqId
	t.delID = stopic.DelId

	// Initialize channel for receiving session online updates.
	t.supd = make(chan *sessionUpdate, 32)

	t.xoriginal = t.name // topic may have been loaded by a channel reader; make sure it's grpXXX, not chnXXX.

	return nil
}

// Initialize system topic. System topic is a singleton, always in memory.
func initTopicSys(t *Topic, sreg *sessionJoin) error {
	t.cat = types.TopicCatSys

	stopic, err := store.Topics.Get(t.name)
	if err != nil {
		return err
	} else if stopic == nil {
		return types.ErrTopicNotFound
	}

	if err = t.loadSubscribers(); err != nil {
		return err
	}

	// There is no t.owner

	// Default permissions are 'W'
	t.accessAuth = types.ModeWrite
	t.accessAnon = types.ModeWrite

	t.public = stopic.Public

	t.created = stopic.CreatedAt
	t.updated = stopic.UpdatedAt
	if !stopic.TouchedAt.IsZero() {
		t.touched = stopic.TouchedAt
	}
	t.lastID = stopic.SeqId

	return nil
}

// loadSubscribers loads topic subscribers, sets topic owner.
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
