// +build mongodb

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
	ctx        context.Context
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

	// Options separately from ClientOptions (custom options):
	Database string `json:"database,omitempty"`

	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
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

	if config.Username != "" {
		var passwordSet bool
		if config.Password != "" {
			passwordSet = true
		}
		opts.SetAuth(
			mdbopts.Credential{
				AuthMechanism: "SCRAM-SHA-256",
				AuthSource:    "admin",
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

func getIdxOpts(field string) mdb.IndexModel {
	return mdb.IndexModel{Keys: b.M{field: 1}}
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

	// Collection "kvmeta" with metadata key-value pairs.
	// Key in "_id" field.

	// Users
	// Create secondary index on User.DeletedAt for finding soft-deleted users
	if _, err := a.db.Collection("users").Indexes().CreateOne(a.ctx, getIdxOpts("deletedat")); err != nil {
		return err
	}
	// Create secondary index on User.Tags array so user can be found by tags
	if _, err := a.db.Collection("users").Indexes().CreateOne(a.ctx, getIdxOpts("tags")); err != nil {
		return err
	}
	// Create secondary index for user.devices.deviceid to ensure Device ID uniqueness across users.
	// Partial filter set to avoid unique constraint for null values (when user object have no devices).
	if _, err := a.db.Collection("users").Indexes().CreateOne(a.ctx, mdb.IndexModel{
		Keys: b.M{"devices.deviceid": 1},
		Options: mdbopts.Index().
			SetUnique(true).
			SetPartialFilterExpression(b.M{"devices.deviceid": b.M{"$exists": true}}),
	}); err != nil {
		return err
	}

	// User authentication records {_id, userid, secret}
	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("auth").Indexes().CreateOne(a.ctx, getIdxOpts("userid")); err != nil {
		return err
	}

	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(a.ctx, getIdxOpts("user")); err != nil {
		return err
	}
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(a.ctx, getIdxOpts("topic")); err != nil {
		return err
	}

	// Topics stored in database
	// Secondary index on Owner field for deleting users.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(a.ctx, getIdxOpts("owner")); err != nil {
		return err
	}
	// Secondary index on Topic.Tags array so topics can be found by tags.
	// These tags are not unique as opposite to User.Tags.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(a.ctx, getIdxOpts("tags")); err != nil {
		return err
	}
	// Create system topic 'sys'.
	if err := createSystemTopic(a); err != nil {
		return err
	}

	// Stored message
	// Compound index of topic - seqID for selecting messages in a topic.
	if _, err := a.db.Collection("messages").Indexes().CreateOne(a.ctx, mdb.IndexModel{
		Keys: b.M{"topic": 1, "seqid": 1},
	}); err != nil {
		return err
	}
	// Compound index of hard-deleted messages
	if _, err := a.db.Collection("messages").Indexes().CreateOne(a.ctx, mdb.IndexModel{
		Keys: b.M{"topic": 1, "delid": 1},
	}); err != nil {
		return err
	}
	// Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
	// 		 [Topic, User1, DelId1], [Topic, User2, DelId2],...
	if _, err := a.db.Collection("messages").Indexes().CreateOne(a.ctx, mdb.IndexModel{
		Keys: b.M{"topic": 1, "deletedfor.user": 1, "deletedfor.delid": 1},
	}); err != nil {
		return err
	}

	// Log of deleted messages
	// Compound index of topic - delId
	if _, err := a.db.Collection("dellog").Indexes().CreateOne(a.ctx, mdb.IndexModel{
		Keys: b.M{"topic": 1, "delid": 1},
	}); err != nil {
		return err
	}

	// User credentials - contact information such as "email:jdoe@example.com" or "tel:+18003287448":
	// Id: "method:credential" like "email:jdoe@example.com". See types.Credential.
	// Create secondary index on credentials.User to be able to query credentials by user id.
	if _, err := a.db.Collection("credentials").Indexes().CreateOne(a.ctx, getIdxOpts("user")); err != nil {
		return err
	}

	// Records of file uploads. See types.FileDef.
	// A secondary index on fileuploads.User to be able to get records by user id.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(a.ctx, getIdxOpts("user")); err != nil {
		return err
	}
	// A secondary index on fileuploads.UseCount to be able to delete unused records at once.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(a.ctx, getIdxOpts("usecount")); err != nil {
		return err
	}

	// Record current DB version.
	if _, err := a.db.Collection("kvmeta").InsertOne(a.ctx, map[string]interface{}{"_id": "version", "value": adpVersion}); err != nil {
		return err
	}

	return nil
}

// TODO: UpgradeDb upgrades database to the current adapter version.
func (a *adapter) UpgradeDb() error {
	return nil
}

// Create system topic 'sys'.
func createSystemTopic(a *adapter) error {
	now := t.TimeNow()
	_, err := a.db.Collection("topics").InsertOne(a.ctx, &t.Topic{
		ObjHeader: t.ObjHeader{Id: "sys", CreatedAt: now, UpdatedAt: now},
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

	filter := b.M{"$and": b.A{
		b.M{"_id": id.String()},
		b.M{"deletedat": b.M{"$exists": false}}}}
	if err := a.db.Collection("users").FindOne(a.ctx, filter).Decode(&user); err != nil {
		if err == mdb.ErrNoDocuments { // User not found
			return nil, nil
		} else {
			return nil, err
		}
	}

	return &user, nil
}

// UserGetAll returns user records for a given list of user IDs
func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = id.String()
	}

	var users []t.User
	filter := b.M{"$and": b.A{
		b.M{"_id": b.M{"$in": uids}},
		b.M{"deletedat": b.M{"$exists": false}}}}
	if cur, err := a.db.Collection("users").Find(a.ctx, filter); err == nil {
		defer cur.Close(a.ctx)

		var user t.User
		for cur.Next(a.ctx) {
			if err := cur.Decode(&user); err != nil {
				return nil, err
			}
			users = append(users, user)
		}
		return users, nil
	} else {
		return nil, err
	}
}

// TODO: UserDelete deletes user record
func (a *adapter) UserDelete(id t.Uid, hard bool) error {
	return nil
}

// UserGetDisabled returns IDs of users which were soft-deleted since given time.
func (a *adapter) UserGetDisabled(since time.Time) ([]t.Uid, error) {
	filter := b.M{"deletedat": b.M{"$gte": since}}
	findOpts := mdbopts.FindOptions{Projection: b.M{"_id": 1}}
	if cur, err := a.db.Collection("users").Find(a.ctx, filter, &findOpts); err == nil {
		defer cur.Close(a.ctx)

		var uids []t.Uid
		var userId map[string]string
		for cur.Next(a.ctx) {
			if err := cur.Decode(&userId); err != nil {
				return nil, err
			}
			uids = append(uids, t.ParseUid(userId["_id"]))
		}
		return uids, nil
	} else {
		return nil, err
	}
}

// UserUpdate updates user record
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	if val, ok := update["UpdatedAt"]; ok { // to get round the hardcoded "UpdatedAt" key in store.Users.Update()
		update["updatedat"] = val
		delete(update, "UpdatedAt")
	}

	_, err := a.db.Collection("users").UpdateOne(a.ctx, b.M{"_id": uid.String()}, b.M{"$set": update})
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
	if err := a.db.Collection("users").FindOne(a.ctx, b.M{"_id": uid.String()}).Decode(&user); err != nil {
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
	findOpts := mdbopts.FindOneOptions{Projection: b.M{"tags": 1, "_id": 0}}
	if err := a.db.Collection("users").FindOne(a.ctx, b.M{"_id": uid.String()}, &findOpts).Decode(&tags); err == nil {
		return tags["tags"], nil
	} else {
		return nil, err
	}
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	var userId map[string]string
	filter := b.M{"_id": method + ":" + value}
	findOpts := mdbopts.FindOneOptions{Projection: b.M{"user": 1, "_id": 0}}
	err := a.db.Collection("credentials").FindOne(a.ctx, filter, &findOpts).Decode(&userId)
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
			// Filter by access mode
			"modewant":  b.M{"$bitsAllSet": b.A{1}},
			"modegiven": b.M{"$bitsAllSet": b.A{1}}}},

		b.M{"$group": b.M{"_id": nil, "unreadCount": b.M{"$sum": b.M{"$subtract": b.A{"$seqid", "$readseqid"}}}}},
	}
	cur, err := a.db.Collection("subscriptions").Aggregate(a.ctx, pipeline)
	if err != nil {
		if err == mdb.ErrNilCursor {
			return 0, nil
		}
		return 0, err
	}
	defer cur.Close(a.ctx)

	var result struct {
		Id          interface{} `bson:"_id"`
		UnreadCount int         `bson:"unreadCount"`
	}
	for cur.Next(a.ctx) {
		if err = cur.Decode(&result); err != nil {
			return 0, err
		}
	}
	return result.UnreadCount, nil
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
	filter := b.M{}
	if method != "" {
		filter["method"] = method
	}
	if validatedOnly {
		filter["done"] = true
	} else {
		filter["deletedat"] = b.M{"$exists": false}
	}

	if cur, err := a.db.Collection("credentials").Find(a.ctx, filter); err == nil {
		var credentials []t.Credential
		if err := cur.All(a.ctx, &credentials); err != nil {
			return nil, err
		}
		return credentials, nil
	} else {
		return nil, err
	}
}

// CredDel deletes credentials for the given method/value. If method is empty, deletes all
// user's credentials.
func (a *adapter) CredDel(uid t.Uid, method, value string) error {
	credCollection := a.db.Collection("credentials")
	filterAnd := b.A{}
	filterUser := b.M{"user": uid.String()}
	filterAnd = append(filterAnd, filterUser)
	if method != "" {
		filterAnd = append(filterAnd, b.M{"method": method})
		if value != "" {
			filterAnd = append(filterAnd, b.M{"value": value})
		}
	} else {
		_, err := credCollection.DeleteMany(a.ctx, filterUser)
		return err
	}

	// Hard-delete all confirmed values or values with no attempts at confirmation.
	filterOr := b.M{"$or": b.A{
		b.M{"done": true},
		b.M{"retries": 0}}}
	filterAnd = append(filterAnd, filterOr)
	filter := b.M{"$and": filterAnd}
	if _, err := credCollection.DeleteMany(a.ctx, filter); err != nil {
		return err
	}

	// Soft-delete all other values.
	_, err := credCollection.UpdateMany(a.ctx, filter, b.M{"$set": b.M{"deletedat": t.TimeNow()}})
	return err
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
	findOpts := mdbopts.FindOneOptions{Projection: b.M{
		"userid":  1,
		"authLvl": 1,
		"secret":  1,
		"expires": 1}}
	if err := a.db.Collection("auth").FindOne(a.ctx, filter, &findOpts).Decode(&record); err != nil {
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
	filter := b.M{
		"userid": uid.String(),
		"scheme": scheme}
	findOpts := &mdbopts.FindOneOptions{Projection: b.M{
		"authLvl": 1,
		"secret":  1,
		"expires": 1,
	}}
	if err := a.db.Collection("auth").FindOne(a.ctx, filter, findOpts).Decode(&record); err != nil {
		if err == mdb.ErrNoDocuments {
			return "", 0, nil, time.Time{}, t.ErrNotFound
		}
		return "", 0, nil, time.Time{}, err
	}

	return record.Id, record.AuthLvl, record.Secret, record.Expires, nil
}

// AuthAddRecord creates new authentication record
func (a *adapter) AuthAddRecord(uid t.Uid, scheme, unique string, authLvl auth.Level, secret []byte, expires time.Time) (bool, error) {
	authRecord := b.M{
		"_id":     unique,
		"userid":  uid.String(),
		"scheme":  scheme,
		"authlvl": authLvl,
		"secret":  secret,
		"expires": expires}
	if _, err := a.db.Collection("auth").InsertOne(a.ctx, authRecord); err != nil {
		if isDuplicateErr(err) {
			return true, t.ErrDuplicate
		}
		return false, err
	}
	return false, nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(uid t.Uid, scheme string) error {
	_, err := a.db.Collection("auth").DeleteOne(a.ctx,
		b.M{
			"userid": uid.String(),
			"scheme": scheme})
	return err
}

// AuthDelAllRecords deletes all records of a given user.
func (a *adapter) AuthDelAllRecords(uid t.Uid) (int, error) {
	res, err := a.db.Collection("auth").DeleteMany(a.ctx, b.M{"userid": uid.String()})
	return int(res.DeletedCount), err
}

// AuthUpdRecord modifies an authentication record.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string,
	authLvl auth.Level, secret []byte, expires time.Time) (bool, error) {
	// The primary key is immutable. If '_id' has changed, we have to replace the old record with a new one:
	// 1. Check if '_id' has changed.
	// 2. If not, execute update by '_id'
	// 3. If yes, first insert the new record (it may fail due to dublicate '_id') then delete the old one.

	var dupe bool
	var err error
	var record struct{ Unique string `bson:"_id"` }
	findOpts := mdbopts.FindOneOptions{Projection: b.M{"_id": 1}}
	filter := b.M{"userid": uid.String(), "scheme": scheme}
	if err = a.db.Collection("auth").FindOne(a.ctx, filter, &findOpts).Decode(&record); err != nil {
		return false, err
	}

	if record.Unique == unique {
		_, err = a.db.Collection("auth").UpdateOne(a.ctx,
			b.M{"_id": unique},
			b.M{"$set": b.M{
				"authlvl": authLvl,
				"secret":  secret,
				"expires": expires}})
	} else {
		var sess mdb.Session
		if sess, err = a.conn.StartSession(); err != nil {
			return false, err
		}
		if err = sess.StartTransaction(); err != nil {
			return false, err
		}
		if err = mdb.WithSession(a.ctx, sess, func(sc mdb.SessionContext) error {
			dupe, err = a.AuthAddRecord(uid, scheme, unique, authLvl, secret, expires)
			if err != nil {
				return err
			}
			if err = a.AuthDelScheme(uid, scheme); err != nil {
				return err
			}
			return sess.CommitTransaction(a.ctx)
		}); err != nil {
			return dupe, err
		}
		sess.EndSession(a.ctx)
	}

	return dupe, err
}

// Topic management

// TopicCreate creates a topic
func (a *adapter) TopicCreate(topic *t.Topic) error {
	_, err := a.db.Collection("topics").InsertOne(a.ctx, &topic)
	return err
}

// TopicCreateP2P creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	initiator.Id = initiator.Topic + ":" + initiator.User
	// Don't care if the initiator changes own subscription
	replOpts := mdbopts.ReplaceOptions{}
	replOpts.SetUpsert(true)
	_, err := a.db.Collection("subscriptions").ReplaceOne(a.ctx, b.M{"_id": initiator.Id}, initiator, &replOpts)
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
		_, err = a.db.Collection("subscriptions").UpdateOne(a.ctx,
			b.M{"_id": invited.Id},
			b.M{
				"$unset": b.M{"deletedat": ""},
				"$set": b.M{
					"updatedat": invited.UpdatedAt,
					"createdat": invited.CreatedAt,
					"modegiven": invited.ModeGiven}})
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

	cur, err := a.db.Collection("subscriptions").Find(a.ctx, filter, mdbopts.Find().SetLimit(int64(limit)))
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
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				// all done with a grp topic
				sub.SetPublic(top.Public)
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
			filter["deletedat"] = b.M{"$exists": false}
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
				sub.SetPublic(usr.Public)
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
			"_id":       b.M{"$in": usrq},
			"deletedat": b.M{"$exists": false}})
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
				sub.SetPublic(usr.Public)
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
	filter := b.M{"owner": uid.String(), "deletedat": b.M{"$exists": false}}
	findOpts := mdbopts.FindOptions{Projection: b.M{"_id": 1}}
	cur, err := a.db.Collection("topics").Find(a.ctx, filter, &findOpts)
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
	var sess mdb.Session
	var err error
	if sess, err = a.conn.StartSession(); err != nil {
		return err
	}
	if err = sess.StartTransaction(); err != nil {
		return err
	}
	if err = mdb.WithSession(a.ctx, sess, func(sc mdb.SessionContext) error {
		for _, sub := range subs {
			if _, err := a.db.Collection("subscriptions").InsertOne(a.ctx, sub); err != nil {
				if isDuplicateErr(err) {
					_, err := a.db.Collection("subscriptions").UpdateOne(a.ctx,
						b.M{"_id": sub.Id},
						b.M{
							"$unset": b.M{"deletedat": ""},
							"$set": b.M{
								"createdat": sub.CreatedAt,
								"updatedat": sub.UpdatedAt,
								"modegiven": sub.ModeGiven}})
					if err != nil {
						return err
					}
				}
				return err
			}
		}
		if err := sess.CommitTransaction(a.ctx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	sess.EndSession(a.ctx)

	return err
}

// TODO (after a.SubsDelForTopic() and a.MessageDeleteList() done): TopicDelete deletes topic, subscription, messages
func (a *adapter) TopicDelete(topic string, hard bool) error {
	return nil
}

// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	_, err := a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$set": b.M{
			"seqid":     msg.SeqId,
			"touchedat": msg.CreatedAt}})

	return err
}

// TopicUpdate updates topic record.
func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	// to get round the hardcoded "UpdatedAt" key in store.Topics.Update()
	if val, ok := update["UpdatedAt"]; ok {
		update["updatedat"] = val
		delete(update, "UpdatedAt")
	}

	_, err := a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$set": update})
	return err
}

// TopicOwnerChange updates topic's owner
func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	_, err := a.db.Collection("topics").UpdateOne(a.ctx,
		b.M{"_id": topic},
		b.M{"$set": b.M{"owner": newOwner}})

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
		subs = append(subs, ss)
	}

	return subs, cur.Err()
}

// SubsUpdate updates pasrt of a subscription object. Pass nil for fields which don't need to be updated
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
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
		b.M{"updatedat": now, "deletedat": now})
	return err
}

// SubsDelForTopic deletes all subscriptions to the given topic
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	var err error
	filter := b.M{"topic": topic}
	if hard {
		_, err = a.db.Collection("subscriptions").DeleteMany(a.ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("subscriptions").UpdateMany(a.ctx,
			filter,
			b.M{"$set": b.M{"updatedat": now, "deletedat": now}})
	}
	return err
}

// SubsDelForUser deletes all subscriptions of the given user
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	var err error
	filter := b.M{"user": user.String()}
	if hard {
		_, err = a.db.Collection("subscriptions").DeleteMany(a.ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("subscriptions").UpdateMany(a.ctx,
			filter,
			b.M{"$set": b.M{"updatedat": now, "deletedat": now}})
	}
	return err
}

// Search

// TODO: FindUsers searches for new contacts given a list of tags
func (a *adapter) FindUsers(user t.Uid, req, opt []string) ([]t.Subscription, error) {
	return nil, nil
}

// TODO: FindTopics searches for group topics given a list of tags
func (a *adapter) FindTopics(req, opt []string) ([]t.Subscription, error) {
	return nil, nil
}

// Messages

// MessageSave saves message to database
func (a *adapter) MessageSave(msg *t.Message) error {
	msg.SetUid(store.GetUid())
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
		filter["seqid"] = b.M{"$gte": lower, "$lte": upper}
	}
	findOpts := mdbopts.Find().SetSort(b.M{"topic": -1, "seqid": -1})
	findOpts.SetLimit(int64(limit))

	cur, err := a.db.Collection("messages").Find(a.ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(a.ctx)

	var msgs []t.Message
	if err = cur.All(a.ctx, &msgs); err != nil {
		return nil, err
	}

	return msgs, nil
}

func (a *adapter) messagesHardDelete(topic string) error {
	var err error

	// TODO: handle file uploads

	if _, err = a.db.Collection("dellog").DeleteMany(a.ctx, b.M{"topic": topic}); err != nil {
		return err
	}

	if _, err = a.db.Collection("messages").DeleteMany(a.ctx, b.M{"topic": topic}); err != nil {
		return err
	}

	return err
}

// MessageDeleteList marks messages as deleted.
// Soft- or Hard- is defined by forUser value: forUSer.IsZero == true is hard.
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	var err error

	if toDel == nil {
		err = a.messagesHardDelete(topic)
	} else {
		// Only some messages are being deleted
		toDel.SetUid(store.GetUid())

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
		if len(toDel.SeqIdRanges) == 1 {
			filter["seqid"] = b.M{"seqid": b.M{"$gte": toDel.SeqIdRanges[0].Low, "$lte": toDel.SeqIdRanges[0].Hi}}
		} else {
			rangeFilter := b.A{}
			for _, rng := range toDel.SeqIdRanges {
				if rng.Hi == 0 {
					rangeFilter = append(rangeFilter, b.M{"seqid": b.M{"$gte": rng.Low}})
				} else {
					rangeFilter = append(rangeFilter, b.M{"seqid": b.M{"$gte": rng.Low, "$lte": rng.Hi}})
				}
			}
			filter["$or"] = rangeFilter
		}

		if toDel.DeletedFor == "" {
			// Hard-delete individual messages. Message is not deleted but all fields with content
			// are replaced with nulls.
			_, err = a.db.Collection("messages").UpdateMany(a.ctx, filter, b.M{"$set": b.M{
				"deletedat":   t.TimeNow(),
				"delid":       toDel.DelId,
				"from":        nil,
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
		filter["delid"] = b.M{"$gte": lower, "$lte": upper}
	}
	findOpts := mdbopts.Find().SetSort(b.M{"topic": 1, "delid": 1})
	findOpts.SetLimit(int64(limit))

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
		"devices.deviceid": dev.DeviceId,}).Decode(&user)

	if err == nil && user.Id != "" { // current user owns this device
		// ArrayFilter used to avoid adding another (duplicate) device object. Update that device data
		updOpts := mdbopts.Update().SetArrayFilters(mdbopts.ArrayFilters{
			Filters: []interface{}{b.M{"dev.deviceid": dev.DeviceId}},})
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

	var row struct {
		Id      string `bson:"_id"`
		Devices []t.DeviceDef
	}

	result := make(map[t.Uid][]t.DeviceDef)
	count := 0
	var uid t.Uid
	for cur.Next(a.ctx) {
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
	findOpts := new(mdbopts.FindOptions)
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

	var result map[string]string
	var locations []string
	for cur.Next(a.ctx) {
		if err := cur.Decode(&result); err != nil {
			return nil, err
		}
		locations = append(locations, result["location"])
	}

	_, err = a.db.Collection("fileuploads").DeleteMany(a.ctx, filter)
	cur.Close(a.ctx)
	return locations, err
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

func isDuplicateErr(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "duplicate key error")
}
