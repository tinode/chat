// +build mongodb

package mongodb

import (
	"time"

	"github.com/tinode/chat/server/auth"
	t "github.com/tinode/chat/server/store/types"
	mdb "go.mongodb.org/mongo-driver/mongo"
)

// adapter holds MongoDB connection data.
type adapter struct {
	conn       *mdb.Client
	dbName     string
	maxResults int
	version    int
}

const (
	defaultHost       = "localhost:27017"
	defaultCollection = "tinode"

	adpVersion  = 108
	adapterName = "mongodb"

	defaultMaxResults = 1024
)

// See https://godoc.org/go.mongodb.org/mongo-driver/mongo/options#ClientOptions for explanations.
type configType struct {
	Hosts          interface{} `json:"addresses,omitempty"`
	ConnectTimeout int         `json:"timeout,omitempty"`

	// Separately from ClientOptions (additional options):
	Collection string `json:"database,omitempty"`
}

// Open initializes mongodb session
func (a *adapter) Open(jsonconfig string) error {
	return nil
}

// Close the adapter
func (a *adapter) Close() error {
	return nil
}

// IsOpen checks if the adapter is ready for use
func (a *adapter) IsOpen() bool {
	return false
}

// GetDbVersion returns current database version.
func (a *adapter) GetDbVersion() (int, error) {
	return 0, nil
}

// CheckDbVersion checks if the actual database version matches adapter version.
func (a *adapter) CheckDbVersion() error {
	return nil
}

// GetName returns the name of the adapter
func (a *adapter) GetName() string {
	return ""
}

// SetMaxResults configures how many results can be returned in a single DB call.
func (a *adapter) SetMaxResults(val int) error {
	return nil
}

// CreateDb creates the database optionally dropping an existing database first.
func (a *adapter) CreateDb(reset bool) error {
	return nil
}

// UpgradeDb upgrades database to the current adapter version.
func (a *adapter) UpgradeDb() error {
	return nil
}

// Version returns adapter version
func (a *adapter) Version() int {
	return 0
}

// User management

// UserCreate creates user record
func (a *adapter) UserCreate(usr *t.User) error {
	return  nil
}

// UserGet returns record for a given user ID
func (a *adapter) UserGet(id t.Uid) (*t.User, error) {
	return &t.User{}, nil
}

// UserGetAll returns user records for a given list of user IDs
func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	return []t.User{}, nil
}

// UserDelete deletes user record
func (a *adapter) UserDelete(id t.Uid, hard bool) error {
	return nil
}

// UserGetDisabled returns IDs of users which were soft-deleted since given time.
func (a *adapter) UserGetDisabled(time.Time) ([]t.Uid, error) {
	return []t.Uid{}, nil
}

// UserUpdate updates user record
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	return nil
}

// UserUpdateTags adds, removes, or resets user's tags
func (a *adapter) UserUpdateTags(uid t.Uid, add, remove, reset []string) ([]string, error) {
	return nil, nil
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	return 0, nil
}

// UserUnreadCount returns the total number of unread messages in all topics with
// the R permission.
func (a *adapter) UserUnreadCount(uid t.Uid) (int, error) {
	return 0, nil
}

// Credential management

// CredUpsert adds or updates a credential record. Returns true if record was inserted, false if updated.
func (a *adapter) CredUpsert(cred *t.Credential) (bool, error) {
	return false, nil
}

// CredGetActive returns the currently active credential record for the given method.
func (a *adapter) CredGetActive(uid t.Uid, method string) (*t.Credential, error) {
	return &t.Credential{}, nil
}

// CredGetAll returns credential records for the given user and method, validated only or all.
func (a *adapter) CredGetAll(uid t.Uid, method string, validatedOnly bool) ([]t.Credential, error) {
	return nil, nil
}

// CredIsConfirmed returns true if the given credential method has been verified, false otherwise.
func (a *adapter) CredIsConfirmed(uid t.Uid, metod string) (bool, error) {
	return false, nil
}

// CredDel deletes credentials for the given method/value. If method is empty, deletes all
// user's credentials.
func (a *adapter) CredDel(uid t.Uid, method, value string) error {
	return nil
}

// CredConfirm marks given credential as validated.
func (a *adapter) CredConfirm(uid t.Uid, method string) error {
	return nil
}

// CredFail increments count of failed validation attepmts for the given credentials.
func (a *adapter) CredFail(uid t.Uid, method string) error {
	return nil
}

// Authentication management for the basic authentication scheme

// AuthGetUniqueRecord returns authentication record for a given unique value i.e. login.
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	return 0, 0, nil, time.Time{}, nil
}

// AuthGetRecord returns authentication record given user ID and method.
func (a *adapter) AuthGetRecord(user t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	return "", 0, nil, time.Time{}, nil
}

// AuthAddRecord creates new authentication record
func (a *adapter) AuthAddRecord(user t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) (bool, error) {
	return false, nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(user t.Uid, scheme string) error {
	return nil
}

// AuthDelAllRecords deletes all records of a given user.
func (a *adapter) AuthDelAllRecords(uid t.Uid) (int, error) {
	return 0, nil
}

// AuthUpdRecord modifies an authentication record.
func (a *adapter) AuthUpdRecord(user t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) (bool, error) {
	return false, nil
}

// Topic management

// TopicCreate creates a topic
func (a *adapter) TopicCreate(topic *t.Topic) error {
	return nil
}

// TopicCreateP2P creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	return nil
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	return &t.Topic{}, nil
}

// TopicsForUser loads subscriptions for a given user. Reads public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// UsersForTopic loads users' subscriptions for a given topic. Public is loaded.
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid, opts *t.QueryOpt) ([]string, error) {
	return nil, nil
}

// TopicShare creates topc subscriptions
func (a *adapter) TopicShare(subs []*t.Subscription) (int, error) {
	return 0, nil
}

// TopicDelete deletes topic, subscription, messages
func (a *adapter) TopicDelete(topic string, hard bool) error {
	return nil
}

// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	return nil
}

// TopicUpdate updates topic record.
func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	return nil
}

// TopicOwnerChange updates topic's owner
func (a *adapter) TopicOwnerChange(topic string, newOwner, oldOwner t.Uid) error {
	return nil
}

// Topic subscriptions

// SubscriptionGet reads a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {
	return &t.Subscription{}, nil
}

// SubsForUser gets a list of topics of interest for a given user. Does NOT load Public value.
func (a *adapter) SubsForUser(user t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// SubsForTopic gets a list of subscriptions to a given topic.. Does NOT load Public value.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	return nil
}

// SubsDelete deletes a single subscription
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	return nil
}

// SubsDelForTopic deletes all subscriptions to the given topic
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	return nil
}

// SubsDelForUser deletes all subscriptions of the given user
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	return nil
}

// Search

// FindUsers searches for new contacts given a list of tags
func (a *adapter) FindUsers(user t.Uid, req, opt []string) ([]t.Subscription, error) {
	return nil, nil
}

// FindTopics searches for group topics given a list of tags
func (a *adapter) FindTopics(req, opt []string) ([]t.Subscription, error) {
	return nil, nil
}

// Messages

// MessageSave saves message to database
func (a *adapter) MessageSave(msg *t.Message) error {
	return nil
}

// MessageGetAll returns messages matching the query
func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {
	return nil, nil
}

// MessageDeleteList marks messages as deleted.
// Soft- or Hard- is defined by forUser value: forUSer.IsZero == true is hard.
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	return nil
}

// MessageGetDeleted returns a list of deleted message Ids.
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.DelMessage, error) {
	return nil, nil
}

// MessageAttachments connects given message to a list of file record IDs.
func (a *adapter) MessageAttachments(msgId t.Uid, fids []string) error {
	return nil
}

// Devices (for push notifications)

// DeviceUpsert creates or updates a device record
func (a *adapter) DeviceUpsert(uid t.Uid, dev *t.DeviceDef) error {
	return nil
}

// DeviceGetAll returns all devices for a given set of users
func (a *adapter) DeviceGetAll(uid ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	return nil, 0, nil
}

// DeviceDelete deletes a device record
func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	return nil
}

// File upload records. The files are stored outside of the database.

// FileStartUpload initializes a file upload
func (a *adapter) FileStartUpload(fd *t.FileDef) error {
	return nil
}

// FileFinishUpload marks file upload as completed, successfully or otherwise.
func (a *adapter) FileFinishUpload(fid string, status int, size int64) (*t.FileDef, error) {
	return &t.FileDef{}, nil
}

// FileGet fetches a record of a specific file
func (a *adapter) FileGet(fid string) (*t.FileDef, error) {
	return &t.FileDef{}, nil
}

// FileDeleteUnused deletes records where UseCount is zero. If olderThan is non-zero, deletes
// unused records with UpdatedAt before olderThan.
// Returns array of FileDef.Location of deleted filerecords so actual files can be deleted too.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	return nil, nil
}
