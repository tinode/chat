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
}

const (
	defaultHost     = "localhost:27017"
	defaultDatabase = "tinode"

	adpVersion  = 108
	adapterName = "mongodb"

	defaultMaxResults = 1024
)

var ctx context.Context

// See https://godoc.org/go.mongodb.org/mongo-driver/mongo/options#ClientOptions for explanations.
type configType struct {
	Addresses      interface{} `json:"addresses,omitempty"`
	ConnectTimeout int         `json:"timeout,omitempty"`

	// Options separately from ClientOptions (custom options):
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

	a.conn, err = mdb.Connect(ctx, &opts)
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
		err = a.conn.Disconnect(ctx)
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
	if err := a.db.Collection("kvmeta").FindOne(ctx, b.M{"_id": "version"}).Decode(&result); err != nil {
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

func getIdxOpts(field string, unique bool) mdb.IndexModel {
	if unique {
		var idxUniqueOpt mdbopts.IndexOptions
		idxUniqueOpt.SetUnique(true)
		return mdb.IndexModel{Keys: b.M{field: 1}, Options: &idxUniqueOpt}
	} else {
		return mdb.IndexModel{Keys: b.M{field: 1}}
	}
}

// CreateDb creates the database optionally dropping an existing database first.
func (a *adapter) CreateDb(reset bool) error {
	if reset {
		log.Print("Dropping database...")
		if err := a.db.Drop(ctx); err != nil {
			return nil
		}
	} else if a.isDbInitialized() {
		return errors.New("Database already initialized")
	}
	// Collections (tables) do not need to be explicitly created since MongoDB creates them with first write operation

	// Collection "kvmeta" with metadata key-value pairs.
	// Key in "_id" field.

	// Users
	// Create secondary index on User.DeletedAt for finding soft-deleted users
	if _, err := a.db.Collection("users").Indexes().CreateOne(ctx, getIdxOpts("deletedat", false)); err != nil {
		return err
	}
	// Create secondary index on User.Tags array so user can be found by tags
	if _, err := a.db.Collection("users").Indexes().CreateOne(ctx, getIdxOpts("tags", false)); err != nil {
		return err
	}
	// TODO: Create secondary index for User.Devices.<hash>.DeviceId to ensure ID uniqueness across users

	// User authentication records {_id, userid, secret}
	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("auth").Indexes().CreateOne(ctx, getIdxOpts("userid", false)); err != nil {
		return err
	}

	// Should be able to access user's auth records by user id
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(ctx, getIdxOpts("user", false)); err != nil {
		return err
	}
	if _, err := a.db.Collection("subscriptions").Indexes().CreateOne(ctx, getIdxOpts("topic", false)); err != nil {
		return err
	}

	// Topics stored in database
	// Secondary index on Owner field for deleting users.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(ctx, getIdxOpts("owner", false)); err != nil {
		return err
	}
	// Secondary index on Topic.Tags array so topics can be found by tags.
	// These tags are not unique as opposite to User.Tags.
	if _, err := a.db.Collection("topics").Indexes().CreateOne(ctx, getIdxOpts("tags", false)); err != nil {
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
	if _, err := a.db.Collection("credentials").Indexes().CreateOne(ctx, getIdxOpts("user", false)); err != nil {
		return err
	}

	// Records of file uploads. See types.FileDef.
	// A secondary index on fileuploads.User to be able to get records by user id.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(ctx, getIdxOpts("user", false)); err != nil {
		return err
	}
	// A secondary index on fileuploads.UseCount to be able to delete unused records at once.
	if _, err := a.db.Collection("fileuploads").Indexes().CreateOne(ctx, getIdxOpts("usecount", false)); err != nil {
		return err
	}

	// Record current DB version.
	if _, err := a.db.Collection("kvmeta").InsertOne(ctx, map[string]interface{}{"_id": "version", "value": adpVersion}); err != nil {
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
	_, err := a.db.Collection("topics").InsertOne(ctx, &t.Topic{
		ObjHeader: t.ObjHeader{Id: "sys", CreatedAt: now, UpdatedAt: now},
		Access:    t.DefaultAccess{Auth: t.ModeNone, Anon: t.ModeNone},
		Public:    map[string]interface{}{"fn": "System"},
	})
	return err
}

// User management

// UserCreate creates user record
func (a *adapter) UserCreate(usr *t.User) error {
	if _, err := a.db.Collection("users").InsertOne(ctx, &usr); err != nil {
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
	if err := a.db.Collection("users").FindOne(ctx, filter).Decode(&user); err != nil {
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
	if cur, err := a.db.Collection("users").Find(ctx, filter); err == nil {
		defer cur.Close(ctx)

		var user t.User
		for cur.Next(ctx) {
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
	if cur, err := a.db.Collection("users").Find(ctx, filter, &findOpts); err == nil {
		defer cur.Close(ctx)

		var uids []t.Uid
		var userId map[string]string
		for cur.Next(ctx) {
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

	_, err := a.db.Collection("users").UpdateOne(ctx, b.M{"_id": uid.String()}, b.M{"$set": update})
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
	if err := a.db.Collection("users").FindOne(ctx, b.M{"_id": uid.String()}).Decode(&user); err != nil {
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
	if err := a.db.Collection("users").FindOne(ctx, b.M{"_id": uid.String()}, &findOpts).Decode(&tags); err == nil {
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
	err := a.db.Collection("credentials").FindOne(ctx, filter, &findOpts).Decode(&userId)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return t.ZeroUid, nil
		}
		return t.ZeroUid, err
	}

	return t.ParseUid(userId["user"]), nil
}

// TODO: UserUnreadCount returns the total number of unread messages in all topics with
// the R permission.
func (a *adapter) UserUnreadCount(uid t.Uid) (int, error) {
	return 0, nil
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
		err := credCollection.FindOne(ctx, b.M{"_id": cred.Id}).Decode(&result1)
		if result1 != (t.Credential{}) {
			// Someone has already validated this credential.
			return false, t.ErrDuplicate
		} else if err != nil && err != mdb.ErrNoDocuments { // if no result -> continue
			return false, err
		}

		// Soft-delete all unvalidated records of this user and method.
		_, err = credCollection.UpdateMany(ctx,
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
		err = credCollection.FindOne(ctx, b.M{"_id": cred.Id}).Decode(&result2)
		if result2 != (t.Credential{}) {
			_, err = credCollection.UpdateOne(ctx,
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
		_, err := credCollection.DeleteOne(ctx, b.M{"_id": cred.User + ":" + cred.Id})
		if err != nil {
			return false, err
		}
	}

	// Insert a new record.
	_, err := credCollection.InsertOne(ctx, cred)
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

	if err := a.db.Collection("credentials").FindOne(ctx, filter).Decode(&cred); err != nil {
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

	if cur, err := a.db.Collection("credentials").Find(ctx, filter); err == nil {
		var credentials []t.Credential
		if err := cur.All(ctx, &credentials); err != nil {
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
		_, err := credCollection.DeleteMany(ctx, filterUser)
		return err
	}

	// Hard-delete all confirmed values or values with no attempts at confirmation.
	filterOr := b.M{"$or": b.A{
		b.M{"done": true},
		b.M{"retries": 0}}}
	filterAnd = append(filterAnd, filterOr)
	filter := b.M{"$and": filterAnd}
	if _, err := credCollection.DeleteMany(ctx, filter); err != nil {
		return err
	}

	// Soft-delete all other values.
	_, err := credCollection.UpdateMany(ctx, filter, b.M{"$set": b.M{"deletedat": t.TimeNow()}})
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

	_, _ = a.db.Collection("credentials").DeleteOne(ctx, b.M{"_id": uid.String() + ":" + cred.Method + ":" + cred.Value})
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
	_, err := a.db.Collection("credentials").UpdateOne(ctx, filter, update)
	return err
}

// Authentication management for the basic authentication scheme

// AuthGetUniqueRecord returns authentication record for a given unique value i.e. login.
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	var record struct {
		Id      string `bson:"_id"`
		AuthLvl auth.Level
		Secret  []byte
		Expires time.Time
	}
	filter := b.M{"_id": unique}
	findOpts := mdbopts.FindOneOptions{Projection: b.M{
		"authLvl": 1,
		"secret":  1,
		"expires": 1}}
	if err := a.db.Collection("auth").FindOne(ctx, filter, &findOpts).Decode(&record); err != nil {
		if err == mdb.ErrNoDocuments {
			return t.ZeroUid, 0, nil, time.Time{}, nil
		}
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	return t.ParseUid(record.Id), record.AuthLvl, record.Secret, record.Expires, nil
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
	if err := a.db.Collection("auth").FindOne(ctx, filter, findOpts).Decode(&record); err != nil {
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
	if _, err := a.db.Collection("auth").InsertOne(ctx, authRecord); err != nil {
		if isDuplicateErr(err) {
			return true, t.ErrDuplicate
		}
		return false, err
	}
	return false, nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(uid t.Uid, scheme string) error {
	_, err := a.db.Collection("auth").DeleteOne(ctx,
		b.M{
			"userid": uid.String(),
			"scheme": scheme})
	return err
}

// AuthDelAllRecords deletes all records of a given user.
func (a *adapter) AuthDelAllRecords(uid t.Uid) (int, error) {
	res, err := a.db.Collection("auth").DeleteMany(ctx, b.M{"userid": uid.String()})
	return int(res.DeletedCount), err
}

// TODO (fix): AuthUpdRecord modifies an authentication record.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string,
	authLvl auth.Level, secret []byte, expires time.Time) (bool, error) {
	//filter := b.M{
	//	"userid": uid.String(),
	//	"scheme": scheme}
	//update := b.M{"$set": b.M{
	//	"unique":  unique,
	//	"authlvl": authLvl,
	//	"secret":  secret,
	//	"expires": expires,
	//}}
	//
	//if _, err := a.db.Collection("auth").UpdateOne(ctx, filter, update); err != nil {
	//	return true, err
	//}
	return false, nil
}

// Topic management

// TopicCreate creates a topic
func (a *adapter) TopicCreate(topic *t.Topic) error {
	_, err := a.db.Collection("topics").InsertOne(ctx, &topic)
	return err
}

// TopicCreateP2P creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	initiator.Id = initiator.Topic + ":" + initiator.User
	// Don't care if the initiator changes own subscription
	replOpts := mdbopts.ReplaceOptions{}
	replOpts.SetUpsert(true)
	_, err := a.db.Collection("subscriptions").ReplaceOne(ctx, b.M{}, initiator, &replOpts)
	if err != nil {
		return err
	}

	// If the second subscription exists, don't overwrite it. Just make sure it's not deleted.
	invited.Id = invited.Topic + ":" + invited.User
	_, err = a.db.Collection("subscriptions").InsertOne(ctx, invited)
	if err != nil {
		// Is this a duplicate subscription?
		if !isDuplicateErr(err) {
			// It's a genuine DB error
			return err
		}
		// Undelete the second subsription if it exists: remove DeletedAt, update CreatedAt and UpdatedAt,
		// update ModeGiven.
		_, err = a.db.Collection("subscriptions").UpdateOne(ctx,
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
	err := a.db.Collection("topics").FindOne(ctx, b.M{"_id": topic}).Decode(tpc)
	if err != nil {
		if err == mdb.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return tpc, nil
}

// TODO: TopicsForUser loads subscriptions for a given user. Reads public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// TODO: UsersForTopic loads users' subscriptions for a given topic. Public is loaded.
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	return nil, nil
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid) ([]string, error) {
	filter := b.M{"owner": uid.String(), "deletedat": b.M{"$exists": false}}
	findOpts := mdbopts.FindOptions{Projection: b.M{"_id": 1}}
	cur, err := a.db.Collection("topics").Find(ctx, filter, &findOpts)
	if err != nil {
		return nil, err
	}

	var res map[string]string
	var names []string
	for cur.Next(ctx) {
		if err := cur.Decode(&res); err == nil {
			names = append(names, res["_id"])
		} else {
			return nil, err
		}
	}
	cur.Close(ctx)

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
	if err = mdb.WithSession(ctx, sess, func(sc mdb.SessionContext) error {
		for _, sub := range subs {
			if _, err := a.db.Collection("subscriptions").InsertOne(ctx, sub); err != nil {
				if isDuplicateErr(err) {
					_, err := a.db.Collection("subscriptions").UpdateOne(ctx,
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
		if err := sess.CommitTransaction(ctx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	sess.EndSession(ctx)

	return err
}

// TODO (after a.SubsDelForTopic() and a.MessageDeleteList() done): TopicDelete deletes topic, subscription, messages
func (a *adapter) TopicDelete(topic string, hard bool) error {
	return nil
}

// TopicUpdateOnMessage increments Topic's or User's SeqId value and updates TouchedAt timestamp.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	_, err := a.db.Collection("topics").UpdateOne(ctx,
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

	_, err := a.db.Collection("topics").UpdateOne(ctx,
		b.M{"_id": topic},
		b.M{"$set": update})
	return err
}

// TopicOwnerChange updates topic's owner
func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	_, err := a.db.Collection("topics").UpdateOne(ctx,
		b.M{"_id": topic},
		b.M{"$set": b.M{"owner": newOwner}})

	return err
}

// Topic subscriptions

// SubscriptionGet reads a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {
	sub := new(t.Subscription)
	err := a.db.Collection("subscriptions").FindOne(ctx, b.M{
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

	cur, err := a.db.Collection("subscriptions").Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var subs []t.Subscription
	var ss t.Subscription
	for cur.Next(ctx) {
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

	cur, err := a.db.Collection("subscriptions").Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var subs []t.Subscription
	var ss t.Subscription
	for cur.Next(ctx) {
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
	_, err := a.db.Collection("subscriptions").UpdateOne(ctx, filter, b.M{"$set": update})
	return err
}

// SubsDelete deletes a single subscription
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	_, err := a.db.Collection("subscriptions").UpdateOne(ctx,
		b.M{"_id": topic + ":" + user.String()},
		b.M{"updatedat": now, "deletedat": now})
	return err
}

// SubsDelForTopic deletes all subscriptions to the given topic
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	var err error
	filter := b.M{"topic": topic}
	if hard {
		_, err = a.db.Collection("subscriptions").DeleteMany(ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("subscriptions").UpdateMany(ctx,
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
		_, err = a.db.Collection("subscriptions").DeleteMany(ctx, filter)
	} else {
		now := t.TimeNow()
		_, err = a.db.Collection("subscriptions").UpdateMany(ctx,
			filter,
			b.M{"$set": b.M{"updatedat": now, "deletedat": now}})
	}
	return err
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
	msg.SetUid(store.GetUid())
	_, err := a.db.Collection("messages").InsertOne(ctx, msg)
	return err
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
	now := t.TimeNow()
	_, err := a.db.Collection("messages").UpdateOne(ctx,
		b.M{"_id": msgId.String()},
		b.M{"$set": b.M{"updatedat": now, "attachments": fids}})
	if err != nil {
		return err
	}

	ids := make([]interface{}, len(fids))
	for i, id := range fids {
		ids[i] = id
	}
	_, err = a.db.Collection("fileuploads").UpdateMany(ctx,
		b.M{"_id": b.M{"$in": ids}},
		b.M{
			"$set": b.M{"updatedat": now},
			"$inc": b.M{"usecount": 1}})

	return err
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
	_, err := a.db.Collection("fileuploads").InsertOne(ctx, fd)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise.
func (a *adapter) FileFinishUpload(fid string, status int, size int64) (*t.FileDef, error) {
	if _, err := a.db.Collection("fileuploads").UpdateOne(ctx,
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
	err := a.db.Collection("fileuploads").FindOne(ctx, b.M{"_id": fid}).Decode(&fd)
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
	cur, err := a.db.Collection("fileuploads").Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}

	var result map[string]string
	var locations []string
	for cur.Next(ctx) {
		if err := cur.Decode(&result); err != nil {
			return nil, err
		}
		locations = append(locations, result["location"])
	}

	_, err = a.db.Collection("fileuploads").DeleteMany(ctx, filter)
	cur.Close(ctx)
	return locations, err
}

func (a *adapter) isDbInitialized() bool {
	var result map[string]int

	findOpts := mdbopts.FindOneOptions{Projection: b.M{"value": 1, "_id": 0}}
	if err := a.db.Collection("kvmeta").FindOne(ctx, b.M{"_id": "version"}, &findOpts).Decode(&result); err != nil {
		return false
	}
	return true
}

func init() {
	store.RegisterAdapter(&adapter{})
	ctx = context.Background()
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
