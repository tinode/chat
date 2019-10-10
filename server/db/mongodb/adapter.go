// +build mongodb

package mongodb

import (
	c "context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	"go.mongodb.org/mongo-driver/bson"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"
)

// adapter holds MongoDB connection data.
type adapter struct {
	conn       *mdb.Client
	db         *mdb.Database
	dbName     string
	maxResults int
	version    int
}

const (
	defaultHost     = "localhost:27017"
	defaultDatabase = "tinode"

	adpVersion  = 108
	adapterName = "mongodb"

	defaultMaxResults = 1024
)

// See https://godoc.org/go.mongodb.org/mongo-driver/mongo/options#ClientOptions for explanations.
type configType struct {
	Addresses      interface{} `json:"addresses,omitempty"`
	ConnectTimeout int         `json:"timeout,omitempty"`

	// Separately from ClientOptions (additional options):
	Database string `json:"database,omitempty"`
}

// Open initializes mongodb session
func (a *adapter) Open(jsonconfig json.RawMessage) error {
	if a.conn != nil {
		return errors.New("adapter mongodb is already connected")
	}

	var err error
	var config configType
	if err = json.Unmarshal(jsonconfig, &config); err != nil {
		return errors.New("adapter mongodb failed to parse config: " + err.Error())
	}

	var opts mdbopts.ClientOptions

	if config.Addresses == nil {
		opts.SetHosts([]string{defaultHost})
	} else if host, ok := config.Addresses.(string); ok {
		opts.SetHosts([]string{host})
	} else if hosts, ok := config.Addresses.([]string); ok {
		opts.SetHosts(hosts)
	} else {
		return errors.New("adapter mongodb failed to parse config.Addresses")
	}

	if config.Database == "" {
		a.dbName = defaultDatabase
	} else {
		a.dbName = config.Database
	}

	if a.maxResults <= 0 {
		a.maxResults = defaultMaxResults
	}

	a.conn, err = mdb.Connect(c.TODO(), &opts)
	a.db = a.conn.Database(a.dbName)
	if err != nil {
		return err
	}

	a.version = -1

	return nil
}

// Close the adapter
func (a *adapter) Close() error {
	var err error
	if a.conn != nil {
		err = a.conn.Disconnect(c.TODO())
		a.conn = nil
		a.version = -1
	}
	return err
}

// IsOpen checks if the adapter is ready for use
func (a *adapter) IsOpen() bool {
	return a.conn != nil
}

// GetDbVersion returns current database version.
func (a *adapter) GetDbVersion() (int, error) {
	if a.version > 0 {
		return a.version, nil
	}

	var result struct {
		Key   string
		Value int
	}
	if err := a.db.Collection("kvmeta").FindOne(c.TODO(), bson.D{{"key", "version"}}).Decode(&result); err != nil {
		return -1, err
	}

	a.version = result.Value
	return result.Value, nil
}

// CheckDbVersion checks if the actual database version matches adapter version.
func (a *adapter) CheckDbVersion() error {
	version, err := a.GetDbVersion()
	if err != nil {
		return err
	}

	if version != adpVersion {
		return errors.New("Invalid database version " + strconv.Itoa(version) +
			". Expected " + strconv.Itoa(adpVersion))
	}

	return nil
}

// Version returns adapter version
func (a *adapter) Version() int {
	return adpVersion
}

// GetName returns the name of the adapter
func (a *adapter) GetName() string {
	return adapterName
}

// SetMaxResults configures how many results can be returned in a single DB call.
func (a *adapter) SetMaxResults(val int) error {
	if val <= 0 {
		a.maxResults = defaultMaxResults
	} else {
		a.maxResults = val
	}

	return nil
}

// CreateDb creates the database optionally dropping an existing database first.
func (a *adapter) CreateDb(reset bool) error {
	if reset {
		log.Print("Dropping database...")
		if err := a.db.Drop(c.TODO()); err != nil {
			return nil
		}
	}

	// Collections (tables) do not need to be explicitly created since MongoDB creates them with first write operation

	// Collection with metadata key-value pairs.
	var idxOpts mdbopts.IndexOptions
	idxOpts.SetUnique(true)
	if _, err := a.db.Collection("kvmeta").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"key": 1}, Options: &idxOpts}); err != nil {
		return err
	}

	// Users
	// Create secondary index on User.DeletedAt for finding soft-deleted users
	if _, err := a.db.Collection("users").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"DeletedAt": 1}}); err != nil {
		return err
	}
	// Create secondary index on User.Tags array so user can be found by tags
	if _, err := a.db.Collection("users").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"Tags": 1}}); err != nil {
		return err
	}
	// TODO: Create secondary index for User.Devices.<hash>.DeviceId to ensure ID uniqueness across users

	// User authentication records {unique, userid, secret}
	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("auth").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"userid": 1}}); err != nil {
		return err
	}

	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"User": 1}}); err != nil {
		return err
	}
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"Topic": 1}}); err != nil {
		return err
	}

	// Topics stored in database
	// Secondary index on Owner field for deleting users.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"Owner": 1}}); err != nil {
		return err
	}
	// Secondary index on Topic.Tags array so topics can be found by tags.
	// These tags are not unique as opposite to User.Tags.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"Tags": 1}}); err != nil {
		return err
	}
	// Create system topic 'sys'.
	if err := createSystemTopic(a); err != nil {
		return err
	}

	// Stored message
	// TODO: Compound index of topic - seqID for selecting messages in a topic.
	// TODO: Compound index of hard-deleted messages
	// TODO: Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
	// 		 [Topic, User1, DelId1], [Topic, User2, DelId2],...

	// Log of deleted messages
	// TODO: Compound index of topic - delId

	// User credentials - contact information such as "email:jdoe@example.com" or "tel:+18003287448":
	// Id: "method:credential" like "email:jdoe@example.com". See types.Credential.
	// Create secondary index on credentials.User to be able to query credentials by user id.
	if _, err := a.db.Collection("credentials").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"User": 1}}); err != nil {
		return err
	}

	// Records of file uploads. See types.FileDef.
	// A secondary index on fileuploads.User to be able to get records by user id.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"User": 1}}); err != nil {
		return err
	}
	// A secondary index on fileuploads.UseCount to be able to delete unused records at once.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(c.TODO(), mdb.IndexModel{Keys: bson.M{"UseCount": 1}}); err != nil {
		return err
	}

	// Record current DB version.
	if _, err := a.db.Collection("kvmeta").InsertOne(c.TODO(), map[string]interface{}{"key": "version", "value": adpVersion}); err != nil {
		return err
	}

	return nil
}

// TODO:
// UpgradeDb upgrades database to the current adapter version.
func (a *adapter) UpgradeDb() error {
	return nil
}

// Create system topic 'sys'.
func createSystemTopic(a *adapter) error {
	now := t.TimeNow()
	_, err := a.db.Collection("topics").InsertOne(c.TODO(), &t.Topic{
		ObjHeader: t.ObjHeader{Id: "sys", CreatedAt: now, UpdatedAt: now},
		Access:    t.DefaultAccess{Auth: t.ModeNone, Anon: t.ModeNone},
		Public:    map[string]interface{}{"fn": "System"},
	})
	return err
}

// User management
// UserCreate creates user record
func (a *adapter) UserCreate(usr *t.User) error {
	if _, err := a.db.Collection("users").InsertOne(c.TODO(), &usr); err != nil {
		return err
	}

	return nil
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

func init() {
	store.RegisterAdapter(&adapter{})
}
