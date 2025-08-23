//go:build mongodb
// +build mongodb

// Package mongodb is a database adapter for MongoDB.
package mongodb

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/db/common"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	b "go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"
)

// adapter holds MongoDB connection data.
type adapter struct {
	conn   *mdb.Client
	db     *mdb.Database
	dbName string
	// Maximum number of records to return
	maxResults int
	// Maximum number of message records to return
	maxMessageResults int
	version           int
	ctx               context.Context
	useTransactions   bool
}

const (
	defaultHost     = "localhost:27017"
	defaultDatabase = "tinode"

	adpVersion  = 116
	adapterName = "mongodb"

	defaultMaxResults = 1024
	// This is capped by the Session's send queue limit (128).
	defaultMaxMessageResults = 100

	defaultAuthMechanism = "SCRAM-SHA-256"
	defaultAuthSource    = "admin"
)

// See https://godoc.org/go.mongodb.org/mongo-driver/mongo/options#ClientOptions for explanations.
type configType struct {
	// Connection string URI https://www.mongodb.com/docs/manual/reference/connection-string/
	Uri            string `json:"uri,omitempty"`
	Addresses      any    `json:"addresses,omitempty"`
	ConnectTimeout int    `json:"timeout,omitempty"`

	// Options separately from ClientOptions (custom options):
	Database   string `json:"database,omitempty"`
	ReplicaSet string `json:"replica_set,omitempty"`

	AuthMechanism string `json:"auth_mechanism,omitempty"`
	AuthSource    string `json:"auth_source,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`

	UseTLS             bool   `json:"tls,omitempty"`
	TlsCertFile        string `json:"tls_cert_file,omitempty"`
	TlsPrivateKey      string `json:"tls_private_key,omitempty"`
	InsecureSkipVerify bool   `json:"tls_skip_verify,omitempty"`

	// The only version supported at this time is "1".
	APIVersion mdbopts.ServerAPIVersion `json:"api_version,omitempty"`
}

// Open initializes mongodb session
func (a *adapter) Open(jsonconfig json.RawMessage) error {
	if a.conn != nil {
		return errors.New("adapter mongodb is already connected")
	}

	if len(jsonconfig) < 2 {
		return errors.New("adapter mongodb missing config")
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
	} else if ihosts, ok := config.Addresses.([]any); ok && len(ihosts) > 0 {
		hosts := make([]string, len(ihosts))
		for i, ih := range ihosts {
			h, ok := ih.(string)
			if !ok || h == "" {
				return errors.New("adapter mongodb invalid config.Addresses value")
			}
			hosts[i] = h
		}
		opts.SetHosts(hosts)
	} else {
		return errors.New("adapter mongodb failed to parse config.Addresses")
	}

	if config.Database == "" {
		a.dbName = defaultDatabase
	} else {
		a.dbName = config.Database
	}

	if config.ReplicaSet != "" {
		opts.SetReplicaSet(config.ReplicaSet)
		a.useTransactions = true
	} else {
		// Retriable writes are not supported in a standalone instance.
		opts.SetRetryWrites(false)
	}

	if config.Username != "" {
		if config.AuthMechanism == "" {
			config.AuthMechanism = defaultAuthMechanism
		}
		if config.AuthSource == "" {
			config.AuthSource = defaultAuthSource
		}
		var passwordSet bool
		if config.Password != "" {
			passwordSet = true
		}
		opts.SetAuth(
			mdbopts.Credential{
				AuthMechanism: config.AuthMechanism,
				AuthSource:    config.AuthSource,
				Username:      config.Username,
				Password:      config.Password,
				PasswordSet:   passwordSet,
			})
	}

	if config.UseTLS {
		tlsConfig := tls.Config{
			InsecureSkipVerify: config.InsecureSkipVerify,
		}

		if config.TlsCertFile != "" {
			cert, err := tls.LoadX509KeyPair(config.TlsCertFile, config.TlsPrivateKey)
			if err != nil {
				return err
			}

			tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
		}

		opts.SetTLSConfig(&tlsConfig)
	}

	if a.maxResults <= 0 {
		a.maxResults = defaultMaxResults
	}

	if a.maxMessageResults <= 0 {
		a.maxMessageResults = defaultMaxMessageResults
	}

	// Connection string URI overrides any other options configured earlier.
	if config.Uri != "" {
		opts.ApplyURI(config.Uri)
	}

	if config.APIVersion != "" {
		opts.SetServerAPIOptions(mdbopts.ServerAPI(config.APIVersion))
	}

	// Make sure the options are sane.
	if err = opts.Validate(); err != nil {
		return err
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

// DB connection stats object.
func (a *adapter) Stats() any {
	if a.db == nil {
		return nil
	}

	var result b.M
	if err := a.db.RunCommand(a.ctx, b.D{{"serverStatus", 1}}, nil).Decode(&result); err != nil {
		return nil
	}

	return result["connections"]
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
		// Index on lastSeen and updatedat for deleting stale user accounts.
		{
			Collection: "users",
			IndexOpts:  mdb.IndexModel{Keys: b.D{{"lastseen", 1}, {"updatedat", 1}}},
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
			IndexOpts:  mdb.IndexModel{Keys: b.D{{"topic", 1}, {"seqid", 1}}},
		},
		// Compound index of hard-deleted messages
		{
			Collection: "messages",
			IndexOpts:  mdb.IndexModel{Keys: b.D{{"topic", 1}, {"delid", 1}}},
		},
		// Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
		// 		 [topic, user1, delid1], [topic, user2, delid2],...
		{
			Collection: "messages",
			IndexOpts:  mdb.IndexModel{Keys: b.D{{"topic", 1}, {"deletedfor.user", 1}, {"deletedfor.delid", 1}}},
		},

		// Log of deleted messages
		// Compound index of 'topic - delid'
		{
			Collection: "dellog",
			IndexOpts:  mdb.IndexModel{Keys: b.D{{"topic", 1}, {"delid", 1}}},
		},

		// User credentials - contact information such as "email:jdoe@example.com" or "tel:+18003287448":
		// Id: "method:credential" like "email:jdoe@example.com". See types.Credential.
		// Index on 'credentials.user' to be able to query credentials by user id.
		{
			Collection: "credentials",
			Field:      "user",
		},

		// Records of file uploads. See types.FileDef.
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
	if _, err := a.db.Collection("kvmeta").InsertOne(a.ctx, map[string]any{"_id": "version", "value": adpVersion}); err != nil {
		return err
	}

	// Create system topic 'sys'.
	return createSystemTopic(a)
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

	if a.version == 111 {
		// Just bump the version to keep in line with MySQL.
		if err := bumpVersion(a, 112); err != nil {
			return err
		}
	}

	if a.version == 112 {
		// Create secondary index on Users(lastseen,updatedat) for deleting stale user accounts.
		if _, err = a.db.Collection("users").Indexes().CreateOne(a.ctx,
			mdb.IndexModel{Keys: b.D{{"lastseen", 1}, {"updatedat", 1}}}); err != nil {
			return err
		}

		if err := bumpVersion(a, 113); err != nil {
			return err
		}
	}

	if a.version < 116 {
		// Version 114: topics.aux added, fileuploads.etag added.
		// Version 115: SQL indexes added.
		// Version 116: topics.subcnt added.
		if err := bumpVersion(a, 116); err != nil {
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
		Public:    map[string]any{"fn": "System"},
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
	user.Trusted = unmarshalBsonD(user.Trusted)
	return &user, nil
}

// UserGetAll returns user records for a given list of user IDs
func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]any, len(ids))
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
		user.Trusted = unmarshalBsonD(user.Trusted)
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

// UserDelete deletes user record.
func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	forUser := uid.String()
	// Select topics where the user is the owner.
	topicIds, err := a.db.Collection("topics").Distinct(a.ctx, "_id", b.M{"owner": forUser})
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
			if len(topicIds) > 0 {
				// 1. Delete dellog
				// 2. Decrement fileuploads.
				// 3. Delete all messages.
				// 4. Delete subscriptions.

				// Delete dellog entries.
				_, err = a.db.Collection("dellog").DeleteMany(sc, topicFilter)
				if err != nil {
					return err
				}

				// Decrement fileuploads UseCounter
				// First get array of attachments IDs that were used in messages of topics from topicIds
				// Then decrement the usecount field of these file records
				err = a.decFileUseCounter(sc, "messages", b.M{"topic": b.M{"$in": topicIds}})
				if err != nil {
					return err
				}

				// Decrement use counter for topic avatars
				err = a.decFileUseCounter(sc, "topics", b.M{"_id": b.M{"$in": topicIds}})
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
				if _, err = a.db.Collection("topics").DeleteMany(sc, b.M{"owner": forUser}); err != nil {
					return err
				}
			}

			// Select all other topics where the user is a subscriber.
			topicIds, err = a.db.Collection("subscriptions").Distinct(sc, "topic", b.M{"user": forUser})
			if err != nil {
				return err
			}

			if len(topicIds) > 0 {
				// Delete user's dellog entries.
				if _, err = a.db.Collection("dellog").DeleteMany(sc,
					b.M{"topic": b.M{"$in": topicIds}, "deletedfor": forUser}); err != nil {
					return err
				}

				// Delete user's markings of soft-deleted messages
				filter := b.M{"topic": b.M{"$in": topicIds}, "deletedfor.user": forUser}
				if _, err = a.db.Collection("messages").
					UpdateMany(sc, filter, b.M{"$pull": b.M{"deletedfor": b.M{"user": forUser}}}); err != nil {
					return err
				}

				// Delete user's subscriptions in all topics.
				if err = a.subsDelete(sc, b.M{"user": forUser}, true); err != nil {
					return err
				}
			}

			// Delete user's authentication records.
			if _, err = a.authDelAllRecords(sc, uid); err != nil {
				return err
			}

			// Delete credentials.
			if err = a.credDel(sc, uid, "", ""); err != nil && err != t.ErrNotFound {
				return err
			}

			// Delete avatar (decrement use counter).
			if err = a.decFileUseCounter(sc, "users", b.M{"_id": forUser}); err != nil {
				return err
			}

			// And finally delete the user.
			if _, err = a.db.Collection("users").DeleteOne(sc, b.M{"_id": forUser}); err != nil {
				return err
			}
		} else {
			// Disable user's subscriptions.
			if err = a.subsDelete(sc, b.M{"user": forUser}, false); err != nil {
				return err
			}

			now := t.TimeNow()
			disable := b.M{"$set": b.M{"updatedat": now, "state": t.StateDeleted, "stateat": now}}

			// Disable subscriptions for topics where the user is the owner.
			if _, err = a.db.Collection("subscriptions").UpdateMany(sc, topicFilter, disable); err != nil {
				return err
			}
			// Disable topics where the user is the owner.
			if _, err = a.db.Collection("topics").UpdateMany(sc, b.M{"_id": b.M{"$in": topicIds}},
				b.M{"$set": b.M{
					"updatedat": now, "touchedat": now, "state": t.StateDeleted, "stateat": now,
				}}); err != nil {
				return err
			}

			// FIXME: disable p2p topics with the user.

			// Finally disable the user.
			if _, err = a.db.Collection("users").UpdateMany(sc, b.M{"_id": forUser}, disable); err != nil {
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
func (a *adapter) topicStateForUser(uid t.Uid, now time.Time, update any) error {
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
func (a *adapter) UserUpdate(uid t.Uid, update map[string]any) error {
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
		return reset, a.UserUpdate(uid, map[string]any{"tags": reset})
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

	update := map[string]any{"tags": newTags}
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
// the R permission. If read fails, the counts are still returned with the original
// user IDs but with the unread count undefined and non-nil error.
func (a *adapter) UserUnreadCount(ids ...t.Uid) (map[t.Uid]int, error) {
	uids := make([]string, len(ids))
	counts := make(map[t.Uid]int, len(ids))
	for i, id := range ids {
		uids[i] = id.String()
		// Ensure all original uids are always present.
		counts[id] = 0
	}
	/*
		Query:
			db.subscriptions.aggregate([
				{ $match: { user: { $in: ["KnElfSSA21U", "0ZcCQmwI2RI"] } } },
				{ $lookup: { from: "topics", localField: "topic", foreignField: "_id", as: "fromTopics"} },
				{ $replaceRoot: { newRoot: { $mergeObjects: [ {$arrayElemAt: [ "$fromTopics", 0 ]} , "$$ROOT" ] } } },
				{ $match: {
						deletedat: { $exists: false },
						state:     { $ne": t.StateDeleted },
						modewant:  { $bitsAllSet: [ t.ModeRead ] },
						modegiven: { $bitsAllSet: [ t.ModeRead ] }
					}
				},
				{ $project: { _id: 0, user: 1, readseqid: 1, seqid: 1} },
				{ $group: { _id: "$user", unreadCount: { $sum: { $subtract: [ "$seqid", "$readseqid" ] } } } }
			])

		Result:
			{ "_id" : "KnElfSSA21U", "unreadCount" : 0 }
			{ "_id" : "0ZcCQmwI2RI", "unreadCount" : 7 }
	*/

	pipeline := b.A{
		b.M{"$match": b.M{"user": b.M{"$in": uids}}},
		// Join documents from two collection
		b.M{"$lookup": b.M{
			"from":         "topics",
			"localField":   "topic",
			"foreignField": "_id",
			"as":           "fromTopics"},
		},
		// Merge two documents into one
		b.M{"$replaceRoot": b.M{"newRoot": b.M{"$mergeObjects": b.A{b.M{"$arrayElemAt": b.A{"$fromTopics", 0}}, "$$ROOT"}}}},

		// Keep only those records which affect the result.
		b.M{"$match": b.M{
			"deletedat": b.M{"$exists": false},
			"state":     b.M{"$ne": t.StateDeleted},
			// Filter by access mode
			"modewant":  b.M{"$bitsAllSet": b.A{t.ModeRead}},
			"modegiven": b.M{"$bitsAllSet": b.A{t.ModeRead}}}},

		// Remove unused fields.
		b.M{"$project": b.M{"_id": 0, "user": 1, "readseqid": 1, "seqid": 1}},
		// GROUP BY user.
		b.M{"$group": b.M{"_id": "$user", "unreadCount": b.M{"$sum": b.M{"$subtract": b.A{"$seqid", "$readseqid"}}}}},
	}
	cur, err := a.db.Collection("subscriptions").Aggregate(a.ctx, pipeline)
	if err != nil {
		return counts, err
	}
	defer cur.Close(a.ctx)

	for cur.Next(a.ctx) {
		var oneCount struct {
			Id          string `bson:"_id"`
			UnreadCount int    `bson:"unreadCount"`
		}
		cur.Decode(&oneCount)
		counts[t.ParseUid(oneCount.Id)] = oneCount.UnreadCount
	}

	return counts, nil
}

// UserGetUnvalidated returns a list of uids which have never logged in, have no
// validated credentials and haven't been updated since lastUpdatedBefore.
func (a *adapter) UserGetUnvalidated(lastUpdatedBefore time.Time, limit int) ([]t.Uid, error) {
	/*
		Query:
		[
			// .. WHERE lastseen IS NULL AND updatedat<?
			{$match: {
				$and: [
					{ lastseen: null },
					{ updatedat: {$lt: new ISODate("2022-12-09T01:26:15.819Z")} },
				],
			}},
			// JOIN credentials ON id=user
			{$lookup: {
				from: "credentials",
				localField: "_id",
				foreignField: "user",
				as: "fcred",
			}},
			// {x: 1, y: [{a: 1}, {a: 2}]} -> [{x: 1, a: 1}, {x: 1, a: 2}]
		  {$unwind: {path: "$fcred"}},
			// SELECT _id, CASE WHEN done THEN 1 ELSE 0 END
		  {$project: {
				_id: 1,
		    completed: { $cond: { if: "$fcred.done", then: 1, else: 0 } },
		  }},
			// GROUP BY _id
		  {$group: { _id: "$_id", completed: { $sum: "$completed" } } },
			// HAVING completed=0
		  {$match: { completed: 0 }},
			// SELECT _id
		  {$project: { _id: "$_id" }},
			{$limit: 10}
		]
	*/
	pipeline := b.A{
		b.M{"$match": b.M{
			"$and": b.A{
				b.M{"lastseen": primitive.Null{}},
				b.M{"updatedat": b.M{"$lt": lastUpdatedBefore}},
			},
		}},
		b.M{"$lookup": b.D{
			{"from", "credentials"},
			{"localField", "_id"},
			{"foreignField", "user"},
			{"as", "fcred"}},
		},
		b.M{"$unwind": b.M{"path": "$fcred"}},
		b.M{"$project": b.D{
			{"_id", 1},
			{"completed", b.M{
				"$cond": b.D{{"if", "$fcred.done"}, {"then", 1}, {"else", 0}}},
			}}},
		b.M{"$group": b.D{{"_id", "$_id"}, {"completed", b.M{"$sum": "$completed"}}}},
		b.M{"$match": b.M{"completed": 0}},
		b.M{"$project": b.M{"_id": "$_id"}},
		b.M{"$limit": limit},
	}

	cur, err := a.db.Collection("users").Aggregate(a.ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var uids []t.Uid
	for cur.Next(a.ctx) {
		var oneUser struct {
			Id string `bson:"_id"`
		}
		if err := cur.Decode(&oneUser); err != nil {
			return nil, err
		}
		uid := t.ParseUid(oneUser.Id)
		if uid.IsZero() {
			return nil, errors.New("failed to decode user id")
		}
		uids = append(uids, uid)
	}

	return uids, err
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
		}
		if err != nil && err != mdb.ErrNoDocuments { // if no result -> continue
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
		}
		if err != nil && err != mdb.ErrNoDocuments {
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
		}
		return nil, err
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
		res, err := credCollection.DeleteMany(ctx, filter)
		if err == nil {
			if res.DeletedCount == 0 {
				err = t.ErrNotFound
			}
		}
		return err
	}

	// Hard-delete all confirmed values or values with no attempts at confirmation.
	hardDeleteFilter := copyBsonMap(filter)
	hardDeleteFilter["$or"] = b.A{
		b.M{"done": true},
		b.M{"retries": 0}}
	if res, err := credCollection.DeleteMany(ctx, hardDeleteFilter); err != nil {
		return err
	} else if res.DeletedCount > 0 {
		return nil
	}

	// Soft-delete all other values.
	res, err := credCollection.UpdateMany(ctx, filter, b.M{"$set": b.M{"deletedat": t.TimeNow()}})
	if err == nil {
		if res.ModifiedCount == 0 {
			err = t.ErrNotFound
		}
	}
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
	if err := a.db.Collection("auth").FindOne(a.ctx, filter, findOpts).Decode(&record); err != nil {
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
			err = t.ErrNotFound
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
		if err == mdb.ErrNoDocuments {
			err = t.ErrNotFound
		}
		return err
	}

	if record.Unique == unique {
		upd := b.M{
			"authlvl": authLvl,
		}
		if len(secret) > 0 {
			upd["secret"] = secret
		}
		if !expires.IsZero() {
			upd["expires"] = expires
		}
		_, err = a.db.Collection("auth").UpdateOne(a.ctx,
			b.M{"_id": unique},
			b.M{"$set": upd})
	} else {
		err = a.AuthAddRecord(uid, scheme, unique, authLvl, secret, expires)
		if err == nil {
			a.AuthDelScheme(uid, scheme)
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
				"modegiven": sub.ModeGiven,
				"modewant":  sub.ModeWant,
				"delid":     0,
				"readseqid": 0,
				"recvseqid": 0}})
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
		TouchedAt: initiator.GetTouchedAt(),
		SubCnt:    2,
	}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	return a.TopicCreate(topic)
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	var tpc = new(t.Topic)
	if err := a.db.Collection("topics").FindOne(a.ctx, b.M{"_id": topic}).Decode(tpc); err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	// Topic found, get subsription count. Try both topic and channel names.
	if err := a.db.Collection("subscriptions").FindOne(a.ctx, b.A{
		b.M{"$match": b.M{
			"topic":     b.M{"$in": b.A{topic, t.GrpToChn(topic)}},
			"deletedat": b.M{"$exists": false},
		}},
		b.M{"$count": "subCnt"},
	}).Decode(&tpc.SubCnt); err != nil {
		return nil, err
	}

	tpc.Public = unmarshalBsonD(tpc.Public)
	tpc.Trusted = unmarshalBsonD(tpc.Trusted)
	// tpc.Aux = unmarshalBsonD(tpc.Aux)
	return tpc, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' & 'fnd' subscriptions.
// Reads and denormalizes Public & Trusted values.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	filter := b.M{"user": uid.String()}
	if !keepDeleted {
		// Filter out rows with defined deletedat
		filter["deletedat"] = b.M{"$exists": false}
	}

	limit := 0
	ims := time.Time{}
	if opts != nil {
		if opts.Topic != "" {
			filter["topic"] = opts.Topic
		}

		// Apply the limit only when the client does not manage the cache (or cold start).
		// Otherwise have to get all subscriptions and do a manual join with users/topics.
		if opts.IfModifiedSince == nil {
			if opts.Limit > 0 && opts.Limit < a.maxResults {
				limit = opts.Limit
			} else {
				limit = a.maxResults
			}
		} else {
			ims = *opts.IfModifiedSince
		}
	} else {
		limit = a.maxResults
	}

	var findOpts *mdbopts.FindOptions
	if limit > 0 {
		findOpts = mdbopts.Find().SetLimit(int64(limit))
	}

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]string, 0, 16)
	usrq := make([]string, 0, 16)
	for cur.Next(a.ctx) {
		var sub t.Subscription
		if err = cur.Decode(&sub); err != nil {
			break
		}
		tname := sub.Topic
		sub.User = uid.String()
		tcat := t.GetTopicCat(tname)

		if tcat == t.TopicCatMe || tcat == t.TopicCatFnd {
			// Skip 'me' or 'fnd' subscription. Don't skip 'sys'.
			continue
		} else if tcat == t.TopicCatP2P {
			// P2P subscription, find the other user to get user.Public
			uid1, uid2, _ := t.ParseP2P(sub.Topic)
			if uid1 == uid {
				usrq = append(usrq, uid2.String())
				sub.SetWith(uid2.UserId())
			} else {
				usrq = append(usrq, uid1.String())
				sub.SetWith(uid1.UserId())
			}
			topq = append(topq, tname)
		} else {
			// Group or sys subscription.
			if tcat == t.TopicCatGrp {
				// Maybe convert channel name to topic name.
				tname = t.ChnToGrp(tname)
			}
			topq = append(topq, tname)
		}
		sub.Private = unmarshalBsonD(sub.Private)
		join[tname] = sub
	}
	cur.Close(a.ctx)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	if len(join) == 0 {
		return subs, nil
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		filter = b.M{"_id": b.M{"$in": topq}}
		if !keepDeleted {
			filter["state"] = b.M{"$ne": t.StateDeleted}
		}
		if !ims.IsZero() {
			// Use cache timestamp if provided: get newer entries only.
			filter["touchedat"] = b.M{"$gt": ims}

			findOpts = nil
			if limit > 0 && limit < len(topq) {
				// No point in fetching more than the requested limit.
				findOpts = mdbopts.Find().SetSort(b.D{{"touchedat", 1}}).SetLimit(int64(limit))
			}
		}
		cur, err = a.db.Collection("topics").Find(a.ctx, filter, findOpts)
		if err != nil {
			return nil, err
		}

		for cur.Next(a.ctx) {
			var top t.Topic
			if err = cur.Decode(&top); err != nil {
				break
			}
			sub := join[top.Id]
			sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, top.UpdatedAt)
			sub.SetState(top.State)
			sub.SetTouchedAt(top.TouchedAt)
			sub.SetSeqId(top.SeqId)
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				sub.SetPublic(unmarshalBsonD(top.Public))
				sub.SetTrusted(unmarshalBsonD(top.Trusted))
			}
			// Put back the updated value of a p2p subsription, will process further below
			join[top.Id] = sub
		}
		cur.Close(a.ctx)
		if err != nil {
			return nil, err
		}
	}

	// Fetch p2p users and join to p2p tables
	if len(usrq) > 0 {
		filter = b.M{"_id": b.M{"$in": usrq}}
		if !keepDeleted {
			filter["state"] = b.M{"$ne": t.StateDeleted}
		}

		// Ignoring ims: we need all users to get LastSeen and UserAgent.

		cur, err = a.db.Collection("users").Find(a.ctx, filter, findOpts)
		if err != nil {
			return nil, err
		}

		for cur.Next(a.ctx) {
			var usr2 t.User
			if err = cur.Decode(&usr2); err != nil {
				break
			}

			joinOn := uid.P2PName(t.ParseUid(usr2.Id))
			if sub, ok := join[joinOn]; ok {
				sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, usr2.UpdatedAt)
				sub.SetState(usr2.State)
				sub.SetPublic(unmarshalBsonD(usr2.Public))
				sub.SetTrusted(unmarshalBsonD(usr2.Trusted))
				sub.SetDefaultAccess(usr2.Access.Auth, usr2.Access.Anon)
				sub.SetLastSeenAndUA(usr2.LastSeen, usr2.UserAgent)
				join[joinOn] = sub
			}
		}
		cur.Close(a.ctx)
		if err != nil {
			return nil, err
		}
	}

	subs = make([]t.Subscription, 0, len(join))
	for _, sub := range join {
		subs = append(subs, sub)
	}

	return common.SelectEarliestUpdatedSubs(subs, opts, a.maxResults), nil
}

// UsersForTopic loads users' subscriptions for a given topic. Public & Trusted are loaded.
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
		// Those unmodified will be stripped of Public, Trusted & Private.

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
	var subs []t.Subscription
	join := make(map[string]t.Subscription)
	usrq := make([]any, 0, 16)
	for cur.Next(a.ctx) {
		var sub t.Subscription
		if err = cur.Decode(&sub); err != nil {
			break
		}
		join[sub.User] = sub
		usrq = append(usrq, sub.User)
	}
	cur.Close(a.ctx)
	if err != nil {
		return nil, err
	}

	if len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(usrq))

		// Fetch users by a list of subscriptions
		cur, err = a.db.Collection("users").Find(a.ctx, b.M{
			"_id":   b.M{"$in": usrq},
			"state": b.M{"$ne": t.StateDeleted}})
		if err != nil {
			return nil, err
		}

		for cur.Next(a.ctx) {
			var usr2 t.User
			if err = cur.Decode(&usr2); err != nil {
				break
			}
			if sub, ok := join[usr2.Id]; ok {
				sub.ObjHeader.MergeTimes(&usr2.ObjHeader)
				sub.Private = unmarshalBsonD(sub.Private)
				sub.SetPublic(unmarshalBsonD(usr2.Public))
				sub.SetTrusted(unmarshalBsonD(usr2.Trusted))
				sub.SetLastSeenAndUA(usr2.LastSeen, usr2.UserAgent)

				subs = append(subs, sub)
			}
		}
		cur.Close(a.ctx)
		if err != nil {
			return nil, err
		}
	}

	if t.GetTopicCat(topic) == t.TopicCatP2P && len(subs) > 0 {
		// Swap public values & lastSeen of P2P topics as expected.
		if len(subs) == 1 {
			// User is deleted. Nothing we can do.
			subs[0].SetPublic(nil)
			subs[0].SetTrusted(nil)
			subs[0].SetLastSeenAndUA(nil, "")
		} else {
			tmp := subs[0].GetPublic()
			subs[0].SetPublic(subs[1].GetPublic())
			subs[1].SetPublic(tmp)

			tmp = subs[0].GetTrusted()
			subs[0].SetTrusted(subs[1].GetTrusted())
			subs[1].SetTrusted(tmp)

			lastSeen := subs[0].GetLastSeen()
			userAgent := subs[0].GetUserAgent()
			subs[0].SetLastSeenAndUA(subs[1].GetLastSeen(), subs[1].GetUserAgent())
			subs[1].SetLastSeenAndUA(lastSeen, userAgent)
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

	var names []string
	for cur.Next(a.ctx) {
		var res map[string]string
		if err = cur.Decode(&res); err != nil {
			break
		}
		names = append(names, res["_id"])
	}
	cur.Close(a.ctx)

	return names, err
}

// ChannelsForUser loads a slice of topic names where the user is a channel reader and notifications (P) are enabled.
func (a *adapter) ChannelsForUser(uid t.Uid) ([]string, error) {
	filter := b.M{
		"user":      uid.String(),
		"deletedat": b.M{"$exists": false},
		"topic":     b.M{"$regex": primitive.Regex{Pattern: "^chn"}},
		"modewant":  b.M{"$bitsAllSet": b.A{t.ModePres}},
		"modegiven": b.M{"$bitsAllSet": b.A{t.ModePres}}}
	findOpts := mdbopts.Find().SetProjection(b.M{"topic": 1})
	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}

	var names []string
	for cur.Next(a.ctx) {
		var res map[string]string
		if err = cur.Decode(&res); err != nil {
			break
		}
		names = append(names, res["topic"])
	}
	cur.Close(a.ctx)

	return names, err
}

// TopicShare creates topic subscriptions
func (a *adapter) TopicShare(shares []*t.Subscription) error {
	if len(shares) == 0 {
		return nil
	}

	// Assign Ids.
	topic := shares[0].Topic
	for _, sub := range shares {
		if sub.Topic != topic {
			return fmt.Errorf("all subscriptions must be for the same topic, got %s vs %s", sub.Topic, topic)
		}
		sub.Id = sub.Topic + ":" + sub.User
	}

	// Subscription could have been marked as deleted (DeletedAt != nil). If it's marked
	// as deleted, unmark by clearing the DeletedAt field of the old subscription and
	// updating times and ModeGiven.
	for _, sub := range shares {
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

	// Update topic's subscription count.
	// The error is ignored because the subvscriptions have been created already.
	a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$inc": map[string]any{"subcnt": len(shares)}})

	return nil
}

// TopicDelete deletes topic, subscription, messages
func (a *adapter) TopicDelete(topic string, isChan, hard bool) error {
	filter := b.M{}
	if isChan {
		// If the topic is a channel, must try to delete subscriptions under both grpXXX and chnXXX names.
		filter["$or"] = b.A{
			b.M{"topic": topic},
			b.M{"topic": t.GrpToChn(topic)},
		}
	} else {
		filter["topic"] = topic
	}
	err := a.subsDelete(a.ctx, filter, hard)
	if err != nil {
		return err
	}

	filter = b.M{"_id": topic}
	if hard {
		if err = a.MessageDeleteList(topic, nil); err != nil {
			return err
		}
		if err = a.decFileUseCounter(a.ctx, "topics", filter); err != nil {
			return err
		}
		_, err = a.db.Collection("topics").DeleteOne(a.ctx, filter)
	} else {
		_, err = a.db.Collection("topics").UpdateOne(a.ctx, filter, b.M{"$set": b.M{
			"state":   t.StateDeleted,
			"stateat": t.TimeNow(),
		}})
	}

	return err
}

// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	return a.topicUpdate(topic, map[string]any{"seqid": msg.SeqId, "touchedat": msg.CreatedAt})
}

// TopicUpdate updates topic record.
func (a *adapter) TopicUpdate(topic string, update map[string]any) error {
	if t, u := update["TouchedAt"], update["UpdatedAt"]; t == nil && u != nil {
		update["TouchedAt"] = u
	}
	return a.topicUpdate(topic, normalizeUpdateMap(update))
}

// TopicOwnerChange updates topic's owner
func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	return a.topicUpdate(topic, map[string]any{"owner": newOwner.String()})
}

func (a *adapter) topicUpdate(topic string, update map[string]any) error {
	_, err := a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$set": update})

	return err
}

// Topic subscriptions

// SubscriptionGet reads a subscription of a user to a topic.
func (a *adapter) SubscriptionGet(topic string, user t.Uid, keepDeleted bool) (*t.Subscription, error) {
	sub := new(t.Subscription)
	filter := b.M{"_id": topic + ":" + user.String()}
	if !keepDeleted {
		filter["deletedat"] = b.M{"$exists": false}
	}
	err := a.db.Collection("subscriptions").FindOne(a.ctx, filter).Decode(sub)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}

	return sub, nil
}

// SubsForUser loads all subscriptions of a given user. It does NOT load Public, Trusted or Private values,
// does not load deleted subs.
func (a *adapter) SubsForUser(user t.Uid) ([]t.Subscription, error) {
	filter := b.M{"user": user.String(), "deletedat": b.M{"$exists": false}}

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var subs []t.Subscription
	for cur.Next(a.ctx) {
		var ss t.Subscription
		if err := cur.Decode(&ss); err != nil {
			return nil, err
		}
		ss.Private = nil
		subs = append(subs, ss)
	}

	return subs, cur.Err()
}

// SubsForTopic gets a list of subscriptions to a given topic. Does NOT load Public & Trusted values.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	filter := b.M{"topic": topic}
	if !keepDeleted {
		filter["deletedat"] = b.M{"$exists": false}
	}

	limit := a.maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public, Trusted & Private.

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
	for cur.Next(a.ctx) {
		var ss t.Subscription
		if err := cur.Decode(&ss); err != nil {
			return nil, err
		}
		ss.Private = unmarshalBsonD(ss.Private)
		subs = append(subs, ss)
	}

	return subs, cur.Err()
}

// SubsUpdate updates part of a subscription object. Pass nil for fields which don't need to be updated
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]any) error {
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
	var sess mdb.Session
	var err error

	if sess, err = a.conn.StartSession(); err != nil {
		return err
	}
	defer sess.EndSession(a.ctx)

	if err = a.maybeStartTransaction(sess); err != nil {
		return err
	}

	forUser := user.String()

	return mdb.WithSession(a.ctx, sess, func(sc mdb.SessionContext) error {
		if err := a.subsDelete(sc, b.M{"_id": topic + ":" + forUser}, false); err != nil {
			return err
		}

		if !t.IsChannel(topic) {

			// Delete user's dellog entries.
			if _, err := a.db.Collection("dellog").DeleteMany(sc, b.M{"topic": topic, "deletedfor": forUser}); err != nil {
				return err
			}

			// Delete user's markings of soft-deleted messages
			filter := b.M{"topic": topic, "deletedfor.user": forUser}
			if _, err := a.db.Collection("messages").
				UpdateMany(sc, filter, b.M{"$pull": b.M{"deletedfor": b.M{"user": forUser}}}); err != nil {
				return err
			}
		}
		// Commit changes.
		return a.maybeCommitTransaction(sc, sess)
	})
}

// Delete/mark deleted subscriptions.
func (a *adapter) subsDelete(ctx context.Context, filter b.M, hard bool) error {
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

// Find searches for contacts and topics given a list of tags.
func (a *adapter) Find(caller, prefPrefix string, req [][]string, opt []string, activeOnly bool) ([]t.Subscription, error) {
	/*
		// MongoDB aggregation pipeline using unionWith.
		[
			{ $match: { tags: { $in: ["basic:alice", "travel"] } } },
			{ $unionWith: {
					coll: "topics",
					pipeline: [ { $match: { tags: { $in: ["basic:alice", "travel"] } } } ]
				}
			},
			{ $project: { _id: 1, access: 1, createdat: 1, updatedat: 1, usebt: 1, public: 1, trusted: 1, tags: 1, _source: 1 } },
			{ $addFields: { matchedCount: { $sum: { $map: {
				input: { $setIntersection: [ "$tags", [ "alias:aliassa", "basic:alice", "travel" ] ] },
				as: "tag",
				in: { $cond: { if: { $regexMatch: { input: "$$tag", regex: "^alias:"} }, then: 20, else: 1 } }
			} }}}},
			{ $match: { $expr: { $ne: [ { $size: { $setIntersection: [ "$tags", ["basic:alice", "travel"] ] } }, 0 ] } } },
			{ $sort: { matchedCount: -1 } },
			{ $limit: 20 }
		]

		// Alternative approach using $facet for (supposedly) better performance:
		[ { $facet: {
					users: [
						{ $match: { tags: { $in: [ "alias:alice", "basic:alice", "travel" ] } } },
						{ $project: { _id: 1, access: 1, createdat: 1, updatedat: 1, usebt: 1, public: 1, trusted: 1, tags: 1 } }
					],
					topics: [
						{ $lookup: {
							from: "topics",
							pipeline: [
								{ $match: { tags: { $in: [ "alias:alice", "basic:alice", "travel" ] } } },
								{ $project: { _id: 1, access: 1, createdat: 1, updatedat: 1, usebt: 1, public: 1, trusted: 1, tags: 1 } } }
							],
							as: "topicDocs"
						}},
						{ $unwind: "$topicDocs" },
						{ $replaceRoot: { newRoot: "$topicDocs" } }
					]
				}
			},
			{ $project: { combined: { $concatArrays: ["$users", "$topics"] } } },
			{ $unwind: "$combined" },
			{ $replaceRoot: { newRoot: "$combined" } },
			{ $group: { _id: "$_id", doc: { $first: "$$ROOT" } } },
			{ $replaceRoot: { newRoot: "$doc" } },
			{ $addFields: { matchedCount:
				{ $sum: { $map: { input:
					{ $setIntersection: [ "$tags", [ "alias:alice", "basic:alice", "travel" ] ] },
					as: "tag",
					in: {
					$cond: {
						if: { $regexMatch: { input: "$$tag", regex: "^alias:" } }, then: 20, else: 1 }
					}
				} }
			} } },
			{ $match: { $expr: { $ne: [
				{ $size: { $setIntersection: [ "$tags", [ "alias:alice", "basic:alice", "travel" ] ] } },
				0
			] } } },
			{ $sort: { matchedCount: -1 } },
			{ $limit: 20 }
		]
	*/

	index := make(map[string]struct{})
	allReq := t.FlattenDoubleSlice(req)
	var allTags []any
	for _, tag := range append(allReq, opt...) {
		allTags = append(allTags, tag)
		index[tag] = struct{}{}
	}

	/*
		matchOn := b.M{"tags": b.M{"$in": allTags}}
		if activeOnly {
			matchOn["state"] = b.M{"$eq": t.StateOK}
		}
		commonPipe := b.A{
			b.M{"$match": matchOn},
			b.M{"$project": b.M{"_id": 1, "createdat": 1, "updatedat": 1, "usebt": 1, "access": 1, "public": 1, "trusted": 1, "tags": 1}},
			b.M{"$unwind": "$tags"},
			b.M{"$match": b.M{"tags": b.M{"$in": allTags}}},
			b.M{"$group": b.M{
				"_id":              "$_id",
				"createdat":        b.M{"$first": "$createdat"},
				"updatedat":        b.M{"$first": "$updatedat"},
				"usebt":            b.M{"$first": "$usebt"},
				"access":           b.M{"$first": "$access"},
				"public":           b.M{"$first": "$public"},
				"trusted":          b.M{"$first": "$trusted"},
				"tags":             b.M{"$addToSet": "$tags"},
				"matchedTagsCount": b.M{"$sum": 1},
			}},
		}

		for _, reqDisjunction := range req {
			if len(reqDisjunction) == 0 {
				continue
			}
			var reqTags []any
			for _, tag := range reqDisjunction {
				reqTags = append(reqTags, tag)
			}
			// Filter out documents where 'tags' intersection with 'reqTags' is an empty array
			commonPipe = append(commonPipe,
				b.M{"$match": b.M{"$expr": b.M{"$ne": b.A{b.M{"$size": b.M{"$setIntersection": b.A{"$tags", reqTags}}}, 0}}}})
		}

		// Must create a copy of commonPipe so the original commonPipe can be used unmodified in $unionWith.
		pipeline := append(slices.Clone(commonPipe),
			b.M{"$unionWith": b.M{"coll": "topics", "pipeline": commonPipe}},
			b.M{"$sort": b.M{"matchedTagsCount": -1}},
			b.M{"$limit": a.maxResults})
	*/

	projectFields := b.M{"_id": 1, "createdat": 1, "updatedat": 1, "usebt": 1,
		"access": 1, "subcnt": 1, "public": 1, "trusted": 1, "tags": 1}

	pipeline := b.A{
		// Stage 1: $facet
		b.M{
			"$facet": b.D{
				{"users", b.A{
					b.M{"$match": b.M{"tags": b.M{"$in": allTags}}},
					b.M{"$project": projectFields},
				}},
				{"topics", b.A{
					b.M{"$lookup": b.D{
						{"from", "topics"},
						{"pipeline", b.A{
							b.M{"$match": b.M{"tags": b.M{"$in": allTags}}},
							b.M{"$project": projectFields},
						}},
						{"as", "topicDocs"},
					}},
					b.M{"$unwind": "$topicDocs"},
					b.M{"$replaceRoot": b.M{"newRoot": "$topicDocs"}},
				}},
			},
		},
		// Stage 2: $project
		b.M{"$project": b.M{"combined": b.M{"$concatArrays": b.A{"$users", "$topics"}}}},
		// Stage 3: $unwind
		b.M{"$unwind": "$combined"},
		// Stage 4: $replaceRoot
		b.M{"$replaceRoot": b.M{"newRoot": "$combined"}},
		// Stage 5: $group
		b.M{"$group": b.D{{"_id", "$_id"}, {"doc", b.M{"$first": "$$ROOT"}}}},
		// Stage 6: $replaceRoot
		b.M{"$replaceRoot": b.M{"newRoot": "$doc"}},
		// Stage 7: $addFields
		b.M{"$addFields": b.M{"matchedCount": b.M{"$sum": b.M{"$map": b.D{
			{"input", b.M{"$setIntersection": b.A{"$tags", allTags}}},
			{"as", "tag"},
			{"in", b.D{
				{"$cond", b.D{
					{"if", b.M{"$regexMatch": b.D{
						{"input", "$$tag"},
						{"regex", "^alias:"},
					},
					}},
					{"then", 20},
					{"else", 1},
				}}}}},
		}}}},
	}

	for _, reqDisjunction := range req {
		if len(reqDisjunction) == 0 {
			continue
		}
		var reqTags []any
		for _, tag := range reqDisjunction {
			reqTags = append(reqTags, tag)
		}
		// Filter out documents where 'tags' intersection with 'reqTags' is an empty array.
		pipeline = append(pipeline,
			b.M{"$match": b.M{"$expr": b.M{"$ne": b.A{b.M{"$size": b.M{"$setIntersection": b.A{"$tags", reqTags}}}, 0}}}})
	}

	pipeline = append(pipeline,
		// Stage 9: $sort
		b.M{"$sort": b.M{"matchedCount": -1}},
		// Stage 10: $limit
		b.M{"$limit": a.maxResults},
	)

	cur, err := a.db.Collection("users").Aggregate(a.ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var subs []t.Subscription
	for cur.Next(a.ctx) {
		var topic t.Topic
		var sub t.Subscription
		if err = cur.Decode(&topic); err != nil {
			break
		}

		if topic.UseBt {
			sub.Topic = t.GrpToChn(topic.Id)
		} else {
			if uid := t.ParseUid(topic.Id); !uid.IsZero() {
				topic.Id = uid.UserId()
				if topic.Id == caller {
					// Skip the caller.
					continue
				}
			}
			sub.Topic = topic.Id
		}

		sub.CreatedAt = topic.CreatedAt
		sub.UpdatedAt = topic.UpdatedAt
		sub.SetSubCnt(topic.SubCnt)
		sub.SetPublic(unmarshalBsonD(topic.Public))
		sub.SetTrusted(unmarshalBsonD(topic.Trusted))
		sub.SetDefaultAccess(topic.Access.Auth, topic.Access.Anon)
		// Indicating that the mode is not set, not 'N'.
		sub.ModeGiven = t.ModeUnset
		sub.ModeWant = t.ModeUnset
		sub.Private = common.FilterFoundTags(topic.Tags, index)
		subs = append(subs, sub)
	}

	if err == nil {
		err = cur.Err()
	}

	return subs, err
}

// FindOne returns topic or user which matches the given tag.
func (a *adapter) FindOne(tag string) (string, error) {
	// Part of the pipeline identical for users and topics collections.
	commonPipe := b.A{b.M{"$match": b.M{"tags": tag}}, b.M{"$project": b.M{"_id": 1}}}

	// Must create a copy of commonPipe so the original commonPipe can be used unmodified in $unionWith.
	pipeline := append(slices.Clone(commonPipe),
		b.M{"$unionWith": b.M{"coll": "topics", "pipeline": commonPipe}},
		b.M{"$limit": 1})
	cur, err := a.db.Collection("users").Aggregate(a.ctx, pipeline)
	if err != nil {
		return "", err
	}
	defer cur.Close(a.ctx)

	var found string
	if cur.Next(a.ctx) {
		entry := map[string]any{}
		if err = cur.Decode(&entry); err != nil {
			return "", err
		}

		if id, ok := entry["_id"].(string); ok {
			if user := t.ParseUid(id); !user.IsZero() {
				found = user.UserId()
			} else {
				found = id
			}
		}
	}

	return found, cur.Err()
}

// Messages

// MessageSave saves message to database
func (a *adapter) MessageSave(msg *t.Message) error {
	_, err := a.db.Collection("messages").InsertOne(a.ctx, msg)
	return err
}

// MessageGetAll returns messages matching the query
func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {
	var limit = a.maxMessageResults
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
	findOpts := mdbopts.Find().SetSort(b.D{{"topic", -1}, {"seqid", -1}})
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

	if err = a.decFileUseCounter(a.ctx, "messages", filter); err != nil {
		return err
	}

	return err
}

// rangeToFilter is Mongo's equivalent of common.RangeToSql.
func rangeToFilter(delRanges []t.Range, filter b.M) b.M {
	if len(delRanges) > 1 || delRanges[0].Hi <= delRanges[0].Low {
		rangeFilter := b.A{}
		for _, rng := range delRanges {
			if rng.Hi == 0 {
				rangeFilter = append(rangeFilter, b.M{"seqid": rng.Low})
			} else {
				rangeFilter = append(rangeFilter, b.M{"seqid": b.M{"$gte": rng.Low, "$lte": rng.Hi}})
			}
		}
		filter["$or"] = rangeFilter
	} else {
		filter["seqid"] = b.M{"$gte": delRanges[0].Low, "$lte": delRanges[0].Hi}
	}
	return filter
}

// MessageDeleteList marks messages as deleted.
// Soft- or Hard- is defined by forUser value: forUSer.IsZero == true is hard.
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	var err error

	if toDel == nil {
		// No filter: delete all messages.
		return a.messagesHardDelete(topic)
	}

	// Only some messages are being deleted

	delRanges := toDel.SeqIdRanges
	filter := b.M{
		"topic": topic,
		// Skip already hard-deleted messages.
		"delid": b.M{"$exists": false},
	}
	// Mongo's equivalent of common.RangeToSql
	rangeToFilter(delRanges, filter)

	if toDel.DeletedFor == "" {
		// Hard-deleting messages requires updates to the messages table.

		// We are asked to delete messages no older than newerThan.
		if newerThan := toDel.GetNewerThan(); newerThan != nil {
			filter["createdat"] = b.M{"$gt": newerThan}
		}

		pipeline := b.A{
			b.M{"$match": filter},
			b.M{"$project": b.M{"seqid": 1}},
		}

		// Find the actual IDs still present in the database.

		cur, err := a.db.Collection("messages").Aggregate(a.ctx, pipeline)
		if err != nil {
			return err
		}
		defer cur.Close(a.ctx)

		var seqIDs []int
		for cur.Next(a.ctx) {
			var result struct {
				SeqID int `bson:"seqid"`
			}
			if err = cur.Decode(&result); err != nil {
				return err
			}
			seqIDs = append(seqIDs, result.SeqID)
		}

		if len(seqIDs) == 0 {
			// Nothing to delete. No need to make a log entry. All done.
			return nil
		}

		// Recalculate the actual ranges to delete.
		sort.Ints(seqIDs)
		delRanges = t.SliceToRanges(seqIDs)

		// Compose a new query with the new ranges.
		filter = b.M{
			"topic": topic,
		}
		rangeToFilter(delRanges, filter)

		if err = a.decFileUseCounter(a.ctx, "messages", filter); err != nil {
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

	// Make log entries. Needed for both hard- and soft-deleting.
	_, err = a.db.Collection("dellog").InsertOne(a.ctx, toDel)
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
		SetSort(b.D{{"topic", 1}, {"delid", 1}}).
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

// Devices (for push notifications).

// DeviceUpsert creates or updates a device record.
func (a *adapter) DeviceUpsert(uid t.Uid, dev *t.DeviceDef) error {
	userId := uid.String()
	var user t.User
	err := a.db.Collection("users").FindOne(a.ctx, b.M{
		"_id":              userId,
		"devices.deviceid": dev.DeviceId}).Decode(&user)

	if err == nil && user.Id != "" { // current user owns this device
		// ArrayFilter used to avoid adding another (duplicate) device object. Update that device data
		updOpts := mdbopts.Update().SetArrayFilters(mdbopts.ArrayFilters{
			Filters: []any{b.M{"dev.deviceid": dev.DeviceId}}})
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
			b.M{"$set": b.M{"devices": []any{dev}}})
	}

	return err
}

// DeviceGetAll returns all devices for a given set of users
func (a *adapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	ids := make([]any, len(uids))
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

// DeviceDelete deletes a device record (push token).
func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	var err error
	filter := b.M{"_id": uid.String()}
	update := b.M{}
	if deviceID == "" {
		update["$set"] = b.M{"devices": []any{}}
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
func (a *adapter) FileFinishUpload(fd *t.FileDef, success bool, size int64) (*t.FileDef, error) {
	now := t.TimeNow()
	if success {
		// Mark upload as completed.
		if _, err := a.db.Collection("fileuploads").UpdateOne(a.ctx,
			b.M{"_id": fd.Id},
			b.M{"$set": b.M{
				"updatedat": now,
				"status":    t.UploadCompleted,
				"size":      size,
				"etag":      fd.ETag,
				"location":  fd.Location,
			}}); err != nil {

			return nil, err
		}
		fd.Status = t.UploadCompleted
		fd.Size = size
	} else {
		// Remove record: it's now useless.
		if _, err := a.db.Collection("fileuploads").DeleteOne(a.ctx, b.M{"_id": fd.Id}); err != nil {
			return nil, err
		}
		fd.Status = t.UploadFailed
		fd.Size = 0
	}

	fd.UpdatedAt = now

	return fd, nil
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

	var locations []string
	for cur.Next(a.ctx) {
		var result map[string]string
		if err := cur.Decode(&result); err != nil {
			return nil, err
		}
		locations = append(locations, result["location"])
	}

	_, err = a.db.Collection("fileuploads").DeleteMany(a.ctx, filter)
	return locations, err
}

// Given a filter query against 'messages' collection, decrement corresponding use counter in 'fileuploads' table.
func (a *adapter) decFileUseCounter(ctx context.Context, collection string, msgFilter b.M) error {
	// Copy msgFilter
	filter := b.M{}
	for k, v := range msgFilter {
		filter[k] = v
	}
	filter["attachments"] = b.M{"$exists": true}
	fileIds, err := a.db.Collection(collection).Distinct(ctx, "attachments", filter)
	if err != nil {
		return err
	}

	if len(fileIds) > 0 {
		_, err = a.db.Collection("fileuploads").UpdateMany(ctx,
			b.M{"_id": b.M{"$in": fileIds}},
			b.M{"$inc": b.M{"usecount": -1}})
	}

	return err
}

// FileLinkAttachments connects given topic or message to the file record IDs from the list.
func (a *adapter) FileLinkAttachments(topic string, userId, msgId t.Uid, fids []string) error {
	if len(fids) == 0 || (topic == "" && userId.IsZero() && msgId.IsZero()) {
		return t.ErrMalformed
	}

	now := t.TimeNow()
	var err error

	if msgId.IsZero() {
		// Only one link per user or topic is permitted.
		fids = fids[0:1]

		// Topics and users and mutable. Must unlink the previous attachments first.
		var table string
		var linkId string
		if topic != "" {
			table = "topics"
			linkId = topic
		} else {
			table = "users"
			linkId = userId.String()
		}

		// Find the old attachment.
		var attachments map[string][]string
		findOpts := mdbopts.FindOne().SetProjection(b.M{"attachments": 1, "_id": 0})
		err = a.db.Collection(table).FindOne(a.ctx, b.M{"_id": linkId}, findOpts).Decode(&attachments)
		if err != nil {
			return err
		}

		if len(attachments["attachments"]) > 0 {
			// Decrement the use count of old attachment.
			if _, err = a.db.Collection("fileuploads").UpdateOne(a.ctx,
				b.M{"_id": attachments["attachments"][0]},
				b.M{
					"$set": b.M{"updatedat": now},
					"$inc": b.M{"usecount": -1},
				},
			); err != nil {
				return err
			}
		}

		_, err = a.db.Collection(table).UpdateOne(a.ctx,
			b.M{"_id": linkId},
			b.M{"$set": b.M{"updatedat": now, "attachments": fids}})
		if err != nil {
			return err
		}
	} else {
		_, err = a.db.Collection("messages").UpdateOne(a.ctx,
			b.M{"_id": msgId.String()},
			b.M{"$set": b.M{"updatedat": now, "attachments": fids}})
		if err != nil {
			return err
		}
	}

	ids := make([]any, len(fids))
	for i, id := range fids {
		ids[i] = id
	}
	_, err = a.db.Collection("fileuploads").UpdateMany(a.ctx,
		b.M{"_id": b.M{"$in": ids}},
		b.M{
			"$set": b.M{"updatedat": now},
			"$inc": b.M{"usecount": 1},
		},
	)

	return err
}

// PCacheGet reads a persistet cache entry.
func (a *adapter) PCacheGet(key string) (string, error) {
	var value map[string]string
	findOpts := mdbopts.FindOneOptions{Projection: b.M{"value": 1, "_id": 0}}
	if err := a.db.Collection("kvmeta").FindOne(a.ctx, b.M{"_id": key}, &findOpts).Decode(&value); err != nil {
		if err == mdb.ErrNoDocuments {
			err = t.ErrNotFound
		}
		return "", err
	}
	return value["value"], nil
}

// PCacheUpsert creates or updates a persistent cache entry.
func (a *adapter) PCacheUpsert(key string, value string, failOnDuplicate bool) error {
	if strings.Contains(key, "^") {
		// Do not allow ^ in keys: it interferes with $match query.
		return t.ErrMalformed
	}

	collection := a.db.Collection("kvmeta")
	doc := b.M{
		"value": value,
	}

	if failOnDuplicate {
		doc["_id"] = key
		doc["createdat"] = t.TimeNow()
		_, err := collection.InsertOne(a.ctx, doc)
		if mdb.IsDuplicateKeyError(err) {
			err = t.ErrDuplicate
		}
		return err
	}

	res := collection.FindOneAndUpdate(a.ctx, b.M{"_id": key}, b.M{"$set": doc},
		mdbopts.FindOneAndUpdate().SetUpsert(true))
	return res.Err()
}

// PCacheDelete deletes one persistent cache entry.
func (a *adapter) PCacheDelete(key string) error {
	_, err := a.db.Collection("kvmeta").DeleteOne(a.ctx, b.M{"_id": key})
	return err
}

// PCacheExpire expires old entries with the given key prefix.
func (a *adapter) PCacheExpire(keyPrefix string, olderThan time.Time) error {
	if keyPrefix == "" {
		return t.ErrMalformed
	}

	_, err := a.db.Collection("kvmeta").DeleteMany(a.ctx, b.M{"createdat": b.M{"$lt": olderThan},
		"_id": primitive.Regex{Pattern: "^" + keyPrefix}})
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

// GetTestingAdapter returns an adapter object. Useful for running tests.
func GetTestingAdapter() *adapter {
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

func union(userTags, addTags []string) []string {
	for _, tag := range addTags {
		if !contains(userTags, tag) {
			userTags = append(userTags, tag)
		}
	}
	return userTags
}

func diff(userTags, removeTags []string) []string {
	var result []string
	for _, tag := range userTags {
		if !contains(removeTags, tag) {
			result = append(result, tag)
		}
	}
	return result
}

// normalizeUpdateMap turns keys that hardcoded as CamelCase into lowercase (MongoDB uses lowercase by default)
func normalizeUpdateMap(update map[string]any) map[string]any {
	result := make(map[string]any, len(update))
	for key, value := range update {
		result[strings.ToLower(key)] = value
	}

	return result
}

// Recursive unmarshalling of bson.D type.
// Mongo drivers unmarshalling into 'any' creates bson.D object for maps and bson.A object for slices.
// We need to manually unmarshal them into correct types: map[string]any and []any respectively.
func unmarshalBsonD(bsonObj any) any {
	if obj, ok := bsonObj.(b.D); ok && len(obj) != 0 {
		result := make(map[string]any)
		for key, val := range obj.Map() {
			result[key] = unmarshalBsonD(val)
		}
		return result
	} else if obj, ok := bsonObj.(primitive.Binary); ok {
		// primitive.Binary is a struct type with Subtype and Data fields. We need only Data ([]byte)
		return obj.Data
	} else if obj, ok := bsonObj.(b.A); ok {
		// in case of array of bson.D objects
		var result []any
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
