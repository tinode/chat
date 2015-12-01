// Package adapter contains the interfaces to be implemented by the database adapter
package adapter

import (
	t "github.com/tinode/chat/server/store/types"
	"time"
)

// Adapter is the interface that must be implemented by a database
// adapter. The current schema supports a single connection by database type.
type Adapter interface {
	Open(config string, workerId int, uidkey []byte) error
	Close() error
	IsOpen() bool

	CreateDb(reset bool) error

	// User management
	GetPasswordHash(appid uint32, username string) (t.Uid, []byte, error)
	UserCreate(appid uint32, usr *t.User) (err error, dupeUserName bool)
	UserGet(appId uint32, id t.Uid) (*t.User, error)
	UserGetAll(appId uint32, ids ...t.Uid) ([]t.User, error)
	UserFind(appId uint32, params map[string]interface{}) ([]t.User, error)
	UserDelete(appId uint32, id t.Uid, soft bool) error
	UserUpdateLastSeen(appid uint32, uid t.Uid, userAgent string, when time.Time) error
	UserUpdateStatus(appid uint32, uid t.Uid, status interface{}) error
	ChangePassword(appid uint32, id t.Uid, password string) error
	UserUpdate(appid uint32, uid t.Uid, update map[string]interface{}) error

	// Topic/contact management

	// TopicCreate creates a topic
	TopicCreate(appid uint32, topic *t.Topic) error
	// TopicCreateP2P creates a p2p topic
	TopicCreateP2P(appId uint32, initiator, invited *t.Subscription) error
	// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
	TopicGet(appid uint32, topic string) (*t.Topic, error)
	// TopicsForUser loads subscriptions for a given user
	TopicsForUser(appid uint32, uid t.Uid) ([]t.Subscription, error)
	// UsersForTopic loads users' subscriptions for a given topic
	UsersForTopic(appid uint32, topic string) ([]t.Subscription, error)
	TopicShare(appid uint32, acl []t.Subscription) (int, error)
	TopicDelete(appid uint32, userDbId, topic string) error
	TopicUpdateOnMessage(appid uint32, topic string, msg *t.Message) error
	TopicUpdate(appid uint32, topic string, update map[string]interface{}) error

	// SubscriptionGet reads a subscription of a user to a topic
	SubscriptionGet(appid uint32, topic string, user t.Uid) (*t.Subscription, error)
	// SubsForUser gets a list of topics of interest for a given user
	SubsForUser(appId uint32, user t.Uid) ([]t.Subscription, error)
	// SubsForTopic gets a list of subscriptions to a given topic
	SubsForTopic(appId uint32, topic string) ([]t.Subscription, error)
	// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
	SubsUpdate(appid uint32, topic string, user t.Uid, update map[string]interface{}) error
	// SubsDelete deletes a subscription
	SubsDelete(appid uint32, topic string, user t.Uid) error

	// Messages
	MessageSave(appId uint32, msg *t.Message) error
	MessageGetAll(appId uint32, topic string, opts *t.BrowseOpt) ([]t.Message, error)
	MessageDelete(appId uint32, id t.Uid) error
}
