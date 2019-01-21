// +build rethinkdb

package rethinkdb

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"strconv"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	rdb "gopkg.in/rethinkdb/rethinkdb-go.v5"
)

// adapter holds RethinkDb connection data.
type adapter struct {
	conn    *rdb.Session
	dbName  string
	version int
}

const (
	defaultHost     = "localhost:28015"
	defaultDatabase = "tinode"

	dbVersion = 106

	adapterName = "rethinkdb"
)

// See https://godoc.org/github.com/rethinkdb/rethinkdb-go#ConnectOpts for explanations.
type configType struct {
	Database          string      `json:"database,omitempty"`
	Addresses         interface{} `json:"addresses,omitempty"`
	Username          string      `json:"username,omitempty"`
	Password          string      `json:"password,omitempty"`
	AuthKey           string      `json:"authkey,omitempty"`
	Timeout           int         `json:"timeout,omitempty"`
	WriteTimeout      int         `json:"write_timeout,omitempty"`
	ReadTimeout       int         `json:"read_timeout,omitempty"`
	KeepAlivePeriod   int         `json:"keep_alive_timeout,omitempty"`
	UseJSONNumber     bool        `json:"use_json_number,omitempty"`
	NumRetries        int         `json:"num_retries,omitempty"`
	InitialCap        int         `json:"initial_cap,omitempty"`
	MaxOpen           int         `json:"max_open,omitempty"`
	DiscoverHosts     bool        `json:"discover_hosts,omitempty"`
	HostDecayDuration int         `json:"host_decay_duration,omitempty"`
}

// TODO: convert hard-coded limits to config options
const (
	// Maximum number of records to return.
	maxResults = 1024
	// Maximum number of topic subscribers to return
	maxSubscribers = 256
)

// Open initializes rethinkdb session
func (a *adapter) Open(jsonconfig string) error {
	if a.conn != nil {
		return errors.New("adapter rethinkdb is already connected")
	}

	var err error
	var config configType

	if err = json.Unmarshal([]byte(jsonconfig), &config); err != nil {
		return errors.New("adapter rethinkdb failed to parse config: " + err.Error())
	}

	var opts rdb.ConnectOpts

	if config.Addresses == nil {
		opts.Address = defaultHost
	} else if host, ok := config.Addresses.(string); ok {
		opts.Address = host
	} else if hosts, ok := config.Addresses.([]string); ok {
		opts.Addresses = hosts
	} else {
		return errors.New("adapter rethinkdb failed to parse config.Addresses")
	}

	if config.Database == "" {
		a.dbName = defaultDatabase
	} else {
		a.dbName = config.Database
	}

	opts.Database = a.dbName
	opts.Username = config.Username
	opts.Password = config.Password
	opts.AuthKey = config.AuthKey
	opts.Timeout = time.Duration(config.Timeout) * time.Second
	opts.WriteTimeout = time.Duration(config.WriteTimeout) * time.Second
	opts.ReadTimeout = time.Duration(config.ReadTimeout) * time.Second
	opts.KeepAlivePeriod = time.Duration(config.KeepAlivePeriod) * time.Second
	opts.UseJSONNumber = config.UseJSONNumber
	opts.NumRetries = config.NumRetries
	opts.InitialCap = config.InitialCap
	opts.MaxOpen = config.MaxOpen
	opts.DiscoverHosts = config.DiscoverHosts
	opts.HostDecayDuration = time.Duration(config.HostDecayDuration) * time.Second

	a.conn, err = rdb.Connect(opts)
	if err != nil {
		return err
	}

	rdb.SetTags("json")
	a.version = -1

	return nil
}

// Close closes the underlying database connection
func (a *adapter) Close() error {
	var err error
	if a.conn != nil {
		// Close will wait for all outstanding requests to finish
		err = a.conn.Close()
		a.conn = nil
		a.version = -1
	}
	return err
}

// IsOpen returns true if connection to database has been established. It does not check if
// connection is actually live.
func (a *adapter) IsOpen() bool {
	return a.conn != nil
}

// Read current database version
func (a *adapter) getDbVersion() (int, error) {
	cursor, err := rdb.DB(a.dbName).Table("kvmeta").Get("version").Pluck("value").Run(a.conn)
	if err != nil {
		return -1, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return -1, nil
	}

	var vers map[string]int
	if err = cursor.One(&vers); err != nil {
		return -1, err
	}
	a.version = vers["value"]

	return a.version, nil
}

// CheckDbVersion checks whether the actual DB version matches the expected version of this adapter.
func (a *adapter) CheckDbVersion() error {
	if a.version <= 0 {
		a.getDbVersion()
	}

	if a.version != dbVersion {
		return errors.New("Invalid database version " + strconv.Itoa(a.version) +
			". Expected " + strconv.Itoa(dbVersion))
	}

	return nil
}

// GetName returns string that adapter uses to register itself with store.
func (a *adapter) GetName() string {
	return adapterName
}

// CreateDb initializes the storage. If reset is true, the database is first deleted losing all the data.
func (a *adapter) CreateDb(reset bool) error {

	// Drop database if exists, ignore error if it does not.
	if reset {
		rdb.DBDrop(a.dbName).RunWrite(a.conn)
	}

	if _, err := rdb.DBCreate(a.dbName).RunWrite(a.conn); err != nil {
		return err
	}

	// Table with metadata key-value pairs.
	if _, err := rdb.DB(a.dbName).TableCreate("kvmeta", rdb.TableCreateOpts{PrimaryKey: "key"}).RunWrite(a.conn); err != nil {
		return err
	}

	// Record current DB version.
	if _, err := rdb.DB(a.dbName).Table("kvmeta").Insert(
		map[string]interface{}{"key": "version", "value": dbVersion}).RunWrite(a.conn); err != nil {
		return err
	}

	// Users
	if _, err := rdb.DB(a.dbName).TableCreate("users", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index on User.DeletedAt for finding soft-deleted users
	if _, err := rdb.DB(a.dbName).Table("users").IndexCreate("DeletedAt").RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index on User.Tags array so user can be found by tags
	if _, err := rdb.DB(a.dbName).Table("users").IndexCreate("Tags", rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index for User.Devices.<hash>.DeviceId to ensure ID uniqueness across users
	if _, err := rdb.DB(a.dbName).Table("users").IndexCreateFunc("DeviceIds",
		func(row rdb.Term) interface{} {
			devices := row.Field("Devices")
			return devices.Keys().Map(func(key rdb.Term) interface{} {
				return devices.Field(key).Field("DeviceId")
			})
		}, rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}

	// User authentication records {unique, userid, secret}
	if _, err := rdb.DB(a.dbName).TableCreate("auth", rdb.TableCreateOpts{PrimaryKey: "unique"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Should be able to access user's auth records by user id
	if _, err := rdb.DB(a.dbName).Table("auth").IndexCreate("userid").RunWrite(a.conn); err != nil {
		return err
	}

	// Subscription to a topic. The primary key is a Topic:User string
	if _, err := rdb.DB(a.dbName).TableCreate("subscriptions", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB(a.dbName).Table("subscriptions").IndexCreate("User").RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB(a.dbName).Table("subscriptions").IndexCreate("Topic").RunWrite(a.conn); err != nil {
		return err
	}

	// Topics stored in database
	if _, err := rdb.DB(a.dbName).TableCreate("topics", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Secondary index on Owner field for deleting users.
	if _, err := rdb.DB(a.dbName).Table("topics").IndexCreate("Owner").RunWrite(a.conn); err != nil {
		return err
	}
	// Secondary index on Topic.Tags array so topics can be found by tags.
	// These tags are not unique as opposite to User.Tags.
	if _, err := rdb.DB(a.dbName).Table("topics").IndexCreate("Tags", rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}

	// Stored message
	if _, err := rdb.DB(a.dbName).TableCreate("messages", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Compound index of topic - seqID for selecting messages in a topic.
	if _, err := rdb.DB(a.dbName).Table("messages").IndexCreateFunc("Topic_SeqId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("SeqId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}
	// Compound index of hard-deleted messages
	if _, err := rdb.DB(a.dbName).Table("messages").IndexCreateFunc("Topic_DelId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("DelId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}
	// Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
	// [Topic, User1, DelId1], [Topic, User2, DelId2],...
	if _, err := rdb.DB(a.dbName).Table("messages").IndexCreateFunc("Topic_DeletedFor",
		func(row rdb.Term) interface{} {
			return row.Field("DeletedFor").Map(func(df rdb.Term) interface{} {
				return []interface{}{row.Field("Topic"), df.Field("User"), df.Field("DelId")}
			})
		}, rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}

	// Log of deleted messages
	if _, err := rdb.DB(a.dbName).TableCreate("dellog", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB(a.dbName).Table("dellog").IndexCreateFunc("Topic_DelId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("DelId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

	// User credentials - contact information such as "email:jdoe@example.com" or "tel:18003287448":
	// Id: "method:credential" like "email:jdoe@example.com". See types.Credential.
	if _, err := rdb.DB(a.dbName).TableCreate("credentials", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index on credentials.User to be able to query credentials by user id.
	if _, err := rdb.DB(a.dbName).Table("credentials").IndexCreate("User").RunWrite(a.conn); err != nil {
		return err
	}

	// Records of file uploads. See types.FileDef.
	if _, err := rdb.DB(a.dbName).TableCreate("fileuploads", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// A secondary index on fileuploads.User to be able to get records by user id.
	if _, err := rdb.DB(a.dbName).Table("fileuploads").IndexCreate("User").RunWrite(a.conn); err != nil {
		return err
	}
	// A secondary index on fileuploads.UseCount to be able to delete unused records at once.
	if _, err := rdb.DB(a.dbName).Table("fileuploads").IndexCreate("UseCount").RunWrite(a.conn); err != nil {
		return err
	}
	return nil
}

// UserCreate creates a new user. Returns error and true if error is due to duplicate user name,
// false for any other error
func (a *adapter) UserCreate(user *t.User) error {
	_, err := rdb.DB(a.dbName).Table("users").Insert(&user).RunWrite(a.conn)
	if err != nil {
		return err
	}

	return nil
}

// Add user's authentication record
func (a *adapter) AuthAddRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) (bool, error) {

	_, err := rdb.DB(a.dbName).Table("auth").Insert(
		map[string]interface{}{
			"unique":  unique,
			"userid":  uid.String(),
			"scheme":  scheme,
			"authLvl": authLvl,
			"secret":  secret,
			"expires": expires}).RunWrite(a.conn)
	if err != nil {
		if rdb.IsConflictErr(err) {
			return true, t.ErrDuplicate
		}
		return false, err
	}
	return false, nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(uid t.Uid, scheme string) error {
	_, err := rdb.DB(a.dbName).Table("auth").
		GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"scheme": scheme}).
		Delete().RunWrite(a.conn)
	return err
}

// AuthDelAllRecords deletes user's all authentication records
func (a *adapter) AuthDelAllRecords(uid t.Uid) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).Delete().RunWrite(a.conn)
	return res.Deleted, err
}

// Update user's authentication secret.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) (bool, error) {
	// The 'unique' is used as a primary key (no other way to ensure uniqueness in RethinkDB).
	// The primary key is immutable. If 'unique' has changed, we have to replace the old record with a new one:
	// 1. Check if 'unique' has changed.
	// 2. If not, execute update by 'unique'
	// 3. If yes, first insert the new record (it may fail due to dublicate 'unique') then delete the old one.
	var dupe bool
	// Get the old 'unique'
	cursor, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"scheme": scheme}).
		Pluck("unique").Default(nil).Run(a.conn)
	if err != nil {
		return dupe, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		// If the record is not found, don't update it
		return dupe, t.ErrNotFound
	}
	var record struct {
		Unique string `json:"unique"`
	}
	if err = cursor.One(&record); err != nil {
		return dupe, err
	}
	if record.Unique == unique {
		// Unique has not changed
		_, err = rdb.DB(a.dbName).Table("auth").Get(unique).Update(
			map[string]interface{}{
				"authLvl": authLvl,
				"secret":  secret,
				"expires": expires}).RunWrite(a.conn)
	} else {
		// Unique has changed. Insert-Delete.
		dupe, err = a.AuthAddRecord(uid, scheme, unique, authLvl, secret, expires)
		if err == nil {
			// We can't do much with the error here. No support for transactions :(
			a.AuthDelScheme(uid, unique)
		}
	}
	return dupe, err
}

// Retrieve user's authentication record
func (a *adapter) AuthGetRecord(uid t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	cursor, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"scheme": scheme}).
		Pluck("unique", "secret", "expires", "authLvl").Default(nil).Run(a.conn)
	if err != nil {
		return "", 0, nil, time.Time{}, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return "", 0, nil, time.Time{}, t.ErrNotFound
	}

	var record struct {
		Unique  string     `json:"unique"`
		AuthLvl auth.Level `json:"authLvl"`
		Secret  []byte     `json:"secret"`
		Expires time.Time  `json:"expires"`
	}

	if err = cursor.One(&record); err != nil {
		return "", 0, nil, time.Time{}, err
	}

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return record.Unique, record.AuthLvl, record.Secret, record.Expires, nil
}

// Retrieve user's authentication record
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	cursor, err := rdb.DB(a.dbName).Table("auth").Get(unique).Pluck(
		"userid", "secret", "expires", "authLvl").Default(nil).Run(a.conn)
	if err != nil {
		return t.ZeroUid, 0, nil, time.Time{}, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return t.ZeroUid, 0, nil, time.Time{}, nil
	}

	var record struct {
		Userid  string     `json:"userid"`
		AuthLvl auth.Level `json:"authLvl"`
		Secret  []byte     `json:"secret"`
		Expires time.Time  `json:"expires"`
	}

	if err = cursor.One(&record); err != nil {
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return t.ParseUid(record.Userid), record.AuthLvl, record.Secret, record.Expires, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *adapter) UserGet(uid t.Uid) (*t.User, error) {
	cursor, err := rdb.DB(a.dbName).Table("users").GetAll(uid.String()).
		Filter(rdb.Row.HasFields("DeletedAt").Not()).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return nil, nil
	}

	var user t.User
	if err = cursor.One(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = id.String()
	}

	users := []t.User{}
	if cursor, err := rdb.DB(a.dbName).Table("users").GetAll(uids...).
		Filter(rdb.Row.HasFields("DeletedAt").Not()).Run(a.conn); err == nil {
		defer cursor.Close()

		var user t.User
		for cursor.Next(&user) {
			users = append(users, user)
		}

		if err = cursor.Err(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return users, nil
}

func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	var err error
	if hard {
		// Delete user's subscriptions in all topics.
		if err = a.SubsDelForUser(uid, true); err != nil {
			return err
		}
		log.Println("Subscriptions deleted")
		// Can't delete user's messages in all topics because we cannot notify topics of such deletion.
		// Or we have to delete these messages one by one.
		// For now, just leave the messages there marked as sent by "not found" user.

		// Delete topics where the user is the owner:

		// 1. Delete dellog
		// 2. Decrement fileuploads.
		// 3. Delete all messages.
		// 4. Delete subscriptions.
		_, err = rdb.DB(a.dbName).Table("topics").GetAllByIndex("Owner", uid.String()).ForEach(
			func(topic rdb.Term) rdb.Term {
				return rdb.Expr([]interface{}{
					// Delete dellog
					rdb.DB(a.dbName).Table("dellog").Between(
						[]interface{}{topic.Field("Id"), rdb.MinVal},
						[]interface{}{topic.Field("Id"), rdb.MaxVal},
						rdb.BetweenOpts{Index: "Topic_DelId"}).Delete(),
					// Decrement fileuploads UseCounter
					rdb.DB(a.dbName).Table("fileuploads").GetAll(
						rdb.Args(
							rdb.DB(a.dbName).Table("messages").Between(
								[]interface{}{topic.Field("Id"), rdb.MinVal},
								[]interface{}{topic.Field("Id"), rdb.MaxVal},
								rdb.BetweenOpts{Index: "Topic_SeqId"}).
								// Fetch messages with attachments only
								Filter(func(msg rdb.Term) rdb.Term {
									return msg.HasFields("Attachments")
								}).
								// Flatten arrays
								ConcatMap(func(row rdb.Term) interface{} { return row.Field("Attachments") }).
								CoerceTo("array"))).
						Update(func(fu rdb.Term) interface{} {
							return map[string]interface{}{"UseCount": fu.Field("UseCount").Default(1).Sub(1)}
						}),
					// Delete messages
					rdb.DB(a.dbName).Table("messages").Between(
						[]interface{}{topic.Field("Id"), rdb.MinVal},
						[]interface{}{topic.Field("Id"), rdb.MaxVal},
						rdb.BetweenOpts{Index: "Topic_SeqId"}).Delete(),
					// Delete subscriptions
					rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic.Field("Id")).Delete(),
				})
			}).RunWrite(a.conn)

		log.Println("dellog, attachment, messages delete result:", err)

		// And finally delete the topics.
		// TODO: denormalize Owner into topic, add index on Owner.
		if _, err = rdb.DB(a.dbName).Table("topics").GetAllByIndex("Owner", uid.String()).
			Delete().RunWrite(a.conn); err != nil {
			return err
		}

		// Delete user's authentication records.
		if _, err = a.AuthDelAllRecords(uid); err != nil {
			return err
		}

		// Delete credentials.
		if err = a.CredDel(uid, ""); err != nil {
			return err
		}
		// And finally delete the user.
		_, err = rdb.DB(a.dbName).Table("users").Get(uid.String()).
			Delete().RunWrite(a.conn)
	} else {
		// Disable user's subscriptions.
		if err = a.SubsDelForUser(uid, false); err != nil {
			return err
		}

		// Disable subscriptions for topics where the user is the owner.
		// Disable topics where the user is the owner.
		now := t.TimeNow()
		disable := map[string]interface{}{"DeletedAt": now, "UpdatedAt": now}
		if _, err = rdb.DB(a.dbName).Table("topics").GetAllByIndex("Owner", uid.String()).ForEach(
			func(topic rdb.Term) rdb.Term {
				return rdb.Expr([]interface{}{
					rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic.Field("Id")).Update(disable),
					rdb.DB(a.dbName).Table("topics").Get(topic.Field("Id")).Update(disable),
				})
			}).RunWrite(a.conn); err != nil {
			return err
		}

		_, err = rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(disable).RunWrite(a.conn)
	}
	return err
}

// UserGetDisabled returns ID of users who were soft-deleted since specified time.
func (a *adapter) UserGetDisabled(since time.Time) ([]t.Uid, error) {
	cursor, err := rdb.DB(a.dbName).Table("users").
		Between(since, rdb.MaxVal, rdb.BetweenOpts{Index: "DeletedAt"}).Field("Id").Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var uids []t.Uid
	var userId string
	for cursor.Next(&userId) {
		uids = append(uids, t.ParseUid(userId))
	}

	if err = cursor.Err(); err != nil {
		return nil, err
	}

	return uids, nil
}

// UserUpdate updates user object.
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(update).RunWrite(a.conn)
	return err
}

// UserUpdateTags append or resets user's tags
func (a *adapter) UserUpdateTags(uid t.Uid, tags []string, reset bool) error {
	if reset {
		// Replace Tags with the new value
		return a.UserUpdate(uid, map[string]interface{}{"Tags": tags})
	}

	// Add tags to the list.
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(
		map[string]interface{}{"Tags": rdb.Row.Field("Tags").SetUnion(tags)}).RunWrite(a.conn)

	return err
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	cursor, err := rdb.DB(a.dbName).Table("credentials").Get(method + ":" + value).Pluck("User").Run(a.conn)
	if err != nil {
		return t.ZeroUid, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return t.ZeroUid, nil
	}

	var userId map[string]string
	if err = cursor.One(&userId); err != nil {
		return t.ZeroUid, err
	}

	return t.ParseUid(userId["User"]), nil
}

// *****************************

// TopicCreate creates a topic from template
func (a *adapter) TopicCreate(topic *t.Topic) error {
	_, err := rdb.DB(a.dbName).Table("topics").Insert(&topic).RunWrite(a.conn)
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	initiator.Id = initiator.Topic + ":" + initiator.User
	// Don't care if the initiator changes own subscription
	_, err := rdb.DB(a.dbName).Table("subscriptions").Insert(initiator, rdb.InsertOpts{Conflict: "replace"}).
		RunWrite(a.conn)
	if err != nil {
		return err
	}

	// If the second subscription exists, don't overwrite it. Just make sure it's not deleted.
	invited.Id = invited.Topic + ":" + invited.User
	_, err = rdb.DB(a.dbName).Table("subscriptions").Insert(invited, rdb.InsertOpts{Conflict: "error"}).
		RunWrite(a.conn)
	if err != nil {
		// Is this a duplicate subscription?
		if !rdb.IsConflictErr(err) {
			// It's a genuine DB error
			return err
		}
		// Undelete the second subsription if it exists: remove DeletedAt, update CreatedAt and UpdatedAt,
		// update ModeGiven.
		_, err = rdb.DB(a.dbName).Table("subscriptions").
			Get(invited.Id).Replace(
			rdb.Row.Without("DeletedAt").
				Merge(map[string]interface{}{
					"CreatedAt": invited.CreatedAt,
					"UpdatedAt": invited.UpdatedAt,
					"ModeGive":  invited.ModeGiven})).
			RunWrite(a.conn)
		if err != nil {
			return err
		}
	}

	topic := &t.Topic{ObjHeader: t.ObjHeader{Id: initiator.Topic}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	topic.TouchedAt = initiator.GetTouchedAt()
	return a.TopicCreate(topic)
}

func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	// Fetch topic by name
	cursor, err := rdb.DB(a.dbName).Table("topics").Get(topic).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return nil, nil
	}

	var tt = new(t.Topic)
	if err = cursor.One(tt); err != nil {
		return nil, err
	}

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' & 'fnd' subscriptions.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", uid.String())
	if !keepDeleted {
		// Filter out rows with defined DeletedAt
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	limit := maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			q = q.Filter(rdb.Row.Field("Topic").Eq(opts.Topic))
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q = q.Limit(limit)

	//log.Printf("RethinkDbAdapter.TopicsForUser q: %+v", q)
	cursor, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	var sub t.Subscription
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]interface{}, 0, 16)
	usrq := make([]interface{}, 0, 16)
	for cursor.Next(&sub) {
		tcat := t.GetTopicCat(sub.Topic)

		// 'me' or 'fnd' subscription, skip
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
	cursor.Close()

	var subs []t.Subscription
	if len(topq) > 0 || len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(join))
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		cursor, err = rdb.DB(a.dbName).Table("topics").GetAll(topq...).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for cursor.Next(&top) {
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
		cursor.Close()
	}

	// Fetch p2p users and join to p2p tables
	if len(usrq) > 0 {
		q = rdb.DB(a.dbName).Table("users").GetAll(usrq...)
		if !keepDeleted {
			q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
		}
		cursor, err = q.Run(a.conn)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for cursor.Next(&usr) {
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
		cursor.Close()
	}

	return subs, nil
}

// UsersForTopic loads users subscribed to the given topic
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch topic subscribers
	// Fetch all subscribed users. The number of users is not large
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if !keepDeleted {
		// Filter out rows with DeletedAt being not null
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	limit := maxSubscribers
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			q = q.Filter(rdb.Row.Field("User").Eq(opts.User.String()))
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q = q.Limit(limit)
	//log.Printf("RethinkDbAdapter.UsersForTopic q: %+v", q)
	cursor, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	join := make(map[string]t.Subscription)
	usrq := make([]interface{}, 0, 16)
	for cursor.Next(&sub) {
		join[sub.User] = sub
		usrq = append(usrq, sub.User)
	}
	cursor.Close()

	//log.Printf("RethinkDbAdapter.UsersForTopic usrq: %+v, usrq)
	if len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(usrq))

		// Fetch users by a list of subscriptions
		cursor, err = rdb.DB(a.dbName).Table("users").GetAll(usrq...).
			Filter(rdb.Row.HasFields("DeletedAt").Not()).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for cursor.Next(&usr) {
			if sub, ok := join[usr.Id]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetPublic(usr.Public)
				subs = append(subs, sub)
			}
		}
		cursor.Close()
		//log.Printf("RethinkDbAdapter.UsersForTopic users: %+v", subs)
	}

	if t.GetTopicCat(topic) == t.TopicCatP2P && len(subs) > 0 {
		// Swap public values of P2P topics as expected.
		if len(subs) == 1 {
			subs[0].SetPublic(nil)
		} else {
			pub := subs[0].GetPublic()
			subs[0].SetPublic(subs[1].GetPublic())
			subs[1].SetPublic(pub)
		}
	}

	return subs, nil
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid, opts *t.QueryOpt) ([]string, error) {
	cursor, err := rdb.DB(a.dbName).Table("topics").GetAllByIndex("Owner", uid.String()).
		Filter(rdb.Row.HasFields("DeletedAt").Not()).Field("Id").Run(a.conn)
	if err != nil {
		return nil, err
	}
	var names []string
	var name string
	for cursor.Next(&name) {
		names = append(names, name)
	}
	cursor.Close()
	return names, nil
}

func (a *adapter) TopicShare(shares []*t.Subscription) (int, error) {
	// Assign Ids.
	for i := 0; i < len(shares); i++ {
		shares[i].Id = shares[i].Topic + ":" + shares[i].User
	}

	// Subscription could have been marked as deleted (DeletedAt != nil). If it's marked
	// as deleted, unmark by clearing the DeletedAt field of the old subscription and
	// updating times and ModeGiven.
	resp, err := rdb.DB(a.dbName).Table("subscriptions").
		Insert(shares, rdb.InsertOpts{Conflict: func(id, oldsub, newsub rdb.Term) interface{} {
			return oldsub.Without("DeletedAt").Merge(map[string]interface{}{
				"CreatedAt": newsub.Field("CreatedAt"),
				"UpdatedAt": newsub.Field("UpdatedAt"),
				"ModeGive":  newsub.Field("ModeGiven")})
		}}).RunWrite(a.conn)

	return resp.Inserted + resp.Replaced, err
}

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

	q := rdb.DB(a.dbName).Table("topics").Get(topic)
	if hard {
		_, err = q.Delete().RunWrite(a.conn)
	} else {
		now := t.TimeNow()
		_, err = q.Update(map[string]interface{}{
			"UpdatedAt": now,
			"DeletedAt": now,
		}).RunWrite(a.conn)
	}
	return err
}

// TopicUpdateOnMessage deserializes message-related values into topic.
func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {

	update := struct {
		SeqId     int
		TouchedAt time.Time
	}{msg.SeqId, msg.CreatedAt}

	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).Update(update).RunWrite(a.conn)
	return err
}

func (a *adapter) TopicOwnerChange(topic string, newOwner, oldOwner t.Uid) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).
		Update(map[string]interface{}{"Owner": newOwner}).RunWrite(a.conn)
	return err
}

// SubscriptionGet returns a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {

	cursor, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return nil, nil
	}

	var sub t.Subscription
	if err = cursor.One(&sub); err != nil {
		return nil, err
	}

	if sub.DeletedAt != nil {
		return nil, nil
	}

	return &sub, nil
}

// Update time when the user was last attached to the topic
func (a *adapter) SubsLastSeen(topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic+":"+user.String()).
		Update(map[string]interface{}{"LastSeen": lastSeen}, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

// SubsForUser loads a list of user's subscriptions to topics. Does NOT load Public value.
func (a *adapter) SubsForUser(forUser t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", forUser.String())
	if !keepDeleted {
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	limit := maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			q = q.Filter(rdb.Row.Field("Topic").Eq(opts.Topic))
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q = q.Limit(limit)

	cursor, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var subs []t.Subscription
	var ss t.Subscription
	for cursor.Next(&ss) {
		subs = append(subs, ss)
	}

	return subs, cursor.Err()
}

// SubsForTopic fetches all subsciptions for a topic. Does NOT load Public value.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {

	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if !keepDeleted {
		// Filter out rows where DeletedAt is defined
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}

	limit := maxSubscribers
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			q = q.Filter(rdb.Row.Field("User").Eq(opts.User.String()))
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q = q.Limit(limit)
	//log.Println("Loading subscription q=", q)

	cursor, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var subs []t.Subscription
	var ss t.Subscription
	for cursor.Next(&ss) {
		subs = append(subs, ss)
		//log.Printf("SubsForTopic: loaded sub %#+v", ss)
	}

	return subs, cursor.Err()
}

// SubsUpdate updates a single subscription.
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	q := rdb.DB(a.dbName).Table("subscriptions")
	if !user.IsZero() {
		// Update one topic subscription
		q = q.Get(topic + ":" + user.String())
	} else {
		// Update all topic subscriptions
		q = q.GetAllByIndex("Topic", topic)
	}
	_, err := q.Update(update).RunWrite(a.conn)
	return err
}

// SubsDelete marks subscription as deleted.
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	_, err := rdb.DB(a.dbName).Table("subscriptions").
		Get(topic + ":" + user.String()).Update(map[string]interface{}{
		"UpdatedAt": now,
		"DeletedAt": now,
	}).RunWrite(a.conn)
	// _, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Delete().RunWrite(a.conn)
	return err
}

// SubsDelForTopic marks all subscriptions to the given topic as deleted
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	var err error
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if hard {
		_, err = q.Delete().RunWrite(a.conn)
	} else {
		now := t.TimeNow()
		_, err = q.Update(map[string]interface{}{
			"UpdatedAt": now,
			"DeletedAt": now,
		}).RunWrite(a.conn)
	}
	return err
}

// SubsDelForUser marks all subscriptions of a given user as deleted
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	var err error
	if hard {
	} else {
		now := t.TimeNow()
		update := map[string]interface{}{
			"UpdatedAt": now,
			"DeletedAt": now,
		}
		_, err = rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", user.String()).
			Update(update).RunWrite(a.conn)
	}
	return err
}

// Returns a list of users who match given tags, such as "email:jdoe@example.com" or "tel:18003287448".
// Searching the 'users.Tags' for the given tags using respective index.
func (a *adapter) FindUsers(uid t.Uid, req, opt []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var allTags []interface{}
	for _, tag := range append(req, opt...) {
		allTags = append(allTags, tag)
		index[tag] = struct{}{}
	}
	// Query for selecting matches where every group includes at least one required match (restricting search to
	// group members).
	/*
		r.db('tinode').
			table('users').
			getAll(<all tags here>, {index: "Tags"}).
			pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").
			group("Id").ungroup().
			map(function(row) { return row.getField('reduction').nth(0).merge({matchedCount: row.getField('reduction').count()}); }).
			filter(function(row) { return row.getField("Tags").setIntersection([<required tags here>]).count().ne(0); }).
			orderBy(r.desc('matchedCount')).
			limit(20);
	*/

	// Get users matched by tags, sort by number of matches from high to low.
	query := rdb.DB(a.dbName).
		Table("users").
		GetAllByIndex("Tags", allTags...).
		Filter(rdb.Row.HasFields("DeletedAt").Not()).
		Pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").
		Group("Id").
		Ungroup().
		Map(func(row rdb.Term) rdb.Term {
			return row.Field("reduction").
				Nth(0).
				Merge(map[string]interface{}{"MatchedTagsCount": row.Field("reduction").Count()})
		})

	if len(req) > 0 {
		var reqTags []interface{}
		for _, tag := range req {
			reqTags = append(reqTags, tag)
		}
		query = query.Filter(func(row rdb.Term) rdb.Term {
			return row.Field("Tags").SetIntersection(reqTags).Count().Ne(0)
		})
	}
	cursor, err := query.OrderBy(rdb.Desc("MatchedTagsCount")).Limit(maxResults).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var user t.User
	var sub t.Subscription
	var subs []t.Subscription
	for cursor.Next(&user) {
		if user.Id == uid.String() {
			// Skip the callee
			continue
		}
		sub.CreatedAt = user.CreatedAt
		sub.UpdatedAt = user.UpdatedAt
		sub.User = user.Id
		sub.SetPublic(user.Public)
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

	if err = cursor.Err(); err != nil {
		return nil, err
	}

	return subs, nil

}

// Returns a list of topics with matching tags.
// Searching the 'topics.Tags' for the given tags using respective index.
func (a *adapter) FindTopics(req, opt []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var allTags []interface{}
	for _, tag := range append(req, opt...) {
		allTags = append(allTags, tag)
		index[tag] = struct{}{}
	}
	query := rdb.DB(a.dbName).
		Table("topics").
		GetAllByIndex("Tags", allTags...).
		Filter(rdb.Row.HasFields("DeletedAt").Not()).
		Pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").
		Group("Id").
		Ungroup().
		Map(func(row rdb.Term) rdb.Term {
			return row.Field("reduction").
				Nth(0).
				Merge(map[string]interface{}{"MatchedTagsCount": row.Field("reduction").Count()})
		})

	if len(req) > 0 {
		var reqTags []interface{}
		for _, tag := range req {
			reqTags = append(reqTags, tag)
		}
		query = query.Filter(func(row rdb.Term) rdb.Term {
			return row.Field("Tags").SetIntersection(reqTags).Count().Ne(0)
		})
	}

	cursor, err := query.OrderBy(rdb.Desc("MatchedTagsCount")).Limit(maxResults).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var topic t.Topic
	var sub t.Subscription
	var subs []t.Subscription
	for cursor.Next(&topic) {
		sub.CreatedAt = topic.CreatedAt
		sub.UpdatedAt = topic.UpdatedAt
		sub.Topic = topic.Id
		sub.SetPublic(topic.Public)
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

	if err = cursor.Err(); err != nil {
		return nil, err
	}
	return subs, nil

}

// Messages
func (a *adapter) MessageSave(msg *t.Message) error {
	msg.SetUid(store.GetUid())
	_, err := rdb.DB(a.dbName).Table("messages").Insert(msg).RunWrite(a.conn)
	return err
}

func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {

	var limit = maxResults
	var lower, upper interface{}

	upper = rdb.MaxVal
	lower = rdb.MinVal

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

	lower = []interface{}{topic, lower}
	upper = []interface{}{topic, upper}

	requester := forUser.String()
	cursor, err := rdb.DB(a.dbName).Table("messages").
		Between(lower, upper, rdb.BetweenOpts{Index: "Topic_SeqId"}).
		// Ordering by index must come before filtering
		OrderBy(rdb.OrderByOpts{Index: rdb.Desc("Topic_SeqId")}).
		// Skip hard-deleted messages
		Filter(rdb.Row.HasFields("DelId").Not()).
		// Skip messages soft-deleted for the current user
		Filter(func(row rdb.Term) interface{} {
			return rdb.Not(row.Field("DeletedFor").Default([]interface{}{}).Contains(
				func(df rdb.Term) interface{} {
					return df.Field("User").Eq(requester)
				}))
		}).Limit(limit).Run(a.conn)

	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var msgs []t.Message
	if err = cursor.All(&msgs); err != nil {
		return nil, err
	}

	return msgs, nil
}

// Get ranges of deleted messages
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.DelMessage, error) {
	var limit = maxResults
	var lower, upper interface{}

	upper = rdb.MaxVal
	lower = rdb.MinVal

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

	// Fetch log of deletions
	cursor, err := rdb.DB(a.dbName).Table("dellog").
		// Select log entries for the given table and DelId values between two limits
		Between([]interface{}{topic, lower}, []interface{}{topic, upper},
			rdb.BetweenOpts{Index: "Topic_DelId"}).
		// Sort from low DelIds to high
		OrderBy(rdb.OrderByOpts{Index: "Topic_DelId"}).
		// Keep entries soft-deleted for the current user and all hard-deleted entries.
		Filter(func(row rdb.Term) interface{} {
			return row.Field("DeletedFor").Eq(forUser.String()).Or(row.Field("DeletedFor").Eq(""))
		}).
		Limit(limit).Run(a.conn)

	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var dmsgs []t.DelMessage
	if err = cursor.All(&dmsgs); err != nil {
		return nil, err
	}

	return dmsgs, nil
}

func (a *adapter) messagesHardDelete(topic string) error {
	var err error

	// TODO: handle file uploads

	if _, err = rdb.DB(a.dbName).Table("dellog").Between(
		[]interface{}{topic, rdb.MinVal},
		[]interface{}{topic, rdb.MaxVal},
		rdb.BetweenOpts{Index: "Topic_DelId"}).Delete().RunWrite(a.conn); err != nil {
		return err
	}

	q := rdb.DB(a.dbName).Table("messages").Between(
		[]interface{}{topic, rdb.MinVal},
		[]interface{}{topic, rdb.MaxVal},
		rdb.BetweenOpts{Index: "Topic_SeqId"})

	if err = a.fileDecrementUseCounter(q); err != nil {
		return err
	}

	_, err = q.Delete().RunWrite(a.conn)

	return err
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	var indexVals []interface{}
	var err error

	if toDel == nil {
		err = a.messagesHardDelete(topic)
	} else {
		// Only some messages are being deleted
		toDel.SetUid(store.GetUid())

		// Start with making a log entry
		_, err = rdb.DB(a.dbName).Table("dellog").Insert(toDel).RunWrite(a.conn)
		if err != nil {
			return err
		}

		query := rdb.DB(a.dbName).Table("messages")
		if len(toDel.SeqIdRanges) > 1 || toDel.SeqIdRanges[0].Hi <= toDel.SeqIdRanges[0].Low {
			for _, rng := range toDel.SeqIdRanges {
				if rng.Hi == 0 {
					indexVals = append(indexVals, []interface{}{topic, rng.Low})
				} else {
					for i := rng.Low; i <= rng.Hi; i++ {
						indexVals = append(indexVals, []interface{}{topic, i})
					}
				}
			}
			query = query.GetAllByIndex("Topic_SeqId", indexVals...)
		} else {
			// Optimizing for a special case of single range low..hi
			query = query.Between(
				[]interface{}{topic, toDel.SeqIdRanges[0].Low},
				[]interface{}{topic, toDel.SeqIdRanges[0].Hi},
				rdb.BetweenOpts{Index: "Topic_SeqId", RightBound: "closed"})
		}
		// Skip already hard-deleted messages.
		query = query.Filter(rdb.Row.HasFields("DelId").Not())
		if toDel.DeletedFor == "" {
			// First decrement use counter for attachments.
			if err = a.fileDecrementUseCounter(query); err == nil {
				// Hard-delete individual messages. Message is not deleted but all fields with content
				// are replaced with nulls.
				_, err = query.Update(map[string]interface{}{
					"DeletedAt": t.TimeNow(), "DelId": toDel.DelId, "From": nil,
					"Head": nil, "Content": nil, "Attachments": nil}).RunWrite(a.conn)
			}

		} else {
			// Soft-deleting: adding DelId to DeletedFor
			_, err = query.
				// Skip messages already soft-deleted for the current user
				Filter(func(row rdb.Term) interface{} {
					return rdb.Not(row.Field("DeletedFor").Default([]interface{}{}).Contains(
						func(df rdb.Term) interface{} {
							return df.Field("User").Eq(toDel.DeletedFor)
						}))
				}).Update(map[string]interface{}{"DeletedFor": rdb.Row.Field("DeletedFor").
				Default([]interface{}{}).Append(
				&t.SoftDelete{
					User:  toDel.DeletedFor,
					DelId: toDel.DelId})}).RunWrite(a.conn)
		}

		// If operation has failed, remove dellog record.
		if err != nil {
			rdb.DB(a.dbName).Table("dellog").Get(toDel.Id).
				Delete(rdb.DeleteOpts{Durability: "soft", ReturnChanges: false}).RunWrite(a.conn)
		}
	}

	return err
}

// MessageAttachments adds attachments to a message.
func (a *adapter) MessageAttachments(msgId t.Uid, fids []string) error {
	now := t.TimeNow()
	_, err := rdb.DB(a.dbName).Table("messages").Get(msgId.String()).
		Update(map[string]interface{}{
			"UpdatedAt":   now,
			"Attachments": fids,
		}).RunWrite(a.conn)
	if err != nil {
		return err
	}

	ids := make([]interface{}, len(fids))
	for i, id := range fids {
		ids[i] = id
	}
	_, err = rdb.DB(a.dbName).Table("fileuploads").GetAll(ids...).
		Update(map[string]interface{}{
			"UpdatedAt": now,
			"UseCount":  rdb.Row.Field("UseCount").Default(0).Add(1),
		}).RunWrite(a.conn)

	return err
}

func deviceHasher(deviceID string) string {
	// Generate custom key as [64-bit hash of device id] to ensure predictable
	// length of the key
	hasher := fnv.New64()
	hasher.Write([]byte(deviceID))
	return strconv.FormatUint(uint64(hasher.Sum64()), 16)
}

// Device management for push notifications
func (a *adapter) DeviceUpsert(uid t.Uid, def *t.DeviceDef) error {
	hash := deviceHasher(def.DeviceId)
	user := uid.String()

	// Ensure uniqueness of the device ID
	var others []interface{}
	// Find users who already use this device ID, ignore current user.
	cursor, err := rdb.DB(a.dbName).Table("users").GetAllByIndex("DeviceIds", def.DeviceId).
		// We only care about user Ids
		Pluck("Id").
		// Make sure we filter out the current user who may legitimately use this device ID
		Filter(rdb.Not(rdb.Row.Field("Id").Eq(user))).
		// Convert slice of objects to a slice of strings
		ConcatMap(func(row rdb.Term) interface{} { return []interface{}{row.Field("Id")} }).
		// Execute
		Run(a.conn)
	if err != nil {
		return err
	}
	defer cursor.Close()

	if err = cursor.All(&others); err != nil {
		return err
	}

	if len(others) > 0 {
		// Delete device ID for the other users.
		_, err = rdb.DB(a.dbName).Table("users").GetAll(others...).Replace(rdb.Row.Without(
			map[string]string{"Devices": hash})).RunWrite(a.conn)
		if err != nil {
			return err
		}
	}

	// Actually add/update DeviceId for the new user
	_, err = rdb.DB(a.dbName).Table("users").Get(user).
		Update(map[string]interface{}{
			"Devices": map[string]*t.DeviceDef{
				hash: def,
			}}).RunWrite(a.conn)
	return err
}

func (a *adapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	ids := make([]interface{}, len(uids))
	for i, id := range uids {
		ids[i] = id.String()
	}

	// {Id: "userid", Devices: {"hash1": {..def1..}, "hash2": {..def2..}}
	cursor, err := rdb.DB(a.dbName).Table("users").GetAll(ids...).Pluck("Id", "Devices").
		Default(nil).Limit(maxResults).Run(a.conn)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close()

	var row struct {
		Id      string
		Devices map[string]*t.DeviceDef
	}

	result := make(map[t.Uid][]t.DeviceDef)
	count := 0
	var uid t.Uid
	for cursor.Next(&row) {
		if row.Devices != nil && len(row.Devices) > 0 {
			if err := uid.UnmarshalText([]byte(row.Id)); err != nil {
				continue
			}

			result[uid] = make([]t.DeviceDef, len(row.Devices))
			i := 0
			for _, def := range row.Devices {
				if def != nil {
					result[uid][i] = *def
					i++
					count++
				}
			}
		}
	}

	return result, count, cursor.Err()
}

func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	var err error
	q := rdb.DB(a.dbName).Table("users").Get(uid.String())
	if deviceID == "" {
		q = q.Update(map[string]interface{}{"Devices": nil})
	} else {
		q = q.Replace(rdb.Row.Without(map[string]string{"Devices": deviceHasher(deviceID)}))
	}
	_, err = q.RunWrite(a.conn)
	return err
}

// Credential management
func (a *adapter) CredAdd(cred *t.Credential) error {
	// log.Println("saving credential", cred)

	cred.Id = cred.Method + ":" + cred.Value
	if !cred.Done {
		// If credential is not confirmed, it should not block others
		// from attempting to validate it.
		cred.Id = cred.User + ":" + cred.Id
	}

	_, err := rdb.DB(a.dbName).Table("credentials").Insert(cred).RunWrite(a.conn)
	if rdb.IsConflictErr(err) {
		return t.ErrDuplicate
	}
	return err
}

func (a *adapter) CredIsConfirmed(uid t.Uid, method string) (bool, error) {
	creds, err := a.CredGet(uid, method)
	if err != nil {
		return false, err
	}

	if len(creds) > 0 {
		return creds[0].Done, nil
	}
	return false, nil
}

func (a *adapter) CredDel(uid t.Uid, method string) error {
	q := rdb.DB(a.dbName).Table("credentials").
		GetAllByIndex("User", uid.String())
	if method != "" {
		q = q.Filter(map[string]interface{}{"Method": method})
	}
	_, err := q.Delete().RunWrite(a.conn)
	return err
}

func (a *adapter) CredConfirm(uid t.Uid, method string) error {
	// RethinkDb does not allow primary key to be changed (userid:method:value -> method:value)
	// We have to delete and re-insert with a different primary key.
	creds, err := a.CredGet(uid, method)
	if err != nil {
		return err
	}
	if len(creds) == 0 {
		return t.ErrNotFound
	}
	if creds[0].Done {
		// Already confirmed
		return nil
	}

	creds[0].Done = true
	creds[0].UpdatedAt = t.TimeNow()
	if err = a.CredAdd(creds[0]); err != nil {
		if rdb.IsConflictErr(err) {
			return t.ErrDuplicate
		}
		return err
	}

	rdb.DB(a.dbName).
		Table("credentials").
		Get(uid.String() + ":" + creds[0].Method + ":" + creds[0].Value).
		Delete(rdb.DeleteOpts{Durability: "soft", ReturnChanges: false}).
		RunWrite(a.conn)

	return nil
}

func (a *adapter) CredFail(uid t.Uid, method string) error {
	_, err := rdb.DB(a.dbName).Table("credentials").
		GetAllByIndex("User", uid.String()).
		Filter(map[string]interface{}{"Method": method}).
		Update(map[string]interface{}{
			"Retries":   rdb.Row.Field("Retries").Default(0).Add(1),
			"UpdatedAt": t.TimeNow(),
		}).RunWrite(a.conn)
	return err
}

func (a *adapter) CredGet(uid t.Uid, method string) ([]*t.Credential, error) {
	q := rdb.DB(a.dbName).Table("credentials").
		GetAllByIndex("User", uid.String())
	if method != "" {
		q = q.Filter(map[string]interface{}{"Method": method})
	}
	cursor, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return nil, nil
	}

	var result []*t.Credential
	if err = cursor.All(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// FileUploads

// FileStartUpload initializes a file upload
func (a *adapter) FileStartUpload(fd *t.FileDef) error {
	_, err := rdb.DB(a.dbName).Table("fileuploads").Insert(fd).RunWrite(a.conn)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise
func (a *adapter) FileFinishUpload(fid string, status int, size int64) (*t.FileDef, error) {
	if _, err := rdb.DB(a.dbName).Table("fileuploads").Get(fid).
		Update(map[string]interface{}{
			"UpdatedAt": t.TimeNow(),
			"Status":    status,
			"Size":      size,
		}).RunWrite(a.conn); err != nil {

		return nil, err
	}
	return a.FileGet(fid)
}

// FileGet fetches a record of a specific file
func (a *adapter) FileGet(fid string) (*t.FileDef, error) {
	cursor, err := rdb.DB(a.dbName).Table("fileuploads").Get(fid).Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	if cursor.IsNil() {
		return nil, nil
	}

	var fd t.FileDef
	if err = cursor.One(&fd); err != nil {
		return nil, err
	}

	return &fd, nil

}

// FileDeleteUnused deletes orphaned file uploads.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	q := rdb.DB(a.dbName).Table("fileuploads").GetAllByIndex("UseCount", 0)
	if !olderThan.IsZero() {
		q = q.Filter(rdb.Row.Field("UpdatedAt").Lt(olderThan))
	}
	if limit > 0 {
		q = q.Limit(limit)
	}

	cursor, err := q.Pluck("Location").Run(a.conn)
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	var locations []string
	var loc map[string]string
	for cursor.Next(&loc) {
		locations = append(locations, loc["Location"])
	}

	if err = cursor.Err(); err != nil {
		return nil, err
	}

	_, err = q.Delete().RunWrite(a.conn)

	return locations, err
}

// Given a select query against 'messages' table, decrement corresponding use counter in 'fileuploads' table.
func (a *adapter) fileDecrementUseCounter(msgQuery rdb.Term) error {
	/*
		r.db("test").table("one")
			.getAll(
				r.args(r.db("test").table("zero")
					.getAll(
						"07e2c6fe-ac91-49cb-9834-ff34bf50aad1",
			  			"0098a829-6da5-4f7b-8432-32b40de9ab3b",
						"0926e7dd-321a-49cb-adb1-7a705d9d9a78",
						"8e195450-babd-4954-a8fb-0cc414b43156")
					.filter(r.row.hasFields("att"))
					.concatMap(function(row) { return row.getField("att"); })
					.coerceTo("array"))
				)
			.update({useCount: r.row.getField("useCount").default(0).add(1)})
	*/
	_, err := rdb.DB(a.dbName).Table("fileuploads").GetAll(
		rdb.Args(
			msgQuery.
				// Fetch messages with attachments only
				Filter(rdb.Row.HasFields("Attachments")).
				// Flatten arrays
				ConcatMap(func(row rdb.Term) interface{} { return row.Field("Attachments") }).
				CoerceTo("array"))).
		// Decrement UseCount.
		Update(map[string]interface{}{"UseCount": rdb.Row.Field("UseCount").Default(1).Sub(1)}).
		RunWrite(a.conn)
	return err
}

func init() {
	store.RegisterAdapter(adapterName, &adapter{})
}
