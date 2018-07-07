// +build rethinkdb

package rethinkdb

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	rdb "gopkg.in/gorethink/gorethink.v4"
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

	dbVersion = 105

	adapterName = "rethinkdb"
)

type configType struct {
	Database            string      `json:"database,omitempty"`
	Addresses           interface{} `json:"addresses,omitempty"`
	AuthKey             string      `json:"authkey,omitempty"`
	Timeout             int         `json:"timeout,omitempty"`
	WriteTimeout        int         `json:"write_timeout,omitempty"`
	ReadTimeout         int         `json:"read_timeout,omitempty"`
	MaxIdle             int         `json:"max_idle,omitempty"`
	MaxOpen             int         `json:"max_open,omitempty"`
	DiscoverHosts       bool        `json:"discover_hosts,omitempty"`
	NodeRefreshInterval int         `json:"node_refresh_interval,omitempty"`
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
	opts.AuthKey = config.AuthKey
	opts.Timeout = time.Duration(config.Timeout) * time.Second
	opts.WriteTimeout = time.Duration(config.WriteTimeout) * time.Second
	opts.ReadTimeout = time.Duration(config.ReadTimeout) * time.Second
	opts.MaxIdle = config.MaxIdle
	opts.MaxOpen = config.MaxOpen
	opts.DiscoverHosts = config.DiscoverHosts
	opts.NodeRefreshInterval = time.Duration(config.NodeRefreshInterval) * time.Second

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
	resp, err := rdb.DB(a.dbName).Table("kvmeta").Get("version").Pluck("value").Run(a.conn)
	if err != nil || resp.IsNil() {
		return -1, err
	}

	var vers map[string]int
	if err = resp.One(&vers); err != nil {
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

	// Topic stored in database
	if _, err := rdb.DB(a.dbName).TableCreate("topics", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index on Topic.Tags array so topics can be found by tags
	// These tags are not unique as opposite to User.Tags.
	if _, err := rdb.DB(a.dbName).Table("topics").IndexCreate("Tags", rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}

	// Stored message
	if _, err := rdb.DB(a.dbName).TableCreate("messages", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
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

// Delete user's authentication record.
func (a *adapter) AuthDelRecord(uid t.Uid, unique string) error {
	_, err := rdb.DB(a.dbName).Table("auth").
		GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"unique": unique}).
		Delete().RunWrite(a.conn)
	return err
}

// Delete user's all authentication records
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
	res, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"scheme": scheme}).
		Pluck("unique").Default(nil).Run(a.conn)
	if err != nil {
		return dupe, err
	}
	if res.IsNil() {
		// If the record is not found, don't update it
		return dupe, t.ErrNotFound
	}
	var record struct {
		Unique string `gorethink:"unique"`
	}
	if err = res.One(&record); err != nil {
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
			a.AuthDelRecord(uid, unique)
		}
	}
	return dupe, err
}

// Retrieve user's authentication record
func (a *adapter) AuthGetRecord(uid t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	row, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).
		Filter(map[string]interface{}{"scheme": scheme}).
		Pluck("unique", "secret", "expires", "authLvl").Default(nil).Run(a.conn)
	if err != nil || row.IsNil() {
		return "", 0, nil, time.Time{}, err
	}

	var record struct {
		Unique  string     `gorethink:"unique"`
		AuthLvl auth.Level `gorethink:"authLvl"`
		Secret  []byte     `gorethink:"secret"`
		Expires time.Time  `gorethink:"expires"`
	}

	if err = row.One(&record); err != nil {
		return "", 0, nil, time.Time{}, err
	}

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return record.Unique, record.AuthLvl, record.Secret, record.Expires, nil
}

// Retrieve user's authentication record
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	row, err := rdb.DB(a.dbName).Table("auth").Get(unique).Pluck(
		"userid", "secret", "expires", "authLvl").Default(nil).Run(a.conn)
	if err != nil || row.IsNil() {
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	var record struct {
		Userid  string     `gorethink:"userid"`
		AuthLvl auth.Level `gorethink:"authLvl"`
		Secret  []byte     `gorethink:"secret"`
		Expires time.Time  `gorethink:"expires"`
	}

	if err = row.One(&record); err != nil {
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return t.ParseUid(record.Userid), record.AuthLvl, record.Secret, record.Expires, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *adapter) UserGet(uid t.Uid) (*t.User, error) {
	row, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Run(a.conn)
	if err != nil || row.IsNil() {
		return nil, err
	}

	var user t.User
	if err = row.One(&user); err != nil {
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
	if rows, err := rdb.DB(a.dbName).Table("users").GetAll(uids...).Run(a.conn); err == nil {
		var user t.User
		for rows.Next(&user) {
			users = append(users, user)
		}

		if err = rows.Err(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return users, nil
}

func (a *adapter) UserDelete(uid t.Uid, soft bool) error {
	var err error
	q := rdb.DB(a.dbName).Table("users").Get(uid.String())
	if soft {
		now := t.TimeNow()
		_, err = q.Update(map[string]interface{}{"DeletedAt": now, "UpdatedAt": now}).RunWrite(a.conn)
	} else {
		_, err = q.Delete().RunWrite(a.conn)
	}
	return err
}

// UserUpdate updates user object.
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(update).RunWrite(a.conn)
	return err
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
	row, err := rdb.DB(a.dbName).Table("topics").Get(topic).Run(a.conn)
	if err != nil || row.IsNil() {
		return nil, err
	}

	var tt = new(t.Topic)
	if err = row.One(tt); err != nil {
		return nil, err
	}

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' subscription.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	// Subscription have Topic.UpdatedAt denormalized into Subscription.UpdatedAt
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
	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	var sub t.Subscription
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]interface{}, 0, 16)
	usrq := make([]interface{}, 0, 16)
	for rows.Next(&sub) {
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

	//log.Printf("RethinkDbAdapter.TopicsForUser topq, usrq: %+v, %+v", topq, usrq)
	var subs []t.Subscription
	if len(topq) > 0 || len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(join))
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		rows, err = rdb.DB(a.dbName).Table("topics").GetAll(topq...).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for rows.Next(&top) {
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

		//log.Printf("RethinkDbAdapter.TopicsForUser 1: %#+v", subs)
	}

	// Fetch p2p users and join to p2p tables
	if len(usrq) > 0 {
		rows, err = rdb.DB(a.dbName).Table("users").GetAll(usrq...).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for rows.Next(&usr) {
			uid2 := t.ParseUid(usr.Id)
			topic := uid.P2PName(uid2)
			if sub, ok := join[topic]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetPublic(usr.Public)
				sub.SetWith(uid2.UserId())
				sub.SetDefaultAccess(usr.Access.Auth, usr.Access.Anon)
				sub.SetLastSeenAndUA(usr.LastSeen, usr.UserAgent)
				subs = append(subs, sub)
			}
		}

		//log.Printf("RethinkDbAdapter.TopicsForUser 2: %#+v", subs)
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
	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	join := make(map[string]t.Subscription)
	usrq := make([]interface{}, 0, 16)
	for rows.Next(&sub) {
		join[sub.User] = sub
		usrq = append(usrq, sub.User)
	}

	//log.Printf("RethinkDbAdapter.UsersForTopic usrq: %+v, usrq)
	if len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(usrq))

		// Fetch users by a list of subscriptions
		rows, err = rdb.DB(a.dbName).Table("users").GetAll(usrq...).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for rows.Next(&usr) {
			if sub, ok := join[usr.Id]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetPublic(usr.Public)
				subs = append(subs, sub)
			}
		}
		//log.Printf("RethinkDbAdapter.UsersForTopic users: %+v", subs)
	}

	return subs, nil
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

func (a *adapter) TopicDelete(topic string) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).Delete().RunWrite(a.conn)
	return err
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {

	update := struct {
		SeqId     int
		TouchedAt time.Time
	}{msg.SeqId, msg.CreatedAt}

	// FIXME(gene): remove 'me' update; no longer relevant
	var err error
	if strings.HasPrefix(topic, "usr") {
		// Topic is passed as usrABCD, but the 'users' table expects Id without the 'usr' prefix.
		user := t.ParseUserId(topic).String()
		_, err = rdb.DB(a.dbName).Table("users").Get(user).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

		// All other messages
	} else {
		_, err = rdb.DB(a.dbName).Table("topics").Get(topic).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)
	}

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).Update(update).RunWrite(a.conn)
	return err
}

// Get a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {

	row, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Run(a.conn)
	if err != nil || row.IsNil() {
		return nil, err
	}

	var sub t.Subscription
	if err = row.One(&sub); err != nil {
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

	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next(&ss) {
		subs = append(subs, ss)
	}

	return subs, rows.Err()
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

	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next(&ss) {
		subs = append(subs, ss)
		//log.Printf("SubsForTopic: loaded sub %#+v", ss)
	}

	return subs, rows.Err()
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
func (a *adapter) SubsDelForTopic(topic string) error {
	now := t.TimeNow()
	update := map[string]interface{}{
		"UpdatedAt": now,
		"DeletedAt": now,
	}
	_, err := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic).
		Update(update).RunWrite(a.conn)
	return err
}

// SubsDelForUser marks all subscriptions of a given user as deleted
func (a *adapter) SubsDelForUser(user t.Uid) error {
	now := t.TimeNow()
	update := map[string]interface{}{
		"UpdatedAt": now,
		"DeletedAt": now,
	}
	_, err := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", user.String()).
		Update(update).RunWrite(a.conn)
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
	rows, err := query.
		OrderBy(rdb.Desc("MatchedTagsCount")).
		Limit(maxResults).
		Run(a.conn)

	if err != nil {
		return nil, err
	}

	var user t.User
	var sub t.Subscription
	var subs []t.Subscription
	for rows.Next(&user) {
		if user.Id == uid.String() {
			// Skip the callee
			continue
		}
		sub.CreatedAt = user.CreatedAt
		sub.UpdatedAt = user.UpdatedAt
		sub.User = user.Id
		sub.SetPublic(user.Public)
		// TODO: maybe report default access to user
		// sub.SetDefaultAccess(user.Access.Auth, user.Access.Anon)
		tags := make([]string, 0, 1)
		for _, tag := range user.Tags {
			if _, ok := index[tag]; ok {
				tags = append(tags, tag)
			}
		}
		sub.Private = tags
		subs = append(subs, sub)
	}

	if err = rows.Err(); err != nil {
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

	rows, err := query.
		OrderBy(rdb.Desc("MatchedTagsCount")).
		Limit(maxResults).
		Run(a.conn)
	if err != nil {
		return nil, err
	}

	var topic t.Topic
	var sub t.Subscription
	var subs []t.Subscription
	for rows.Next(&topic) {
		sub.CreatedAt = topic.CreatedAt
		sub.UpdatedAt = topic.UpdatedAt
		sub.Topic = topic.Id
		sub.SetPublic(topic.Public)
		// TODO: maybe report default access to user
		// sub.SetDefaultAccess(user.Access.Auth, user.Access.Anon)
		tags := make([]string, 0, 1)
		for _, tag := range topic.Tags {
			if _, ok := index[tag]; ok {
				tags = append(tags, tag)
			}
		}
		sub.Private = tags
		subs = append(subs, sub)
	}

	if err = rows.Err(); err != nil {
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
	rows, err := rdb.DB(a.dbName).Table("messages").
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

	var msgs []t.Message
	if err = rows.All(&msgs); err != nil {
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
	rows, err := rdb.DB(a.dbName).Table("dellog").
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

	var dmsgs []t.DelMessage
	if err = rows.All(&dmsgs); err != nil {
		return nil, err
	}

	return dmsgs, nil
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) error {
	var indexVals []interface{}
	var err error

	query := rdb.DB(a.dbName).Table("messages")
	if toDel == nil {
		// Whole topic is being deleted, thus also deleting all messages
		_, err = query.Between(
			[]interface{}{topic, rdb.MinVal},
			[]interface{}{topic, rdb.MaxVal},
			rdb.BetweenOpts{Index: "Topic_SeqId"}).Delete().RunWrite(a.conn)
	} else {
		// Only some messages are being deleted
		toDel.SetUid(store.GetUid())

		// Start with making a log entry
		_, err = rdb.DB(a.dbName).Table("dellog").Insert(toDel).RunWrite(a.conn)
		if err != nil {
			return err
		}

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

			rdb.DB(a.dbName).Table("fileuploads").GetAll(
				rdb.Args(
					query.
						// Fetch messages with attachments only
						Filter(rdb.Row.HasFields("Attachments")).
						// Flatten arrays
						ConcatMap(func(row rdb.Term) interface{} { return row.Field("Attachments") }).
						CoerceTo("array"))).
				// Decrement UseCount.
				Update(map[string]interface{}{"UseCount": rdb.Row.Field("UseCount").Default(0).Sub(1)}).
				RunWrite(a.conn)
			// Hard-delete individual messages. Message is not deleted but all fields with content
			// are replaced with nulls.
			_, err = query.Update(map[string]interface{}{
				"DeletedAt": t.TimeNow(), "DelId": toDel.DelId, "From": nil,
				"Head": nil, "Content": nil, "Attachments": nil}).RunWrite(a.conn)

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
	}

	if err != nil && toDel != nil {
		rdb.DB(a.dbName).Table("dellog").Get(toDel.Id).
			Delete(rdb.DeleteOpts{Durability: "soft", ReturnChanges: false}).RunWrite(a.conn)
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
	if resp, err := rdb.DB(a.dbName).Table("users").GetAllByIndex("DeviceIds", def.DeviceId).
		// We only care about user Ids
		Pluck("Id").
		// Make sure we filter out the current user who may legitimately use this device ID
		Filter(rdb.Not(rdb.Row.Field("Id").Eq(user))).
		// Convert slice of objects to a slice of strings
		ConcatMap(func(row rdb.Term) interface{} { return []interface{}{row.Field("Id")} }).
		// Execute
		Run(a.conn); err != nil {
		return err
	} else if err = resp.All(&others); err != nil {
		return err
	} else if len(others) > 0 {
		// Delete device ID for the other users.
		_, err := rdb.DB(a.dbName).Table("users").GetAll(others...).Replace(rdb.Row.Without(
			map[string]string{"Devices": hash})).RunWrite(a.conn)
		if err != nil {
			return err
		}
	}

	// Actually add/update DeviceId for the new user
	_, err := rdb.DB(a.dbName).Table("users").Get(user).
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
	rows, err := rdb.DB(a.dbName).Table("users").GetAll(ids...).Pluck("Id", "Devices").
		Default(nil).Limit(maxResults).Run(a.conn)
	if err != nil {
		return nil, 0, err
	}

	var row struct {
		Id      string
		Devices map[string]*t.DeviceDef
	}

	result := make(map[t.Uid][]t.DeviceDef)
	count := 0
	var uid t.Uid
	for rows.Next(&row) {
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

	return result, count, rows.Err()
}

func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Replace(rdb.Row.Without(
		map[string]string{"Devices": deviceHasher(deviceID)})).RunWrite(a.conn)
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
	rows, err := q.Run(a.conn)
	if err != nil || rows.IsNil() {
		return nil, err
	}

	var result []*t.Credential
	if err = rows.All(&result); err != nil {
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
	row, err := rdb.DB(a.dbName).Table("fileuploads").Get(fid).Run(a.conn)
	if err != nil || row.IsNil() {
		return nil, err
	}

	var fd t.FileDef
	if err = row.One(&fd); err != nil {
		return nil, err
	}

	return &fd, nil

}

// FileDeleteUnused
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	q := rdb.DB(a.dbName).Table("fileuploads").GetAllByIndex("UseCount", 0)
	if !olderThan.IsZero() {
		q = q.Filter(rdb.Row.Field("UpdatedAt").Lt(olderThan))
	}
	if limit > 0 {
		q = q.Limit(limit)
	}

	rows, err := q.Pluck("Location").Run(a.conn)
	if err != nil {
		return nil, err
	}

	var locations []string
	var loc map[string]string
	for rows.Next(&loc) {
		locations = append(locations, loc["Location"])
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	_, err = q.Delete().RunWrite(a.conn)

	return locations, err
}

func init() {
	store.RegisterAdapter(adapterName, &adapter{})
}
