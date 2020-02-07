// +build mongodb

// Package mongodb is a database adapter for MongoDB.
package mongodb

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	b "go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"
)

// adapter holds MongoDB connection data.
type adapter struct {
	conn            *mdb.Client
	db              *mdb.Database
	dbName          string
	maxResults      int
	version         int
	ctx             context.Context
	useTransactions bool
}

const (
	defaultHost     = "localhost:27017"
	defaultDatabase = "tinode"

	adpVersion  = 111
	adapterName = "mongodb"

	defaultMaxResults = 1024
)

// See https://godoc.org/go.mongodb.org/mongo-driver/mongo/options#ClientOptions for explanations.
type configType struct {
	Addresses      interface{} `json:"addresses,omitempty"`
	ConnectTimeout int         `json:"timeout,omitempty"`

	// Options separately from ClientOptions (custom options):
	Database   string `json:"database,omitempty"`
	ReplicaSet string `json:"replica_set,omitempty"`

	AuthSource string `json:"auth_source,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
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

	if config.ReplicaSet == "" {
		log.Println("MongoDB configured as standalone or replica_set option not set. Transaction support is disabled.")
	} else {
		opts.SetReplicaSet(config.ReplicaSet)
		a.useTransactions = true
	}

	if config.Username != "" {
		var passwordSet bool
		if config.AuthSource == "" {
			config.AuthSource = "admin"
		}
		if config.Password != "" {
			passwordSet = true
		}
		opts.SetAuth(
			mdbopts.Credential{
				AuthMechanism: "SCRAM-SHA-256",
				AuthSource:    config.AuthSource,
				Username:      config.Username,
				Password:      config.Password,
				PasswordSet:   passwordSet,
			})
	}

	if a.maxResults <= 0 {
		a.maxResults = defaultMaxResults
	}

	a.ctx = context.Background()
	a.conn, err = mdb.Connect(a.ctx, &opts)
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
		err = a.conn.Disconnect(a.ctx)
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
		Key   string `bson:"_id"`
		Value int
	}
	if err := a.db.Collection("kvmeta").FindOne(a.ctx, b.M{"_id": "version"}).Decode(&result); err != nil {
		if err == mdb.ErrNoDocuments {
			err = errors.New("Database not initialized")
		}
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
		if err := a.db.Drop(a.ctx); err != nil {
			return err
		}
	} else if a.isDbInitialized() {
		return errors.New("Database already initialized")
	}
	// Collections (tables) do not need to be explicitly created since MongoDB creates them with first write operation

	indexes := []struct {
		Collection string
		Field      string
		IndexOpts  mdb.IndexModel
	}{
		// Users
		// Index on 'user.state' for finding suspended and soft-deleted users.
		{
			Collection: "users",
			Field:      "state",
		},
		// Index on 'user.tags' array so user can be found by tags.
		{
			Collection: "users",
			Field:      "tags",
		},
		// Index for 'user.devices.deviceid' to ensure Device ID uniqueness across users.
		// Partial filter set to avoid unique constraint for null values (when user object have no devices).
		{
			Collection: "users",
			IndexOpts: mdb.IndexModel{
				Keys: b.M{"devices.deviceid": 1},
				Options: mdbopts.Index().
					SetUnique(true).
					SetPartialFilterExpression(b.M{"devices.deviceid": b.M{"$exists": true}}),
			},
		},

		// User authentication records {_id, userid, secret}
		// Should be able to access user's auth records by user id
		{
			Collection: "auth",
			Field:      "userid",
		},

		// Subscription to a topic. The primary key is a topic:user string
		{
			Collection: "subscriptions",
			Field:      "user",
		},
		{
			Collection: "subscriptions",
			Field:      "topic",
		},

		// Topics stored in database
		// Index on 'owner' field for deleting users.
		{
			Collection: "topics",
			Field:      "owner",
		},
		// Index on 'state' for finding suspended and soft-deleted topics.
		{
			Collection: "topics",
			Field:      "state",
		},
		// Index on 'topic.tags' array so topics can be found by tags.
		// These tags are not unique as opposite to 'user.tags'.
		{
			Collection: "topics",
			Field:      "tags",
		},

		// Stored message
		// Compound index of 'topic - seqid' for selecting messages in a topic.
		{
			Collection: "messages",
			IndexOpts:  mdb.IndexModel{Keys: b.M{"topic": 1, "seqid": 1}},
		},
		// Compound index of hard-deleted messages
		{
			Collection: "messages",
			IndexOpts:  mdb.IndexModel{Keys: b.M{"topic": 1, "delid": 1}},
		},
		// Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
		// 		 [topic, user1, delid1], [topic, user2, delid2],...
		{
			Collection: "messages",
			IndexOpts:  mdb.IndexModel{Keys: b.M{"topic": 1, "deletedfor.user": 1, "deletedfor.delid": 1}},
		},

		// Log of deleted messages
		// Compound index of 'topic - delid'
		{
			Collection: "dellog",
			IndexOpts:  mdb.IndexModel{Keys: b.M{"topic": 1, "delid": 1}},
		},

		// User credentials - contact information such as "email:jdoe@example.com" or "tel:+18003287448":
		// Id: "method:credential" like "email:jdoe@example.com". See types.Credential.
		// Index on 'credentials.user' to be able to query credentials by user id.
		{
			Collection: "credentials",
			Field:      "user",
		},

		// Records of file uploads. See types.FileDef.
		// Index on 'fileuploads.user' to be able to get records by user id.
		{
			Collection: "fileuploads",
			Field:      "user",
		},
		// Index on 'fileuploads.usecount' to be able to delete unused records at once.
		{
			Collection: "fileuploads",
			Field:      "usecount",
		},
	}

	var err error
	for _, idx := range indexes {
		if idx.Field != "" {
			_, err = a.db.Collection(idx.Collection).Indexes().CreateOne(a.ctx, mdb.IndexModel{Keys: b.M{idx.Field: 1}})
		} else {
			_, err = a.db.Collection(idx.Collection).Indexes().CreateOne(a.ctx, idx.IndexOpts)
		}
		if err != nil {
			return err
		}
	}

	// Collection "kvmeta" with metadata key-value pairs.
	// Key in "_id" field.
	// Record current DB version.
	if _, err := a.db.Collection("kvmeta").InsertOne(a.ctx, map[string]interface{}{"_id": "version", "value": adpVersion}); err != nil {
		return err
	}

	// Create system topic 'sys'.
	if err := createSystemTopic(a); err != nil {
		return err
	}

	return nil
}

// UpgradeDb upgrades database to the current adapter version.
func (a *adapter) UpgradeDb() error {
	bumpVersion := func(a *adapter, x int) error {
		if err := a.updateDbVersion(x); err != nil {
			return err
		}
		_, err := a.GetDbVersion()
		return err
	}

	_, err := a.GetDbVersion()
	if err != nil {
		return err
	}

	if a.version == 110 {
		// Perform database upgrade from versions 110 to version 111.

		// Users

		// Reset previously unused field State to value StateOK.
		if _, err := a.db.Collection("users").UpdateMany(a.ctx,
			b.M{},
			b.M{"$set": b.M{"state": t.StateOK}}); err != nil {
			return err
		}

		// Add StatusDeleted to all deleted users as indicated by DeletedAt not being null.
		if _, err := a.db.Collection("users").UpdateMany(a.ctx,
			b.M{"deletedat": b.M{"$ne": nil}},
			b.M{"$set": b.M{"state": t.StateDeleted}}); err != nil {
			return err
		}

		// Rename DeletedAt into StateAt. Update only those rows which have defined DeletedAt.
		if _, err := a.db.Collection("users").UpdateMany(a.ctx,
			b.M{"deletedat": b.M{"$exists": true}},
			b.M{"$rename": b.M{"deletedat": "stateat"}}); err != nil {
			return err
		}

		// Drop secondary index DeletedAt.
		if _, err := a.db.Collection("users").Indexes().DropOne(a.ctx, "deletedat_1"); err != nil {
			return err
		}

		// Create secondary index on State for finding suspended and soft-deleted topics.
		if _, err = a.db.Collection("users").Indexes().CreateOne(a.ctx, mdb.IndexModel{Keys: b.M{"state": 1}}); err != nil {
			return err
		}

		// Topics

		// Add StateDeleted to all topics with DeletedAt not null.
		if _, err := a.db.Collection("topics").UpdateMany(a.ctx,
			b.M{"deletedat": b.M{"$ne": nil}},
			b.M{"$set": b.M{"state": t.StateDeleted}}); err != nil {
			return err
		}

		// Set StateOK for all other topics.
		if _, err := a.db.Collection("topics").UpdateMany(a.ctx,
			b.M{"state": b.M{"$exists": false}},
			b.M{"$set": b.M{"state": t.StateOK}}); err != nil {
			return err
		}

		// Rename DeletedAt into StateAt. Update only those rows which have defined DeletedAt.
		if _, err := a.db.Collection("topics").UpdateMany(a.ctx,
			b.M{"deletedat": b.M{"$exists": true}},
			b.M{"$rename": b.M{"deletedat": "stateat"}}); err != nil {
			return err
		}

		// Create secondary index on State for finding suspended and soft-deleted topics.
		if _, err = a.db.Collection("topics").Indexes().CreateOne(a.ctx, mdb.IndexModel{Keys: b.M{"state": 1}}); err != nil {
			return err
		}

		if err := bumpVersion(a, 111); err != nil {
			return err
		}
	}

	if a.version != adpVersion {
		return errors.New("Failed to perform database upgrade to version " + strconv.Itoa(adpVersion) +
			". DB is still at " + strconv.Itoa(a.version))
	}
	return nil
}

func (a *adapter) updateDbVersion(v int) error {
	a.version = -1
	_, err := a.db.Collection("kvmeta").UpdateOne(a.ctx,
		b.M{"_id": "version"},
		b.M{"$set": b.M{"value": v}},
	)
	return err
}

// Create system topic 'sys'.
func createSystemTopic(a *adapter) error {
	now := t.TimeNow()
	_, err := a.db.Collection("topics").InsertOne(a.ctx, &t.Topic{
		ObjHeader: t.ObjHeader{
			Id:        "sys",
			CreatedAt: now,
			UpdatedAt: now},
		TouchedAt: now,
		Access:    t.DefaultAccess{Auth: t.ModeNone, Anon: t.ModeNone},
		Public:    map[string]interface{}{"fn": "System"},
	})
	return err
}

// User management

// UserCreate creates user record
func (a *adapter) UserCreate(usr *t.User) error {
	if _, err := a.db.Collection("users").InsertOne(a.ctx, &usr); err != nil {
		return err
	}

	return nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *adapter) UserGet(id t.Uid) (*t.User, error) {
	var user t.User

	filter := b.M{"_id": id.String(), "state": b.M{"$ne": t.StateDeleted}}
	if err := a.db.Collection("users").FindOne(a.ctx, filter).Decode(&user); err != nil {
		if err == mdb.ErrNoDocuments { // User not found
			return nil, nil
		} else {
			return nil, err
		}
	}
	user.Public = unmarshalBsonD(user.Public)
	return &user, nil
}

// UserGetAll returns user records for a given list of user IDs
func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = id.String()
	}

	var users []t.User
	filter := b.M{"_id": b.M{"$in": uids}, "state": b.M{"$ne": t.StateDeleted}}
	cur, err := a.db.Collection("users").Find(a.ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	for cur.Next(a.ctx) {
		var user t.User
		if err := cur.Decode(&user); err != nil {
			return nil, err
		}
		user.Public = unmarshalBsonD(user.Public)
		users = append(users, user)
	}
	return users, nil
}

func (a *adapter) maybeStartTransaction(sess mdb.Session) error {
	if a.useTransactions {
		return sess.StartTransaction()
	}
	return nil
}

func (a *adapter) maybeCommitTransaction(ctx context.Context, sess mdb.Session) error {
	if a.useTransactions {
		return sess.CommitTransaction(ctx)
	}
	return nil
}

// UserDelete deletes user record
func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	topicIds, err := a.db.Collection("topics").Distinct(a.ctx, "_id", b.M{"owner": uid.String()})
	if err != nil {
		return err
	}
	topicFilter := b.M{"topic": b.M{"$in": topicIds}}

	var sess mdb.Session
	if sess, err = a.conn.StartSession(); err != nil {
		return err
	}
	defer sess.EndSession(a.ctx)

	if err = a.maybeStartTransaction(sess); err != nil {
		return err
	}
	if err = mdb.WithSession(a.ctx, sess, func(sc mdb.SessionContext) error {
		if hard {
			// Can't delete user's messages in all topics because we cannot notify topics of such deletion.
			// Or we have to delete these messages one by one.
			// For now, just leave the messages there marked as sent by "not found" user.

			// Delete topics where the user is the owner:

			// 1. Delete dellog
			// 2. Decrement fileuploads.
			// 3. Delete all messages.
			// 4. Delete subscriptions.

			// Delete user's subscriptions in all topics.
			if err = a.subsDel(sc, b.M{"user": uid.String()}, true); err != nil {
				return err
			}

			// Delete dellog
			_, err = a.db.Collection("dellog").DeleteMany(sc, topicFilter)
			if err != nil {
				return err
			}

			// Decrement fileuploads UseCounter
			// First get array of attachments IDs that were used in messages of topics from topicIds
			// Then decrement the usecount field of these file records
			err := a.fileDecrementUseCounter(sc, b.M{"topic": b.M{"$in": topicIds}})
			if err != nil {
				return err
			}

			// Delete messages
			_, err = a.db.Collection("messages").DeleteMany(sc, topicFilter)
			if err != nil {
				return err
			}

			// Delete subscriptions
			_, err = a.db.Collection("subscriptions").DeleteMany(sc, topicFilter)
			if err != nil {
				return err
			}

			// And finally delete the topics.
			if _, err = a.db.Collection("topics").DeleteMany(sc, b.M{"owner": uid.String()}); err != nil {
				return err
			}

			// Delete user's authentication records.
			if _, err = a.authDelAllRecords(sc, uid); err != nil {
				return err
			}

			// Delete credentials.
			if err = a.credDel(sc, uid, "", ""); err != nil {
				return err
			}

			// And finally delete the user.
			if _, err = a.db.Collection("users").DeleteOne(sc, b.M{"_id": uid.String()}); err != nil {
				return err
			}
		} else {
			// Disable user's subscriptions.
			if err = a.subsDel(sc, b.M{"user": uid.String()}, false); err != nil {
				return err
			}

			// Disable subscriptions for topics where the user is the owner.
			// Disable topics where the user is the owner.
			now := t.TimeNow()
			disable := b.M{"$set": b.M{"state": t.StateDeleted, "stateat": now}}

			if _, err = a.db.Collection("subscriptions").UpdateMany(sc, topicFilter, disable); err != nil {
				return err
			}
			if _, err = a.db.Collection("topics").UpdateMany(sc, b.M{"_id": b.M{"$in": topicIds}}, disable); err != nil {
				return err
			}
			if _, err = a.db.Collection("users").UpdateMany(sc, b.M{"_id": uid.String()}, disable); err != nil {
				return err
			}
		}

		// Finally commit all changes
		return a.maybeCommitTransaction(sc, sess)
	}); err != nil {
		return err
	}

	return err
}

// topicStateForUser is called by UserUpdate when the update contains state change
func (a *adapter) topicStateForUser(uid t.Uid, now time.Time, update interface{}) error {
	state, ok := update.(t.ObjState)
	if !ok {
		return t.ErrMalformed
	}

	if now.IsZero() {
		now = t.TimeNow()
	}

	// Change state of all topics where the user is the owner.
	if _, err := a.db.Collection("topics").UpdateMany(a.ctx,
		b.M{"owner": uid.String(), "state": b.M{"$ne": t.StateDeleted}},
		b.M{"$set": b.M{"state": state, "stateat": now}}); err != nil {
		return err
	}

	// Change state of p2p topics with the user (p2p topic's owner is blank)
	topicIds, err := a.db.Collection("subscriptions").Distinct(a.ctx, "topic", b.M{"user": uid.String()})
	if err != nil {
		return err
	}

	if _, err := a.db.Collection("topics").UpdateMany(a.ctx,
		b.M{"_id": b.M{"$in": topicIds}, "owner": "", "state": b.M{"$ne": t.StateDeleted}},
		b.M{"$set": b.M{"state": state, "stateat": now}}); err != nil {
		return err
	}
	// Subscriptions don't need to be updated:
	// subscriptions of a disabled user are not disabled and still can be manipulated.
	return nil
}

// UserUpdate updates user record
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	// to get round the hardcoded "UpdatedAt" key in store.Users.Update()
	update = normalizeUpdateMap(update)

	_, err := a.db.Collection("users").UpdateOne(a.ctx, b.M{"_id": uid.String()}, b.M{"$set": update})
	if err != nil {
		return err
	}

	if state, ok := update["state"]; ok {
		now, _ := update["stateat"].(time.Time)
		err = a.topicStateForUser(uid, now, state)
	}
	return err
}

// UserUpdateTags adds, removes, or resets user's tags
func (a *adapter) UserUpdateTags(uid t.Uid, add, remove, reset []string) ([]string, error) {
	// Compare to nil vs checking for zero length: zero length reset is valid.
	if reset != nil {
		// Replace Tags with the new value
		return reset, a.UserUpdate(uid, map[string]interface{}{"tags": reset})
	}

	var user t.User
	err := a.db.Collection("users").FindOne(a.ctx, b.M{"_id": uid.String()}).Decode(&user)
	if err != nil {
		return nil, err
	}

	// Mutate the tag list.
	newTags := user.Tags
	if len(add) > 0 {
		newTags = union(newTags, add)
	}
	if len(remove) > 0 {
		newTags = diff(newTags, remove)
	}

	update := map[string]interface{}{"tags": newTags}
	if err := a.UserUpdate(uid, update); err != nil {
		return nil, err
	}

	// Get the new tags
	var tags map[string][]string
	findOpts := mdbopts.FindOne().SetProjection(b.M{"tags": 1, "_id": 0})
	err = a.db.Collection("users").FindOne(a.ctx, b.M{"_id": uid.String()}, findOpts).Decode(&tags)
	if err != nil {
		return nil, err
	}

	return tags["tags"], nil
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	var userId map[string]string
	err := a.db.Collection("credentials").FindOne(a.ctx,
		b.M{"_id": method + ":" + value},
		mdbopts.FindOne().SetProjection(b.M{"user": 1, "_id": 0}),
	).Decode(&userId)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return t.ZeroUid, nil
		}
		return t.ZeroUid, err
	}

	return t.ParseUid(userId["user"]), nil
}

// UserUnreadCount returns the total number of unread messages in all topics with
// the R permission.
func (a *adapter) UserUnreadCount(uid t.Uid) (int, error) {
	pipeline := b.A{
		b.M{"$match": b.M{"user": uid.String()}},
		// Join documents from two collection
		b.M{"$lookup": b.M{
			"from":         "topics",
			"localField":   "topic",
			"foreignField": "_id",
			"as":           "fromTopics"},
		},
		// Merge two documents into one
		b.M{"$replaceRoot": b.M{"newRoot": b.M{"$mergeObjects": b.A{b.M{"$arrayElemAt": b.A{"$fromTopics", 0}}, "$$ROOT"}}}},

		b.M{"$match": b.M{
			"deletedat": b.M{"$exists": false},
			"state":     b.M{"$ne": t.StateDeleted},
			// Filter by access mode
			"modewant":  b.M{"$bitsAllSet": b.A{1}},
			"modegiven": b.M{"$bitsAllSet": b.A{1}}}},

		b.M{"$group": b.M{"_id": nil, "unreadCount": b.M{"$sum": b.M{"$subtract": b.A{"$seqid", "$readseqid"}}}}},
	}
	cur, err := a.db.Collection("subscriptions").Aggregate(a.ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(a.ctx)

	var result []struct {
		Id          interface{} `bson:"_id"`
		UnreadCount int         `bson:"unreadCount"`
	}
	if err = cur.All(a.ctx, &result); err != nil {
		return 0, err
	}
	if len(result) == 0 { // Not found
		return 0, nil
	}
	return result[0].UnreadCount, nil
}

// Credential management

// CredUpsert adds or updates a validation record. Returns true if inserted, false if updated.
// 1. if credential is validated:
// 1.1 Hard-delete unconfirmed equivalent record, if exists.
// 1.2 Insert new. Report error if duplicate.
// 2. if credential is not validated:
// 2.1 Check if validated equivalent exist. If so, report an error.
// 2.2 Soft-delete all unvalidated records of the same method.
// 2.3 Undelete existing credential. Return if successful.
// 2.4 Insert new credential record.
func (a *adapter) CredUpsert(cred *t.Credential) (bool, error) {
	credCollection := a.db.Collection("credentials")

	cred.Id = cred.Method + ":" + cred.Value

	if !cred.Done {
		// Check if the same credential is already validated.
		var result1 t.Credential
		err := credCollection.FindOne(a.ctx, b.M{"_id": cred.Id}).Decode(&result1)
		if result1 != (t.Credential{}) {
			// Someone has already validated this credential.
			return false, t.ErrDuplicate
		} else if err != nil && err != mdb.ErrNoDocuments { // if no result -> continue
			return false, err
		}

		// Soft-delete all unvalidated records of this user and method.
		_, err = credCollection.UpdateMany(a.ctx,
			b.M{"user": cred.User, "method": cred.Method, "done": false},
			b.M{"$set": b.M{"deletedat": t.TimeNow()}})
		if err != nil {
			return false, err
		}

		// If credential is not confirmed, it should not block others
		// from attempting to validate it: make index user-unique instead of global-unique.
		cred.Id = cred.User + ":" + cred.Id

		// Check if this credential has already been added by the user.
		var result2 t.Credential
		err = credCollection.FindOne(a.ctx, b.M{"_id": cred.Id}).Decode(&result2)
		if result2 != (t.Credential{}) {
			_, err = credCollection.UpdateOne(a.ctx,
				b.M{"_id": cred.Id},
				b.M{
					"$unset": b.M{"deletedat": ""},
					"$set":   b.M{"updatedat": cred.UpdatedAt, "resp": cred.Resp}})
			if err != nil {
				return false, err
			}

			// The record was updated, all is fine.
			return false, nil
		} else if err != nil && err != mdb.ErrNoDocuments {
			return false, err
		}
	} else {
		// Hard-delete potentially present unvalidated credential.
		_, err := credCollection.DeleteOne(a.ctx, b.M{"_id": cred.User + ":" + cred.Id})
		if err != nil {
			return false, err
		}
	}

	// Insert a new record.
	_, err := credCollection.InsertOne(a.ctx, cred)
	if isDuplicateErr(err) {
		return true, t.ErrDuplicate
	}

	return true, err
}

// CredGetActive returns the currently active credential record for the given method.
func (a *adapter) CredGetActive(uid t.Uid, method string) (*t.Credential, error) {
	var cred t.Credential

	filter := b.M{
		"user":      uid.String(),
		"deletedat": b.M{"$exists": false},
		"method":    method,
		"done":      false}

	if err := a.db.Collection("credentials").FindOne(a.ctx, filter).Decode(&cred); err != nil {
		if err == mdb.ErrNoDocuments { // Cred not found
			return nil, t.ErrNotFound
		} else {
			return nil, err
		}
	}

	return &cred, nil
}

// CredGetAll returns credential records for the given user and method, validated only or all.
func (a *adapter) CredGetAll(uid t.Uid, method string, validatedOnly bool) ([]t.Credential, error) {
	filter := b.M{"user": uid.String()}
	if method != "" {
		filter["method"] = method
	}
	if validatedOnly {
		filter["done"] = true
	} else {
		filter["deletedat"] = b.M{"$exists": false}
	}

	cur, err := a.db.Collection("credentials").Find(a.ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var credentials []t.Credential
	if err := cur.All(a.ctx, &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// CredDel deletes credentials for the given method/value. If method is empty, deletes all
// user's credentials.
func (a *adapter) credDel(ctx context.Context, uid t.Uid, method, value string) error {
	credCollection := a.db.Collection("credentials")
	filter := b.M{"user": uid.String()}
	if method != "" {
		filter["method"] = method
		if value != "" {
			filter["value"] = value
		}
	} else {
		_, err := credCollection.DeleteMany(ctx, filter)
		return err
	}

	// Hard-delete all confirmed values or values with no attempts at confirmation.
	hardDeleteFilter := copyBsonMap(filter)
	hardDeleteFilter["$or"] = b.A{
		b.M{"done": true},
		b.M{"retries": 0}}
	if _, err := credCollection.DeleteMany(ctx, hardDeleteFilter); err != nil {
		return err
	}

	// Soft-delete all other values.
	_, err := credCollection.UpdateMany(ctx, filter, b.M{"$set": b.M{"deletedat": t.TimeNow()}})
	return err
}

func (a *adapter) CredDel(uid t.Uid, method, value string) error {
	return a.credDel(a.ctx, uid, method, value)
}

// CredConfirm marks given credential as validated.
func (a *adapter) CredConfirm(uid t.Uid, method string) error {
	cred, err := a.CredGetActive(uid, method)
	if err != nil {
		return err
	}

	cred.Done = true
	cred.UpdatedAt = t.TimeNow()
	if _, err = a.CredUpsert(cred); err != nil {
		return err
	}

	_, _ = a.db.Collection("credentials").DeleteOne(a.ctx, b.M{"_id": uid.String() + ":" + cred.Method + ":" + cred.Value})
	return nil
}

// CredFail increments count of failed validation attepmts for the given credentials.
func (a *adapter) CredFail(uid t.Uid, method string) error {
	filter := b.M{
		"user":      uid.String(),
		"deletedat": b.M{"$exists": false},
		"method":    method,
		"done":      false}

	update := b.M{
		"$inc": b.M{"retries": 1},
		"$set": b.M{"updatedat": t.TimeNow()}}
	_, err := a.db.Collection("credentials").UpdateOne(a.ctx, filter, update)
	return err
}

// Authentication management for the basic authentication scheme

// AuthGetUniqueRecord returns authentication record for a given unique value i.e. login.
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	var record struct {
		UserId  string
		AuthLvl auth.Level
		Secret  []byte
		Expires time.Time
	}

	filter := b.M{"_id": unique}
	findOpts := mdbopts.FindOne().SetProjection(b.M{
		"userid":  1,
		"authlvl": 1,
		"secret":  1,
		"expires": 1,
	})
	err := a.db.Collection("auth").FindOne(a.ctx, filter, findOpts).Decode(&record)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return t.ZeroUid, 0, nil, time.Time{}, nil
		}
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	return t.ParseUid(record.UserId), record.AuthLvl, record.Secret, record.Expires, nil
}

// AuthGetRecord returns authentication record given user ID and method.
func (a *adapter) AuthGetRecord(uid t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	var record struct {
		Id      string `bson:"_id"`
		AuthLvl auth.Level
		Secret  []byte
		Expires time.Time
	}

	filter := b.M{"userid": uid.String(), "scheme": scheme}
	findOpts := mdbopts.FindOne().SetProjection(b.M{
		"authlvl": 1,
		"secret":  1,
		"expires": 1,
	})
	err := a.db.Collection("auth").FindOne(a.ctx, filter, findOpts).Decode(&record)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return "", 0, nil, time.Time{}, t.ErrNotFound
		}
		return "", 0, nil, time.Time{}, err
	}

	return record.Id, record.AuthLvl, record.Secret, record.Expires, nil
}

// AuthAddRecord creates new authentication record
func (a *adapter) AuthAddRecord(uid t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) error {
	authRecord := b.M{
		"_id":     unique,
		"userid":  uid.String(),
		"scheme":  scheme,
		"authlvl": authLvl,
		"secret":  secret,
		"expires": expires}
	if _, err := a.db.Collection("auth").InsertOne(a.ctx, authRecord); err != nil {
		if isDuplicateErr(err) {
			return t.ErrDuplicate
		}
		return err
	}
	return nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(uid t.Uid, scheme string) error {
	_, err := a.db.Collection("auth").DeleteOne(a.ctx,
		b.M{
			"userid": uid.String(),
			"scheme": scheme})
	return err
}

func (a *adapter) authDelAllRecords(ctx context.Context, uid t.Uid) (int, error) {
	res, err := a.db.Collection("auth").DeleteMany(ctx, b.M{"userid": uid.String()})
	return int(res.DeletedCount), err
}

// AuthDelAllRecords deletes all records of a given user.
func (a *adapter) AuthDelAllRecords(uid t.Uid) (int, error) {
	return a.authDelAllRecords(a.ctx, uid)
}

// AuthUpdRecord modifies an authentication record.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string,
	authLvl auth.Level, secret []byte, expires time.Time) error {
	// The primary key is immutable. If '_id' has changed, we have to replace the old record with a new one:
	// 1. Check if '_id' has changed.
	// 2. If not, execute update by '_id'
	// 3. If yes, first insert the new record (it may fail due to dublicate '_id') then delete the old one.

	var err error
	var record struct {
		Unique string `bson:"_id"`
	}
	findOpts := mdbopts.FindOne().SetProjection(b.M{"_id": 1})
	filter := b.M{"userid": uid.String(), "scheme": scheme}
	if err = a.db.Collection("auth").FindOne(a.ctx, filter, findOpts).Decode(&record); err != nil {
		return err
	}

	if record.Unique == unique {
		_, err = a.db.Collection("auth").UpdateOne(a.ctx,
			b.M{"_id": unique},
			b.M{"$set": b.M{
				"authlvl": authLvl,
				"secret":  secret,
				"expires": expires}})
	} else {
		if err = a.AuthAddRecord(uid, scheme, unique, authLvl, secret, expires); err != nil {
			return err
		}
		if err = a.AuthDelScheme(uid, scheme); err != nil {
			return err
		}
	}

	return err
}

// Topic management

func (a *adapter) undeleteSubscription(sub *t.Subscription) error {
	_, err := a.db.Collection("subscriptions").UpdateOne(a.ctx,
		b.M{"_id": sub.Id},
		b.M{
			"$unset": b.M{"deletedat": ""},
			"$set": b.M{
				"updatedat": sub.UpdatedAt,
				"createdat": sub.CreatedAt,
				"modegiven": sub.ModeGiven}})
	return err
}

// TopicCreate creates a topic
func (a *adapter) TopicCreate(topic *t.Topic) error {
	_, err := a.db.Collection("topics").InsertOne(a.ctx, &topic)
	return err
}

// TopicCreateP2P creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	initiator.Id = initiator.Topic + ":" + initiator.User
	// Don't care if the initiator changes own subscription
	replOpts := mdbopts.Replace().SetUpsert(true)
	_, err := a.db.Collection("subscriptions").ReplaceOne(a.ctx, b.M{"_id": initiator.Id}, initiator, replOpts)
	if err != nil {
		return err
	}

	// If the second subscription exists, don't overwrite it. Just make sure it's not deleted.
	invited.Id = invited.Topic + ":" + invited.User
	_, err = a.db.Collection("subscriptions").InsertOne(a.ctx, invited)
	if err != nil {
		// Is this a duplicate subscription?
		if !isDuplicateErr(err) {
			// It's a genuine DB error
			return err
		}
		// Undelete the second subsription if it exists: remove DeletedAt, update CreatedAt and UpdatedAt,
		// update ModeGiven.
		err = a.undeleteSubscription(invited)
		if err != nil {
			return err
		}
	}

	topic := &t.Topic{
		ObjHeader: t.ObjHeader{Id: initiator.Topic},
		TouchedAt: initiator.GetTouchedAt()}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	return a.TopicCreate(topic)
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	var tpc = new(t.Topic)
	err := a.db.Collection("topics").FindOne(a.ctx, b.M{"_id": topic}).Decode(tpc)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return tpc, nil
}

// TopicsForUser loads subscriptions for a given user. Reads public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	filter := b.M{"user": uid.String()}
	if !keepDeleted {
		// Filter out rows with defined deletedat
		filter["deletedat"] = b.M{"$exists": false}
	}
	limit := a.maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			filter["topic"] = opts.Topic
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	findOpts := mdbopts.Find().SetLimit(int64(limit))
	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	var sub t.Subscription
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]string, 0, 16)
	usrq := make([]string, 0, 16)
	for cur.Next(a.ctx) {
		if err = cur.Decode(&sub); err != nil {
			return nil, err
		}
		tcat := t.GetTopicCat(sub.Topic)

		// skip 'me' or 'fnd' subscription
		if tcat == t.TopicCatMe || tcat == t.TopicCatFnd {
			continue

			// p2p subscription, find the other user to get user.Public
		} else if tcat == t.TopicCatP2P {
			uid1, uid2, _ := t.ParseP2P(sub.Topic)
			if uid1 == uid {
				usrq = append(usrq, uid2.String())
			} else {
				usrq = append(usrq, uid1.String())
			}
			topq = append(topq, sub.Topic)

			// grp subscription
		} else {
			topq = append(topq, sub.Topic)
		}
		join[sub.Topic] = sub
	}
	cur.Close(a.ctx)

	var subs []t.Subscription
	if len(topq) > 0 || len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(join))
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		cur, err = a.db.Collection("topics").Find(a.ctx, b.M{"_id": b.M{"$in": topq}})
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for cur.Next(a.ctx) {
			if err = cur.Decode(&top); err != nil {
				return nil, err
			}
			sub = join[top.Id]
			sub.ObjHeader.MergeTimes(&top.ObjHeader)
			sub.SetSeqId(top.SeqId)
			sub.SetTouchedAt(top.TouchedAt)
			sub.Private = unmarshalBsonD(sub.Private)
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				// all done with a grp topic
				sub.SetPublic(unmarshalBsonD(top.Public))
				subs = append(subs, sub)
			} else {
				// put back the updated value of a p2p subsription, will process further below
				join[top.Id] = sub
			}
		}
		cur.Close(a.ctx)
	}

	// Fetch p2p users and join to p2p tables
	if len(usrq) > 0 {
		filter := b.M{"_id": b.M{"$in": usrq}}
		if !keepDeleted {
			filter["state"] = b.M{"$ne": t.StateDeleted}
		}
		cur, err = a.db.Collection("users").Find(a.ctx, filter)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for cur.Next(a.ctx) {
			if err = cur.Decode(&usr); err != nil {
				return nil, err
			}

			uid2 := t.ParseUid(usr.Id)
			if sub, ok := join[uid.P2PName(uid2)]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetPublic(unmarshalBsonD(usr.Public))
				sub.SetWith(uid2.UserId())
				sub.SetDefaultAccess(usr.Access.Auth, usr.Access.Anon)
				sub.SetLastSeenAndUA(usr.LastSeen, usr.UserAgent)
				subs = append(subs, sub)
			}
		}
		cur.Close(a.ctx)
	}

	return subs, nil
}

// UsersForTopic loads users' subscriptions for a given topic. Public is loaded.
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	tcat := t.GetTopicCat(topic)

	// Fetch topic subscribers
	// Fetch all subscribed users. The number of users is not large
	filter := b.M{"topic": topic}
	if !keepDeleted && tcat != t.TopicCatP2P {
		// Filter out rows with DeletedAt being not null.
		// P2P topics must load all subscriptions otherwise it will be impossible
		// to swap Public values.
		filter["deletedat"] = b.M{"$exists": false}
	}

	limit := a.maxResults
	var oneUser t.Uid
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			if tcat != t.TopicCatP2P {
				filter["user"] = opts.User.String()
			}
			oneUser = opts.User
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, mdbopts.Find().SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	join := make(map[string]t.Subscription)
	usrq := make([]interface{}, 0, 16)
	for cur.Next(a.ctx) {
		if err = cur.Decode(&sub); err != nil {
			return nil, err
		}
		join[sub.User] = sub
		usrq = append(usrq, sub.User)
	}
	cur.Close(a.ctx)

	if len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(usrq))

		// Fetch users by a list of subscriptions
		cur, err = a.db.Collection("users").Find(a.ctx, b.M{
			"_id":   b.M{"$in": usrq},
			"state": b.M{"$ne": t.StateDeleted}})
		if err != nil {
			return nil, err
		}

		var usr t.User
		for cur.Next(a.ctx) {
			if err = cur.Decode(&usr); err != nil {
				return nil, err
			}
			if sub, ok := join[usr.Id]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.Private = unmarshalBsonD(sub.Private)
				sub.SetPublic(unmarshalBsonD(usr.Public))
				subs = append(subs, sub)
			}
		}
		cur.Close(a.ctx)
	}

	if t.GetTopicCat(topic) == t.TopicCatP2P && len(subs) > 0 {
		// Swap public values of P2P topics as expected.
		if len(subs) == 1 {
			// User is deleted. Nothing we can do.
			subs[0].SetPublic(nil)
		} else {
			pub := subs[0].GetPublic()
			subs[0].SetPublic(subs[1].GetPublic())
			subs[1].SetPublic(pub)
		}

		// Remove deleted and unneeded subscriptions
		if !keepDeleted || !oneUser.IsZero() {
			var xsubs []t.Subscription
			for i := range subs {
				if (subs[i].DeletedAt != nil && !keepDeleted) || (!oneUser.IsZero() && subs[i].Uid() != oneUser) {
					continue
				}
				xsubs = append(xsubs, subs[i])
			}
			subs = xsubs
		}
	}

	return subs, nil
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid) ([]string, error) {
	filter := b.M{"owner": uid.String(), "state": b.M{"$ne": t.StateDeleted}}
	findOpts := mdbopts.Find().SetProjection(b.M{"_id": 1})
	cur, err := a.db.Collection("topics").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}

	var res map[string]string
	var names []string
	for cur.Next(a.ctx) {
		if err := cur.Decode(&res); err == nil {
			names = append(names, res["_id"])
		} else {
			return nil, err
		}
	}
	cur.Close(a.ctx)

	return names, nil
}

// TopicShare creates topic subscriptions
func (a *adapter) TopicShare(subs []*t.Subscription) error {
	// Assign Ids.
	for i := 0; i < len(subs); i++ {
		subs[i].Id = subs[i].Topic + ":" + subs[i].User
	}

	// Subscription could have been marked as deleted (DeletedAt != nil). If it's marked
	// as deleted, unmark by clearing the DeletedAt field of the old subscription and
	// updating times and ModeGiven.
	for _, sub := range subs {
		_, err := a.db.Collection("subscriptions").InsertOne(a.ctx, sub)
		if err != nil {
			if isDuplicateErr(err) {
				if err = a.undeleteSubscription(sub); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}

	return nil
}

// TopicDelete deletes topic, subscription, messages
func (a *adapter) TopicDelete(topic string, hard bool) error {
	var err error
	if err = a.SubsDelForTopic(topic, hard); err != nil {
		return err
	}

	if hard {
		if err = a.MessageDeleteList(topic, nil); err != nil {
			return err
		}
	}

	filter := b.M{"_id": topic}
	if hard {
		_, err = a.db.Collection("topics").DeleteOne(a.ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("topics").UpdateOne(a.ctx, filter, b.M{"$set": b.M{
			"state":   t.StateDeleted,
			"stateat": now,
		}})
	}
	return err
}

// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	return a.topicUpdate(topic, map[string]interface{}{"seqid": msg.SeqId, "touchedat": msg.CreatedAt})
}

// TopicUpdate updates topic record.
func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	return a.topicUpdate(topic, normalizeUpdateMap(update))
}

// TopicOwnerChange updates topic's owner
func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	return a.topicUpdate(topic, map[string]interface{}{"owner": newOwner.String()})
}

func (a *adapter) topicUpdate(topic string, update map[string]interface{}) error {
	_, err := a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$set": update})

	return err
}

// Topic subscriptions

// SubscriptionGet reads a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {
	sub := new(t.Subscription)
	err := a.db.Collection("subscriptions").FindOne(a.ctx, b.M{
		"_id":       topic + ":" + user.String(),
		"deletedat": b.M{"$exists": false}}).Decode(sub)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}

	return sub, nil
}

// SubsForUser gets a list of topics of interest for a given user. Does NOT load Public value.
func (a *adapter) SubsForUser(user t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	filter := b.M{"user": user.String()}
	if !keepDeleted {
		filter["deletedat"] = b.M{"$exists": false}
	}
	limit := a.maxResults

	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			filter["topic"] = opts.Topic
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	findOpts := new(mdbopts.FindOptions).SetLimit(int64(limit))

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var subs []t.Subscription
	var ss t.Subscription
	for cur.Next(a.ctx) {
		if err := cur.Decode(&ss); err != nil {
			return nil, err
		}
		ss.Private = unmarshalBsonD(ss.Private)
		subs = append(subs, ss)
	}

	return subs, cur.Err()
}

// SubsForTopic gets a list of subscriptions to a given topic.. Does NOT load Public value.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	filter := b.M{"topic": topic}
	if !keepDeleted {
		filter["deletedat"] = b.M{"$exists": false}
	}

	limit := a.maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			filter["user"] = opts.User.String()
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	findOpts := new(mdbopts.FindOptions).SetLimit(int64(limit))

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var subs []t.Subscription
	var ss t.Subscription
	for cur.Next(a.ctx) {
		if err := cur.Decode(&ss); err != nil {
			return nil, err
		}
		ss.Private = unmarshalBsonD(ss.Private)
		subs = append(subs, ss)
	}

	return subs, cur.Err()
}

// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	// to get round the hardcoded pass of "Private" key
	update = normalizeUpdateMap(update)

	filter := b.M{}
	if !user.IsZero() {
		// Update one topic subscription
		filter["_id"] = topic + ":" + user.String()
	} else {
		// Update all topic subscriptions
		filter["topic"] = topic
	}
	_, err := a.db.Collection("subscriptions").UpdateOne(a.ctx, filter, b.M{"$set": update})
	return err
}

// SubsDelete deletes a single subscription
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	_, err := a.db.Collection("subscriptions").UpdateOne(a.ctx,
		b.M{"_id": topic + ":" + user.String()},
		b.M{"$set": b.M{"updatedat": now, "deletedat": now}})
	return err
}

func (a *adapter) subsDel(ctx context.Context, filter b.M, hard bool) error {
	var err error
	if hard {
		_, err = a.db.Collection("subscriptions").DeleteMany(ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("subscriptions").UpdateMany(ctx, filter,
			b.M{"$set": b.M{"updatedat": now, "deletedat": now}})
	}
	return err
}

// SubsDelForTopic deletes all subscriptions to the given topic
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	filter := b.M{"topic": topic}
	return a.subsDel(a.ctx, filter, hard)
}

// SubsDelForUser deletes all subscriptions of the given user
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	filter := b.M{"user": user.String()}
	return a.subsDel(a.ctx, filter, hard)
}

// Search
func (a *adapter) getFindPipeline(req, opt []string) (map[string]struct{}, b.A) {
	index := make(map[string]struct{})
	var allTags []interface{}
	for _, tag := range append(req, opt...) {
		allTags = append(allTags, tag)
		index[tag] = struct{}{}
	}

	pipeline := b.A{
		b.M{"$match": b.M{
			"tags":  b.M{"$in": allTags},
			"state": b.M{"$ne": t.StateDeleted},
		}},

		b.M{"$project": b.M{"_id": 1, "access": 1, "createdat": 1, "updatedat": 1, "public": 1, "tags": 1}},

		b.M{"$unwind": "$tags"},

		b.M{"$match": b.M{"tags": b.M{"$in": allTags}}},

		b.M{"$group": b.M{
			"_id":              "$_id",
			"access":           b.M{"$first": "$access"},
			"createdat":        b.M{"$first": "$createdat"},
			"updatedat":        b.M{"$first": "$updatedat"},
			"public":           b.M{"$first": "$public"},
			"tags":             b.M{"$addToSet": "$tags"},
			"matchedTagsCount": b.M{"$sum": 1},
		}},

		b.M{"$sort": b.M{"matchedTagsCount": -1}},
	}

	if len(req) > 0 {
		var reqTags []interface{}
		for _, tag := range req {
			reqTags = append(reqTags, tag)
		}

		// Filter out documents where 'tags' intersection with 'reqTags' is an empty array
		pipeline = append(pipeline,
			b.M{"$match": b.M{"$expr": b.M{"$ne": b.A{b.M{"$size": b.M{"$setIntersection": b.A{"$tags", reqTags}}}, 0}}}})
	}

	return index, append(pipeline, b.M{"$limit": a.maxResults})
}

// FindUsers searches for new contacts given a list of tags
func (a *adapter) FindUsers(uid t.Uid, req, opt []string) ([]t.Subscription, error) {
	index, pipeline := a.getFindPipeline(req, opt)
	cur, err := a.db.Collection("users").Aggregate(a.ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var user t.User
	var sub t.Subscription
	var subs []t.Subscription
	for cur.Next(a.ctx) {
		if err = cur.Decode(&user); err != nil {
			return nil, err
		}
		if user.Id == uid.String() {
			// Skip the caller
			continue
		}
		sub.CreatedAt = user.CreatedAt
		sub.UpdatedAt = user.UpdatedAt
		sub.User = user.Id
		sub.SetPublic(unmarshalBsonD(user.Public))
		sub.SetDefaultAccess(user.Access.Auth, user.Access.Anon)
		tags := make([]string, 0, 1)
		for _, tag := range user.Tags {
			if _, ok := index[tag]; ok {
				tags = append(tags, tag)
			}
		}
		sub.Private = tags
		subs = append(subs, sub)
	}

	return subs, nil
}

// FindTopics searches for group topics given a list of tags
func (a *adapter) FindTopics(req, opt []string) ([]t.Subscription, error) {
	index, pipeline := a.getFindPipeline(req, opt)
	cur, err := a.db.Collection("topics").Aggregate(a.ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var topic t.Topic
	var sub t.Subscription
	var subs []t.Subscription
	for cur.Next(a.ctx) {
		if err = cur.Decode(&topic); err != nil {
			return nil, err
		}

		sub.CreatedAt = topic.CreatedAt
		sub.UpdatedAt = topic.UpdatedAt
		sub.User = topic.Id
		sub.SetPublic(unmarshalBsonD(topic.Public))
		sub.SetDefaultAccess(topic.Access.Auth, topic.Access.Anon)
		tags := make([]string, 0, 1)
		for _, tag := range topic.Tags {
			if _, ok := index[tag]; ok {
				tags = append(tags, tag)
			}
		}
		sub.Private = tags
		subs = append(subs, sub)
	}

	return subs, nil
}

// Messages

// MessageSave saves message to database
func (a *adapter) MessageSave(msg *t.Message) error {
	_, err := a.db.Collection("messages").InsertOne(a.ctx, msg)
	return err
}

// MessageGetAll returns messages matching the query
func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {
	var limit = a.maxResults
	var lower, upper int
	requester := forUser.String()
	if opts != nil {
		if opts.Since > 0 {
			lower = opts.Since
		}
		if opts.Before > 0 {
			upper = opts.Before
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	filter := b.M{
		"topic":           topic,
		"delid":           b.M{"$exists": false},
		"deletedfor.user": b.M{"$ne": requester},
	}
	if upper == 0 {
		filter["seqid"] = b.M{"$gte": lower}
	} else {
		filter["seqid"] = b.M{"$gte": lower, "$lt": upper}
	}
	findOpts := mdbopts.Find().SetSort(b.M{"topic": -1, "seqid": -1})
	findOpts.SetLimit(int64(limit))

	cur, err := a.db.Collection("messages").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var msgs []t.Message
	for cur.Next(a.ctx) {
		var msg t.Message
		if err = cur.Decode(&msg); err != nil {
			return nil, err
		}
		msg.Content = unmarshalBsonD(msg.Content)
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (a *adapter) messagesHardDelete(topic string) error {
	var err error

	// TODO: handle file uploads
	filter := b.M{"topic": topic}
	if _, err = a.db.Collection("dellog").DeleteMany(a.ctx, filter); err != nil {
		return err
	}

	if _, err = a.db.Collection("messages").DeleteMany(a.ctx, filter); err != nil {
		return err
	}

	if err = a.fileDecrementUseCounter(a.ctx, filter); err != nil {
		return err
	}

	return err
}

// MessageDeleteList marks messages as deleted.
// Soft- or Hard- is defined by forUser value: forUSer.IsZero == true is hard.
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	var err error

	if toDel == nil {
		return a.messagesHardDelete(topic)
	}

	// Only some messages are being deleted

	// Start with making a log entry
	_, err = a.db.Collection("dellog").InsertOne(a.ctx, toDel)
	if err != nil {
		return err
	}

	filter := b.M{
		"topic": topic,
		// Skip already hard-deleted messages.
		"delid": b.M{"$exists": false},
	}
	if len(toDel.SeqIdRanges) > 1 || toDel.SeqIdRanges[0].Hi <= toDel.SeqIdRanges[0].Low {
		rangeFilter := b.A{}
		for _, rng := range toDel.SeqIdRanges {
			if rng.Hi == 0 {
				rangeFilter = append(rangeFilter, b.M{"seqid": b.M{"$gte": rng.Low}})
			} else {
				rangeFilter = append(rangeFilter, b.M{"seqid": b.M{"$gte": rng.Low, "$lte": rng.Hi}})
			}
		}
		filter["$or"] = rangeFilter
	} else {
		filter["seqid"] = b.M{"$gte": toDel.SeqIdRanges[0].Low, "$lte": toDel.SeqIdRanges[0].Hi}
	}

	if toDel.DeletedFor == "" {
		if err = a.fileDecrementUseCounter(a.ctx, filter); err != nil {
			return err
		}
		// Hard-delete individual messages. Message is not deleted but all fields with content
		// are replaced with nulls.
		_, err = a.db.Collection("messages").UpdateMany(a.ctx, filter, b.M{"$set": b.M{
			"deletedat":   t.TimeNow(),
			"delid":       toDel.DelId,
			"from":        "",
			"head":        nil,
			"content":     nil,
			"attachments": nil}})
	} else {
		// Soft-deleting: adding DelId to DeletedFor

		// Skip messages already soft-deleted for the current user
		filter["deletedfor.user"] = b.M{"$ne": toDel.DeletedFor}
		_, err = a.db.Collection("messages").UpdateMany(a.ctx, filter,
			b.M{"$addToSet": b.M{
				"deletedfor": &t.SoftDelete{
					User:  toDel.DeletedFor,
					DelId: toDel.DelId,
				}}})
	}

	// If operation has failed, remove dellog record.
	if err != nil {
		_, _ = a.db.Collection("dellog").DeleteOne(a.ctx, b.M{"_id": toDel.Id})
	}
	return err
}

// MessageGetDeleted returns a list of deleted message Ids.
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.DelMessage, error) {
	var limit = a.maxResults
	var lower, upper int
	if opts != nil {
		if opts.Since > 0 {
			lower = opts.Since
		}
		if opts.Before > 0 {
			upper = opts.Before
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	filter := b.M{
		"topic": topic,
		"$or": b.A{
			b.M{"deletedfor": forUser.String()},
			b.M{"deletedfor": ""},
		}}
	if upper == 0 {
		filter["delid"] = b.M{"$gte": lower}
	} else {
		filter["delid"] = b.M{"$gte": lower, "$lt": upper}
	}
	findOpts := mdbopts.Find().
		SetSort(b.M{"topic": 1, "delid": 1}).
		SetLimit(int64(limit))

	cur, err := a.db.Collection("dellog").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var dmsgs []t.DelMessage
	if err = cur.All(a.ctx, &dmsgs); err != nil {
		return nil, err
	}

	return dmsgs, nil
}

// MessageAttachments connects given message to a list of file record IDs.
func (a *adapter) MessageAttachments(msgId t.Uid, fids []string) error {
	now := t.TimeNow()
	_, err := a.db.Collection("messages").UpdateOne(a.ctx,
		b.M{"_id": msgId.String()},
		b.M{"$set": b.M{"updatedat": now, "attachments": fids}})
	if err != nil {
		return err
	}

	ids := make([]interface{}, len(fids))
	for i, id := range fids {
		ids[i] = id
	}
	_, err = a.db.Collection("fileuploads").UpdateMany(a.ctx,
		b.M{"_id": b.M{"$in": ids}},
		b.M{
			"$set": b.M{"updatedat": now},
			"$inc": b.M{"usecount": 1}})

	return err
}

// Devices (for push notifications)

// DeviceUpsert creates or updates a device record
func (a *adapter) DeviceUpsert(uid t.Uid, dev *t.DeviceDef) error {
	userId := uid.String()
	var user t.User
	err := a.db.Collection("users").FindOne(a.ctx, b.M{
		"_id":              userId,
		"devices.deviceid": dev.DeviceId}).Decode(&user)

	if err == nil && user.Id != "" { // current user owns this device
		// ArrayFilter used to avoid adding another (duplicate) device object. Update that device data
		updOpts := mdbopts.Update().SetArrayFilters(mdbopts.ArrayFilters{
			Filters: []interface{}{b.M{"dev.deviceid": dev.DeviceId}}})
		_, err = a.db.Collection("users").UpdateOne(a.ctx,
			b.M{"_id": userId},
			b.M{"$set": b.M{
				"devices.$[dev].platform": dev.Platform,
				"devices.$[dev].lastseen": dev.LastSeen,
				"devices.$[dev].lang":     dev.Lang}},
			updOpts)
		return err
	} else if err == mdb.ErrNoDocuments { // device is free or owned by other user
		err = a.deviceInsert(userId, dev)

		if isDuplicateErr(err) {
			// Other user owns this device.
			// We need to delete this device from that user and then insert again
			if _, err = a.db.Collection("users").UpdateOne(a.ctx,
				b.M{"devices.deviceid": dev.DeviceId},
				b.M{"$pull": b.M{"devices": b.M{"deviceid": dev.DeviceId}}}); err != nil {

				return err
			}
			return a.deviceInsert(userId, dev)
		}
		if err != nil {
			return err
		}
		return nil
	}

	return err
}

// deviceInsert adds device object to user.devices array
func (a *adapter) deviceInsert(userId string, dev *t.DeviceDef) error {
	filter := b.M{"_id": userId}
	_, err := a.db.Collection("users").UpdateOne(a.ctx, filter,
		b.M{"$push": b.M{"devices": dev}})

	if err != nil && strings.Contains(err.Error(), "must be an array") {
		// field 'devices' is not array. Make it array with 'dev' as its first element
		_, err = a.db.Collection("users").UpdateOne(a.ctx, filter,
			b.M{"$set": b.M{"devices": []interface{}{dev}}})
	}

	return err
}

// DeviceGetAll returns all devices for a given set of users
func (a *adapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	ids := make([]interface{}, len(uids))
	for i, id := range uids {
		ids[i] = id.String()
	}

	filter := b.M{"_id": b.M{"$in": ids}}
	findOpts := mdbopts.Find().SetProjection(b.M{"_id": 1, "devices": 1})
	cur, err := a.db.Collection("users").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(a.ctx)

	result := make(map[t.Uid][]t.DeviceDef)
	count := 0
	var uid t.Uid
	for cur.Next(a.ctx) {
		var row struct {
			Id      string `bson:"_id"`
			Devices []t.DeviceDef
		}
		if err = cur.Decode(&row); err != nil {
			return nil, 0, err
		}
		if row.Devices != nil && len(row.Devices) > 0 {
			if err := uid.UnmarshalText([]byte(row.Id)); err != nil {
				continue
			}

			result[uid] = row.Devices
			count++
		}
	}
	return result, count, cur.Err()
}

// DeviceDelete deletes a device record
func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	var err error
	filter := b.M{"_id": uid.String()}
	update := b.M{}
	if deviceID == "" {
		update["$set"] = b.M{"devices": []interface{}{}}
	} else {
		update["$pull"] = b.M{"devices": b.M{"deviceid": deviceID}}
	}
	_, err = a.db.Collection("users").UpdateOne(a.ctx, filter, update)
	return err
}

// File upload records. The files are stored outside of the database.

// FileStartUpload initializes a file upload
func (a *adapter) FileStartUpload(fd *t.FileDef) error {
	_, err := a.db.Collection("fileuploads").InsertOne(a.ctx, fd)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise.
func (a *adapter) FileFinishUpload(fid string, status int, size int64) (*t.FileDef, error) {
	if _, err := a.db.Collection("fileuploads").UpdateOne(a.ctx,
		b.M{"_id": fid},
		b.M{"$set": b.M{
			"updatedat": t.TimeNow(),
			"status":    status,
			"size":      size}}); err != nil {

		return nil, err
	}

	return a.FileGet(fid)
}

// FileGet fetches a record of a specific file
func (a *adapter) FileGet(fid string) (*t.FileDef, error) {
	var fd t.FileDef
	err := a.db.Collection("fileuploads").FindOne(a.ctx, b.M{"_id": fid}).Decode(&fd)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}

	return &fd, nil
}

// FileDeleteUnused deletes records where UseCount is zero. If olderThan is non-zero, deletes
// unused records with UpdatedAt before olderThan.
// Returns array of FileDef.Location of deleted filerecords so actual files can be deleted too.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	findOpts := mdbopts.Find()
	filter := b.M{"$or": b.A{
		b.M{"usecount": 0},
		b.M{"usecount": b.M{"$exists": false}}}}
	if !olderThan.IsZero() {
		filter["updatedat"] = b.M{"$lt": olderThan}
	}
	if limit > 0 {
		findOpts.SetLimit(int64(limit))
	}

	findOpts.SetProjection(b.M{"location": 1, "_id": 0})
	cur, err := a.db.Collection("fileuploads").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var result map[string]string
	var locations []string
	for cur.Next(a.ctx) {
		if err := cur.Decode(&result); err != nil {
			return nil, err
		}
		locations = append(locations, result["location"])
	}

	_, err = a.db.Collection("fileuploads").DeleteMany(a.ctx, filter)
	return locations, err
}

// Given a filter query against 'messages' collection, decrement corresponding use counter in 'fileuploads' table.
func (a *adapter) fileDecrementUseCounter(ctx context.Context, msgFilter b.M) error {
	// Copy msgFilter
	filter := b.M{}
	for k, v := range msgFilter {
		filter[k] = v
	}
	filter["attachments"] = b.M{"$exists": true}
	fileIds, err := a.db.Collection("messages").Distinct(ctx, "attachments", filter)
	if err != nil {
		return err
	}

	_, err = a.db.Collection("fileuploads").UpdateMany(ctx,
		b.M{"_id": b.M{"$in": fileIds}},
		b.M{"$inc": b.M{"usecount": -1}})

	return err
}

func (a *adapter) isDbInitialized() bool {
	var result map[string]int

	findOpts := mdbopts.FindOneOptions{Projection: b.M{"value": 1, "_id": 0}}
	if err := a.db.Collection("kvmeta").FindOne(a.ctx, b.M{"_id": "version"}, &findOpts).Decode(&result); err != nil {
		return false
	}
	return true
}

// Required for running adapter tests.
func GetAdapter() *adapter {
	return &adapter{}
}

func init() {
	store.RegisterAdapter(&adapter{})
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func union(userTags []string, addTags []string) []string {
	for _, tag := range addTags {
		if !contains(userTags, tag) {
			userTags = append(userTags, tag)
		}
	}
	return userTags
}

func diff(userTags []string, removeTags []string) []string {
	var result []string
	for _, tag := range userTags {
		if !contains(removeTags, tag) {
			result = append(result, tag)
		}
	}
	return result
}

// normalizeUpdateMap turns keys that hardcoded as CamelCase into lowercase (MongoDB uses lowercase by default)
func normalizeUpdateMap(update map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(update))
	for key, value := range update {
		result[strings.ToLower(key)] = value
	}

	return result
}

// Recursive unmarshalling of bson.D type.
// Mongo drivers unmarshalling into interface{} creates bson.D object for maps and bson.A object for slices.
// We need manually unmarshal them into correct type - bson.M (map[string]interface{}).
func unmarshalBsonD(bsonObj interface{}) interface{} {
	if obj, ok := bsonObj.(b.D); ok && len(obj) != 0 {
		result := make(b.M, 0)
		for k, v := range obj.Map() {
			result[k] = unmarshalBsonD(v)
		}
		return result
	} else if obj, ok := bsonObj.(primitive.Binary); ok {
		// primitive.Binary is a struct type with Subtype and Data fields. We need only Data ([]byte)
		return obj.Data
	} else if obj, ok := bsonObj.(b.A); ok {
		// in case of array of bson.D objects
		result := make(b.A, 0)
		for _, elem := range obj {
			result = append(result, unmarshalBsonD(elem))
		}
		return result
	}
	// Just return value as is
	return bsonObj
}

func copyBsonMap(mp b.M) b.M {
	result := b.M{}
	for k, v := range mp {
		result[k] = v
	}
	return result
}
func isDuplicateErr(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "duplicate key error")
}
