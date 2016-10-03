// Package adapter contains the interfaces to be implemented by the database adapter
package adapter

import (
	"time"

	t "github.com/tinode/chat/server/store/types"
)

// Adapter is the interface that must be implemented by a database
// adapter. The current schema supports a single connection by database type.
type Adapter interface {
	Open(config string) error
	Close() error
	IsOpen() bool

	CreateDb(reset bool) error

	// User management
	UserCreate(usr *t.User) (err error, dupeUserName bool)
	GetAuthRecord(unique string) (t.Uid, []byte, time.Time, error)
	AddAuthRecord(user t.Uid, unique string, secret []byte, expires time.Time) (error, bool)
	DelAuthRecord(unique string) (int, error)
	DelAllAuthRecords(uid t.Uid) (int, error)
	UpdAuthRecord(unique string, secret []byte, expires time.Time) (int, error)
	UserGet(id t.Uid) (*t.User, error)
	UserGetAll(ids ...t.Uid) ([]t.User, error)
	UserDelete(id t.Uid, soft bool) error
	UserUpdateLastSeen(uid t.Uid, userAgent string, when time.Time) error
	//UserUpdateStatus(uid t.Uid, status interface{}) error
	ChangePassword(id t.Uid, password string) error
	UserUpdate(uid t.Uid, update map[string]interface{}) error

	// Topic/contact management

	// TopicCreate creates a topic
	TopicCreate(topic *t.Topic) error
	// TopicCreateP2P creates a p2p topic
	TopicCreateP2P(initiator, invited *t.Subscription) error
	// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
	TopicGet(topic string) (*t.Topic, error)
	// TopicsForUser loads subscriptions for a given user
	TopicsForUser(uid t.Uid) ([]t.Subscription, error)
	// UsersForTopic loads users' subscriptions for a given topic
	UsersForTopic(topic string) ([]t.Subscription, error)
	TopicShare(subs []*t.Subscription) (int, error)
	TopicDelete(topic string) error
	TopicUpdateOnMessage(topic string, msg *t.Message) error
	TopicUpdate(topic string, update map[string]interface{}) error

	// SubscriptionGet reads a subscription of a user to a topic
	SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error)
	// SubsForUser gets a list of topics of interest for a given user
	SubsForUser(user t.Uid) ([]t.Subscription, error)
	// SubsForTopic gets a list of subscriptions to a given topic
	SubsForTopic(topic string) ([]t.Subscription, error)
	// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
	SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error
	// SubsDelete deletes a single subscription
	SubsDelete(topic string, user t.Uid) error
	// SubsDelForTopic deletes all subscriptions to the given topic
	SubsDelForTopic(topic string) error
	// Search for new contacts given a list of tags
	FindSubs(user t.Uid, query []interface{}) ([]t.Subscription, error)

	// Messages
	MessageSave(msg *t.Message) error
	MessageGetAll(topic string, opts *t.BrowseOpt) ([]t.Message, error)
	MessageDeleteAll(topic string, before int) error

	// Devices (for push notifications)
	DeviceUpsert(user t.Uid, deviceId string, updated time.Time) error
	DeviceGetAll(user t.Uid) ([]string, error)
}
