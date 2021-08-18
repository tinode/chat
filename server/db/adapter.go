// Package adapter contains the interfaces to be implemented by the database adapter
package adapter

import (
	"encoding/json"
	"time"

	"github.com/tinode/chat/server/auth"
	t "github.com/tinode/chat/server/store/types"
)

// Adapter is the interface that must be implemented by a database
// adapter. The current schema supports a single connection by database type.
type Adapter interface {
	// General

	// Open and configure the adapter
	Open(config json.RawMessage) error
	// Close the adapter
	Close() error
	// IsOpen checks if the adapter is ready for use
	IsOpen() bool
	// GetDbVersion returns current database version.
	GetDbVersion() (int, error)
	// CheckDbVersion checks if the actual database version matches adapter version.
	CheckDbVersion() error
	// GetName returns the name of the adapter
	GetName() string
	// SetMaxResults configures how many results can be returned in a single DB call.
	SetMaxResults(val int) error
	// CreateDb creates the database optionally dropping an existing database first.
	CreateDb(reset bool) error
	// UpgradeDb upgrades database to the current adapter version.
	UpgradeDb() error
	// Version returns adapter version
	Version() int
	// DB connection stats object.
	Stats() interface{}

	// User management

	// UserCreate creates user record
	UserCreate(user *t.User) error
	// UserGet returns record for a given user ID
	UserGet(uid t.Uid) (*t.User, error)
	// UserGetAll returns user records for a given list of user IDs
	UserGetAll(ids ...t.Uid) ([]t.User, error)
	// UserDelete deletes user record
	UserDelete(uid t.Uid, hard bool) error
	// UserUpdate updates user record
	UserUpdate(uid t.Uid, update map[string]interface{}) error
	// UserUpdateTags adds, removes, or resets user's tags
	UserUpdateTags(uid t.Uid, add, remove, reset []string) ([]string, error)
	// UserGetByCred returns user ID for the given validated credential.
	UserGetByCred(method, value string) (t.Uid, error)
	// UserUnreadCount returns the total number of unread messages in all topics with
	// the R permission.
	UserUnreadCount(uid t.Uid) (int, error)

	// Credential management

	// CredUpsert adds or updates a credential record. Returns true if record was inserted, false if updated.
	CredUpsert(cred *t.Credential) (bool, error)
	// CredGetActive returns the currently active credential record for the given method.
	CredGetActive(uid t.Uid, method string) (*t.Credential, error)
	// CredGetAll returns credential records for the given user and method, validated only or all.
	CredGetAll(uid t.Uid, method string, validatedOnly bool) ([]t.Credential, error)
	// CredDel deletes credentials for the given method/value. If method is empty, deletes all
	// user's credentials.
	CredDel(uid t.Uid, method, value string) error
	// CredConfirm marks given credential as validated.
	CredConfirm(uid t.Uid, method string) error
	// CredFail increments count of failed validation attepmts for the given credentials.
	CredFail(uid t.Uid, method string) error

	// Authentication management for the basic authentication scheme

	// AuthGetUniqueRecord returns authentication record for a given unique value i.e. login.
	AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error)
	// AuthGetRecord returns authentication record given user ID and method.
	AuthGetRecord(user t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error)
	// AuthAddRecord creates new authentication record
	AuthAddRecord(user t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) error
	// AuthDelScheme deletes an existing authentication scheme for the user.
	AuthDelScheme(user t.Uid, scheme string) error
	// AuthDelAllRecords deletes all records of a given user.
	AuthDelAllRecords(uid t.Uid) (int, error)
	// AuthUpdRecord modifies an authentication record. Only non-default/non-zero values are updated.
	AuthUpdRecord(user t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) error

	// Topic management

	// TopicCreate creates a topic
	TopicCreate(topic *t.Topic) error
	// TopicCreateP2P creates a p2p topic
	TopicCreateP2P(initiator, invited *t.Subscription) error
	// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
	TopicGet(topic string) (*t.Topic, error)
	// TopicsForUser loads subscriptions for a given user. Reads public value.
	// When the 'opts.IfModifiedSince' query is not nil the subscriptions with UpdatedAt > opts.IfModifiedSince
	// are returned, where UpdatedAt can be either a subscription, a topic, or a user update timestamp.
	// This is need in order to support paginagion of subscriptions: get subscriptions page by page
	// from the oldest updates to most recent:
	// 1. Client already has subscriptions with the latest update timestamp X.
	// 2. Client asks for N updated subscriptions since X. The server returns N with updates between X and Y.
	// 3. Client goes to step 1 with X := Y.
	TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error)
	// UsersForTopic loads users' subscriptions for a given topic. Public is loaded.
	UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error)
	// OwnTopics loads a slice of topic names where the user is the owner.
	OwnTopics(uid t.Uid) ([]string, error)
	// ChannelsForUser loads a slice of topic names where the user is a channel reader and notifications (P) are enabled.
	ChannelsForUser(uid t.Uid) ([]string, error)
	// TopicShare creates topc subscriptions
	TopicShare(subs []*t.Subscription) error
	// TopicDelete deletes topic, subscription, messages
	TopicDelete(topic string, hard bool) error
	// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
	TopicUpdateOnMessage(topic string, msg *t.Message) error
	// TopicUpdate updates topic record.
	TopicUpdate(topic string, update map[string]interface{}) error
	// TopicOwnerChange updates topic's owner
	TopicOwnerChange(topic string, newOwner t.Uid) error
	// Topic subscriptions

	// SubscriptionGet reads a subscription of a user to a topic
	SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error)
	// SubsForUser loads all subscriptions of a given user. Does NOT load Public or Private values,
	// does not load deleted subscriptions.
	SubsForUser(user t.Uid) ([]t.Subscription, error)
	// SubsForTopic gets a list of subscriptions to a given topic.. Does NOT load Public value.
	SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error)
	// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
	SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error
	// SubsDelete deletes a single subscription
	SubsDelete(topic string, user t.Uid) error

	// Search

	// FindUsers searches for new contacts given a list of tags
	FindUsers(user t.Uid, req [][]string, opt []string) ([]t.Subscription, error)
	// FindTopics searches for group topics given a list of tags
	FindTopics(req [][]string, opt []string) ([]t.Subscription, error)

	// Messages

	// MessageSave saves message to database
	MessageSave(msg *t.Message) error
	// MessageGetAll returns messages matching the query
	MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error)
	// MessageDeleteList marks messages as deleted.
	// Soft- or Hard- is defined by forUser value: forUSer.IsZero == true is hard.
	MessageDeleteList(topic string, toDel *t.DelMessage) error
	// MessageGetDeleted returns a list of deleted message Ids.
	MessageGetDeleted(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.DelMessage, error)

	// Devices (for push notifications)

	// DeviceUpsert creates or updates a device record
	DeviceUpsert(uid t.Uid, dev *t.DeviceDef) error
	// DeviceGetAll returns all devices for a given set of users
	DeviceGetAll(uid ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error)
	// DeviceDelete deletes a device record
	DeviceDelete(uid t.Uid, deviceID string) error

	// File upload records. The files are stored outside of the database.

	// FileStartUpload initializes a file upload.
	FileStartUpload(fd *t.FileDef) error
	// FileFinishUpload marks file upload as completed, successfully or otherwise.
	FileFinishUpload(fd *t.FileDef, success bool, size int64) (*t.FileDef, error)
	// FileGet fetches a record of a specific file
	FileGet(fid string) (*t.FileDef, error)
	// FileDeleteUnused deletes records where UseCount is zero. If olderThan is non-zero, deletes
	// unused records with UpdatedAt before olderThan.
	// Returns array of FileDef.Location of deleted filerecords so actual files can be deleted too.
	FileDeleteUnused(olderThan time.Time, limit int) ([]string, error)
	// FileLinkAttachments connects given topic or message to the file record IDs from the list.
	FileLinkAttachments(topic string, userId, msgId t.Uid, fids []string) error
}
