/*****************************************************************************
 * Storage schema
 *****************************************************************************
 * System-accessible tables:
 ***************************
 * 1. Customer (customer of the service)
 *****************************
 * Customer-accessible tables:
 *****************************
 * 2. Application (a customer may have multiple applications)
 * 3. Application keys (an application may have multiple API keys)
 ****************************************
 * Application/end-user-accessible tables
 ****************************************
 * 4. User (end-user)
 * 5. Session (data associated with logged-in user)
 * 6. Topics (aka Inbox; a list of user's threads/conversations, with access rights, indexed by user id and by
	topic name, neither userId nor topicName are unique)
 * 7. Messages (persistent store of messages)
 * 8. Contacts (a.k.a. ledger, address book)
 *****************************************************************************/
package store

import (
	"errors"
	"github.com/tinode/chat/server/store/adapter"
	"github.com/tinode/chat/server/store/types"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

const (
	MAX_USERS_FOR_TOPIC = 32
)

var adaptr adapter.Adapter

// Open initializes the persistence system. Adapter holds a connection pool for a single database.
func Open(dataSourceName string) error {
	if adaptr == nil {
		return errors.New("store: attept to Open an adapter before registering")
	}
	if adaptr.IsOpen() {
		return errors.New("store: connection already opened")
	}

	return adaptr.Open(dataSourceName)
}

func Close() error {
	if adaptr.IsOpen() {
		return adaptr.Close()
	} else {
		return errors.New("store: connection already closed")
	}
}

func IsOpen() bool {
	if adaptr != nil {
		return adaptr.IsOpen()
	} else {
		return false
	}
}

func InitDb(reset bool) error {
	return adaptr.CreateDb(reset)
}

// Register makes a persistence adapter available by the provided name.
// If Register is called twice with the same name or if the adapter is nil,
// it panics.
func Register(adapter adapter.Adapter) {
	if adapter == nil {
		panic("store: Register adapter is nil")
	}
	if adaptr != nil {
		panic("store: Adapter already registered")
	}
	adaptr = adapter
}

// Users struct to hold methods for persistence mapping for the User object.
type UsersObjMapper struct{}

// Users is the ancor for storing/retrieving User objects
var Users UsersObjMapper

// CreateUser inserts User object into a database, updates creation time and assigns UID
func (u UsersObjMapper) Create(appid uint32, user *types.User, scheme, secret string, private interface{}) (*types.User, error) {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			user.InitTimes()

			user.Username = secret[:splitAt]
			var err error
			user.Passhash, err = bcrypt.GenerateFromPassword([]byte(secret[splitAt+1:]), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}

			// TODO(gene): maybe have some additional handling of duplicate user name error
			err, _ = adaptr.UserCreate(appid, user)
			user.Passhash = nil
			if err != nil {
				return nil, err
			}

			// Create user's subscription to !me. The !me topic is ephemeral, the topic object need not to be inserted.
			err = Subs.Create(appid,
				&types.Subscription{
					ObjHeader: types.ObjHeader{CreatedAt: user.CreatedAt},
					User:      user.Id,
					Topic:     user.Uid().UserId(),
					ModeWant:  types.ModeSelf,
					ModeGiven: types.ModeSelf,
					Private:   private,
				})
			if err != nil {
				return nil, err
			}

			return user, nil
		} else {
			return nil, errors.New("store: invalid format of secret")
		}
	}
	return nil, errors.New("store: unknown authentication scheme '" + scheme + "'")

}

// Process user login. TODO(gene): abstract out the authentication scheme
func (UsersObjMapper) Login(appid uint32, scheme, secret string) (types.Uid, error) {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			uname := secret[:splitAt]
			password := secret[splitAt+1:]

			uid, hash, err := adaptr.GetPasswordHash(appid, uname)
			if err != nil {
				return types.ZeroUid, err
			} else if uid.IsZero() {
				// Invalid login
				return types.ZeroUid, nil
			}

			err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
			if err != nil {
				// Invalid password
				return types.ZeroUid, nil
			}
			//log.Println("Logged in as", uid, uid.String())
			return uid, nil
		} else {
			return types.ZeroUid, errors.New("store: invalid format of secret")
		}
	}
	return types.ZeroUid, errors.New("store: unknown authentication scheme '" + scheme + "'")
}

// TODO(gene): implement
func (UsersObjMapper) Get(appid uint32, uid types.Uid) (*types.User, error) {
	return adaptr.UserGet(appid, uid)
}

/*
func (u UsersObjMapper) GetLastSeenAndStatus(appid uint32, id types.Uid) (time.Time, interface{}, error) {
	return adaptr.GetLastSeenAndStatus(appid, id)
}
*/

// TODO(gene): implement
func (UsersObjMapper) Find(appId uint32, params map[string]interface{}) ([]types.User, error) {
	return nil, errors.New("store: not implemented")
}

// TODO(gene): implement
func (UsersObjMapper) Delete(appId uint32, id types.Uid, soft bool) error {
	return errors.New("store: not implemented")
}

func (UsersObjMapper) UpdateStatus(appid uint32, id types.Uid, status interface{}) error {
	return errors.New("store: not implemented")
}

// ChangePassword changes user's password in "basic" authentication scheme
func (UsersObjMapper) ChangeAuthCredential(appid uint32, uid types.Uid, scheme, secret string) error {
	if scheme == "basic" {
		if splitAt := strings.Index(secret, ":"); splitAt > 0 {
			return adaptr.ChangePassword(appid, uid, secret[splitAt+1:])
		}
		return errors.New("store: invalid format of secret")
	}
	return errors.New("store: unknown authentication scheme '" + scheme + "'")
}

func (UsersObjMapper) Update(appid uint32, uid types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.UserUpdate(appid, uid, update)
}

// GetSubs loads a list of subscriptions for the given user
func (u UsersObjMapper) GetSubs(appid uint32, id types.Uid, opts *types.BrowseOpt) ([]types.Subscription, error) {
	return adaptr.SubsForUser(appid, id, opts)
}

// GetTopics is exacly the same as Topics.GetForUser
func (u UsersObjMapper) GetTopics(appid uint32, id types.Uid, opts *types.BrowseOpt) ([]types.Subscription, error) {
	return adaptr.TopicsForUser(appid, id, opts)
}

// Topics struct to hold methods for persistence mapping for the topic object.
type TopicsObjMapper struct{}

var Topics TopicsObjMapper

// Create creates a topic and owner's subscription to topic
func (TopicsObjMapper) Create(appid uint32, topic *types.Topic, owner types.Uid, private interface{}) error {

	topic.InitTimes()

	err := adaptr.TopicCreate(appid, topic)
	if err != nil {
		return err
	}

	if !owner.IsZero() {
		err = Subs.Create(appid,
			&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: topic.CreatedAt},
				User:      owner.String(),
				Topic:     topic.Name,
				ModeGiven: types.ModeFull,
				ModeWant:  topic.GetAccess(owner),
				Private:   private})
	}

	return err
}

// CreateP2P creates a P2P topic by generating two user's subsciptions to each other.
func (TopicsObjMapper) CreateP2P(appid uint32, initiator, invited *types.Subscription) error {

	if users, err := adaptr.UserGetAll(appid, []types.Uid{
		types.ParseUid(initiator.User),
		types.ParseUid(invited.User)}); err != nil {
		return err
	} else if len(users) == 2 {
		var other = 1
		if users[0].Id == invited.User {
			other = 0
		}
		initiator.SetPublic(users[(other+1)%2].Public)
		invited.SetPublic(users[other].Public)
		invited.ModeWant = users[other].Access.Auth

	} else {
		// invited user does not exist
		return errors.New("invited user does not exist " + initiator.Topic)
	}

	// initiator is given as much access as permitted by the other user
	initiator.ModeGiven = invited.ModeWant

	initiator.InitTimes()

	invited.InitTimes()

	return adaptr.TopicCreateP2P(appid, initiator, invited)
}

// Get a single topic with a list of relevent users de-normalized into it
func (TopicsObjMapper) Get(appid uint32, topic string) (*types.Topic, error) {
	return adaptr.TopicGet(appid, topic)
}

// GetUsers loads subscriptions for topic plus loads user.Public
func (TopicsObjMapper) GetUsers(appid uint32, topic string, opts *types.BrowseOpt) ([]types.Subscription, error) {
	// Limit the number of subscriptions per topic
	if opts == nil {
		opts = &types.BrowseOpt{Limit: MAX_USERS_FOR_TOPIC}
	}
	return adaptr.UsersForTopic(appid, topic, opts)
}

// GetSubs loads a list of subscriptions to the given topic, user.Public is not loaded
func (TopicsObjMapper) GetSubs(appid uint32, topic string, opts *types.BrowseOpt) ([]types.Subscription, error) {
	// Limit the number of subscriptions per topic
	if opts == nil {
		opts = &types.BrowseOpt{Limit: MAX_USERS_FOR_TOPIC}
	}
	return adaptr.SubsForTopic(appid, topic, opts)
}

func (TopicsObjMapper) UpdateLastSeen(appid uint32, topic string, id types.Uid, tag string, when time.Time) error {
	return adaptr.UpdateLastSeen(appid, topic, id, tag, when)
}

func (TopicsObjMapper) Update(appid uint32, topic string, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.TopicUpdate(appid, topic, update)
}

// Topics struct to hold methods for persistence mapping for the topic object.
type SubsObjMapper struct{}

var Subs SubsObjMapper

func (SubsObjMapper) Create(appid uint32, sub *types.Subscription) error {
	sub.InitTimes()

	_, err := adaptr.TopicShare(appid, []types.Subscription{*sub})
	return err
}

func (SubsObjMapper) Get(appid uint32, topic string, user types.Uid) (*types.Subscription, error) {
	return adaptr.SubscriptionGet(appid, topic, user)
}

// Update changes values of user's subscription.
func (SubsObjMapper) Update(appid uint32, topic string, user types.Uid, update map[string]interface{}) error {
	update["UpdatedAt"] = types.TimeNow()
	return adaptr.SubsUpdate(appid, topic, user, update)
}

// Messages struct to hold methods for persistence mapping for the Message object.
type MessagesObjMapper struct{}

var Messages MessagesObjMapper

// Save message
func (MessagesObjMapper) Save(appid uint32, msg *types.Message) error {
	msg.InitTimes()

	// Need a transaction here, RethinkDB does not support transactions
	if err := adaptr.TopicUpdateLastMsgTime(appid, msg.Topic, msg.CreatedAt); err != nil {
		return err
	}

	return adaptr.MessageSave(appid, msg)
}

// Soft-delete semmsages for the current user
func (MessagesObjMapper) DeleteAll(appId uint32, user types.Uid, topic string) error {
	return errors.New("store: not implemented")
}

func (MessagesObjMapper) GetAll(appid uint32, topic string, opt *types.BrowseOpt) ([]types.Message, error) {
	return adaptr.MessageGetAll(appid, topic, opt)
}

func (MessagesObjMapper) Delete(appId uint32, uid types.Uid) error {
	return errors.New("store: not implemented")
}

func ZeroUid() types.Uid {
	return types.ZeroUid
}

func UidFromBytes(b []byte) types.Uid {
	var uid types.Uid
	(&uid).UnmarshalBinary(b)
	return uid
}
