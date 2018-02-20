// +build rethinkdb

package rethinkdb

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

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

	dbVersion = 100

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

const (
	// Maximum number of records to return
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
	if err != nil {
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
	// [[Topic, User1, DelId1], [Topic, User2, DelId2],...]
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

	// Index of unique user contact information as strings, such as "email:jdoe@example.com" or "tel:18003287448":
	// {Id: <tag>, Source: <uid>} to ensure uniqueness of tags.
	if _, err := rdb.DB(a.dbName).TableCreate("tagunique", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}

	return nil
}

// Indexable tag as stored in 'tagunique'
type storedTag struct {
	Id     string
	Source string
}

// UserCreate creates a new user. Returns error and true if error is due to duplicate user name,
// false for any other error
func (a *adapter) UserCreate(user *t.User) error {
	// Save user's tags to a separate table to ensure uniquness
	// TODO(gene): add support for non-unique tags
	if len(user.Tags) > 0 {
		tags := make([]storedTag, 0, len(user.Tags))
		for _, t := range user.Tags {
			tags = append(tags, storedTag{Id: t, Source: user.Id})
		}
		res, err := rdb.DB(a.dbName).Table("tagunique").Insert(tags).RunWrite(a.conn)
		if err != nil {
			if res.Inserted > 0 {
				// Something went wrong, do best effort deletion of inserted tags
				rdb.DB(a.dbName).Table("tagunique").GetAll(user.Tags).
					Filter(map[string]interface{}{"Source": user.Id}).Delete().RunWrite(a.conn)
			}

			if rdb.IsConflictErr(err) {
				return errors.New("duplicate tag(s)")
			}
			return err
		}
	}

	_, err := rdb.DB(a.dbName).Table("users").Insert(&user).RunWrite(a.conn)
	if err != nil {
		return err
	}

	return nil
}

// Add user's authentication record
func (a *adapter) AddAuthRecord(uid t.Uid, authLvl int, unique string,
	secret []byte, expires time.Time) (bool, error) {

	_, err := rdb.DB(a.dbName).Table("auth").Insert(
		map[string]interface{}{
			"unique":  unique,
			"userid":  uid.String(),
			"authLvl": authLvl,
			"secret":  secret,
			"expires": expires}).RunWrite(a.conn)
	if err != nil {
		if rdb.IsConflictErr(err) {
			return true, errors.New("duplicate credential")
		}
		return false, err
	}
	return false, nil
}

// Delete user's authentication record
func (a *adapter) DelAuthRecord(unique string) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").Get(unique).Delete().RunWrite(a.conn)
	return res.Deleted, err
}

// Delete user's all authentication records
func (a *adapter) DelAllAuthRecords(uid t.Uid) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).Delete().RunWrite(a.conn)
	return res.Deleted, err
}

// Update user's authentication secret
func (a *adapter) UpdAuthRecord(unique string, authLvl int, secret []byte, expires time.Time) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").Get(unique).Update(
		map[string]interface{}{
			"authLvl": authLvl,
			"secret":  secret,
			"expires": expires}).RunWrite(a.conn)
	return res.Updated, err
}

// Retrieve user's authentication record
func (a *adapter) GetAuthRecord(unique string) (t.Uid, int, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	row, err := rdb.DB(a.dbName).Table("auth").Get(unique).Pluck(
		"userid", "secret", "expires", "authLvl").Default(nil).Run(a.conn)
	if err != nil {
		return t.ZeroUid, 0, nil, time.Time{}, err
	}

	var record struct {
		Userid  string    `gorethink:"userid"`
		AuthLvl int       `gorethink:"authLvl"`
		Secret  []byte    `gorethink:"secret"`
		Expires time.Time `gorethink:"expires"`
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
	if err == nil && !row.IsNil() {
		var user t.User
		if err = row.One(&user); err == nil {
			return &user, nil
		}
		return nil, err
	}

	if row != nil {
		row.Close()
	}

	// If user does not exist, it returns nil, nil
	return nil, err
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

func (a *adapter) UserUpdateLastSeen(uid t.Uid, userAgent string, when time.Time) error {
	update := struct {
		LastSeen  time.Time
		UserAgent string
	}{when, userAgent}

	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

// UserUpdate updates user object. Use UserTagsUpdate when updating Tags.
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

	// Ensure this is a new subscription. If one already exist, don't overwrite it
	invited.Id = invited.Topic + ":" + invited.User
	_, err = rdb.DB(a.dbName).Table("subscriptions").Insert(invited, rdb.InsertOpts{Conflict: "error"}).
		RunWrite(a.conn)
	if err != nil {
		// Is this a duplicate subscription? If so, ifnore it. Otherwise it's a genuine DB error
		if !rdb.IsConflictErr(err) {
			return err
		}
	}

	topic := &t.Topic{ObjHeader: t.ObjHeader{Id: initiator.Topic}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	return a.TopicCreate(topic)
}

func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	// Fetch topic by name
	row, err := rdb.DB(a.dbName).Table("topics").Get(topic).Run(a.conn)
	if err != nil {
		return nil, err
	}

	var tt = new(t.Topic)
	if err = row.One(tt); err != nil {
		return nil, err
	}

	return tt, row.Err()
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' subscription.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	// Subscription have Topic.UpdatedAt denormalized into Subscription.UpdatedAt
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", uid.String())
	if !keepDeleted {
		// Filter out rows with defined DeletedAt
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(maxResults)
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
			// sub.SetDelId(top.DelId)
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
func (a *adapter) UsersForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch topic subscribers
	// Fetch all subscribed users. The number of users is not large
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if !keepDeleted {
		// Filter out rows with DeletedAt being not null
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(maxSubscribers)
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
	// as deleted, unmark.
	resp, err := rdb.DB(a.dbName).Table("subscriptions").
		Insert(shares, rdb.InsertOpts{Conflict: "update"}).RunWrite(a.conn)
	if err != nil {
		return resp.Inserted + resp.Replaced, err
	}

	return resp.Inserted + resp.Replaced, nil
}

func (a *adapter) TopicDelete(topic string) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).Delete().RunWrite(a.conn)
	return err
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {

	update := struct {
		SeqId int
	}{msg.SeqId}

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

	rows, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Run(a.conn)
	if err != nil {
		return nil, err
	}

	var sub t.Subscription
	if err = rows.One(&sub); err != nil {
		return nil, err
	}

	if sub.DeletedAt != nil {
		return nil, rows.Err()
	}
	return &sub, rows.Err()
}

// Update time when the user was last attached to the topic
func (a *adapter) SubsLastSeen(topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic+":"+user.String()).
		Update(map[string]interface{}{"LastSeen": lastSeen}, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

// SubsForUser loads a list of user's subscriptions to topics. Does NOT read Public value.
func (a *adapter) SubsForUser(forUser t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	if forUser.IsZero() {
		return nil, errors.New("RethinkDb adapter: invalid user ID in SubsForUser")
	}

	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", forUser.String())
	if !keepDeleted {
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(maxResults)

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

// SubsForTopic fetches all subsciptions for a topic.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	//log.Println("Loading subscriptions for topic ", topic)

	// must load User.Public for p2p topics
	var p2p []t.User
	var err error
	if t.GetTopicCat(topic) == t.TopicCatP2P {
		uid1, uid2, _ := t.ParseP2P(topic)
		if p2p, err = a.UserGetAll(uid1, uid2); err != nil {
			return nil, err
		} else if len(p2p) != 2 {
			return nil, errors.New("failed to load two p2p users")
		}
	}

	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if !keepDeleted {
		// Filter out rows where DeletedAt is defined
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(maxSubscribers)
	//log.Println("Loading subscription q=", q)

	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next(&ss) {
		if p2p != nil {
			// Assigning values provided by the other user
			if p2p[0].Id == ss.User {
				ss.SetPublic(p2p[1].Public)
				ss.SetWith(p2p[1].Id)
				ss.SetDefaultAccess(p2p[1].Access.Auth, p2p[1].Access.Anon)
			} else {
				ss.SetPublic(p2p[0].Public)
				ss.SetWith(p2p[0].Id)
				ss.SetDefaultAccess(p2p[0].Access.Auth, p2p[0].Access.Anon)
			}
		}
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

// Returns a list of users who match given tags, such as "email:jdoe@example.com" or "tel:18003287448".
// Searching the 'users.Tags' for the given tags using respective index.
func (a *adapter) FindUsers(uid t.Uid, tags []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var query []interface{}
	for _, tag := range tags {
		query = append(query, tag)
		index[tag] = struct{}{}
	}

	// Get users matched by tags, sort by number of matches from high to low.
	rows, err := rdb.DB(a.dbName).
		Table("users").
		GetAllByIndex("Tags", query...).
		Pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").
		Group("Id").
		Ungroup().
		Map(func(row rdb.Term) rdb.Term {
			return row.Field("reduction").
				Nth(0).
				Merge(map[string]interface{}{"MatchedTagsCount": row.Field("reduction").Count()})
		}).
		// Query may contain redundant records, i.e. the same tag twice.
		// User could be matched on multiple tags, i.e on email and phone#. Thus the query may
		// return duplicate users. Thus the need for distinct.
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
func (a *adapter) FindTopics(tags []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var query []interface{}
	for _, tag := range tags {
		query = append(query, tag)
		index[tag] = struct{}{}
	}

	/*
	   Javascript query we are replicating here in Go:
	   r.db('tinode').table('users')
	   	.getAll("email:alice@example.com", "email:bob@example.com", "tel:17025550002", {index: 'Tags'})
	   	.group("Id")
	   	.ungroup()
	   	.map(function(row) {
	   		return row.getField('reduction')
	   			.nth(0)
	   			.merge({
	   				merge_count: row.getField('reduction').count()
	   			})
	   	})
	   	.orderBy(r.desc("merge_count"))
	*/

	rows, err := rdb.DB(a.dbName).
		Table("topics").
		GetAllByIndex("Tags", query...).
		Pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").
		Group("Id").
		Ungroup().
		Map(func(row rdb.Term) rdb.Term {
			return row.Field("reduction").
				Nth(0).
				Merge(map[string]interface{}{"MatchedTagsCount": row.Field("reduction").Count()})
		}).
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

// UserTagsUpdate updates user's Tags. 'unique' contains the prefixes of tags which are
// treated as unique, i.e. 'email' or 'tel'.
func (a *adapter) UserTagsUpdate(uid t.Uid, unique, tags []string) error {
	user, err := a.UserGet(uid)
	if err != nil {
		return err
	}

	added, removed := tagsUniqueDelta(unique, user.Tags, tags)
	if err := a.updateUniqueTags(user.Id, added, removed); err != nil {
		return err
	}

	return a.UserUpdate(uid, map[string]interface{}{"Tags": tags})
}

// TopicTagsUpdate updates topic's tags.
// - name is the name of the topic to update
// - unique is the list of prefixes to treat as unique.
// - tags are the new tags.
func (a *adapter) TopicTagsUpdate(name string, unique, tags []string) error {
	topic, err := a.TopicGet(name)
	if err != nil {
		return err
	}

	added, removed := tagsUniqueDelta(unique, topic.Tags, tags)
	if err := a.updateUniqueTags(name, added, removed); err != nil {
		return err
	}

	return a.TopicUpdate(name, map[string]interface{}{"Tags": tags})
}

func (a *adapter) updateUniqueTags(source string, added, removed []string) error {
	if added != nil && len(added) > 0 {
		toAdd := make([]storedTag, 0, len(added))
		for _, tag := range added {
			toAdd = append(toAdd, storedTag{Id: tag, Source: source})
		}
		res, err := rdb.DB(a.dbName).Table("tagunique").Insert(toAdd).RunWrite(a.conn)
		if err != nil {
			if res.Inserted > 0 {
				// Something went wrong, do best effort deletion of inserted tags
				rdb.DB(a.dbName).Table("tagunique").GetAll(added).
					Filter(map[string]interface{}{"Source": source}).Delete().RunWrite(a.conn)
			}

			if rdb.IsConflictErr(err) {
				return errors.New("duplicate tag(s)")
			}
			return err
		}
	}

	if removed != nil && len(removed) > 0 {
		_, err := rdb.DB(a.dbName).Table("tagunique").GetAll(removed).
			Filter(map[string]interface{}{"Source": source}).Delete().RunWrite(a.conn)
		if err != nil {
			return err
		}
	}

	return nil
}

// tagsUniqueDelta extracts the lists of added unique tags and removed unique tags:
//   added :=  newTags - (oldTags & newTags) -- present in new but missing in old
//   removed := oldTags - (newTags & oldTags) -- present in old but missing in new
func tagsUniqueDelta(unique, oldTags, newTags []string) (added, removed []string) {
	if oldTags == nil {
		return filterUniqueTags(unique, newTags), nil
	}
	if newTags == nil {
		return nil, filterUniqueTags(unique, oldTags)
	}

	sort.Strings(oldTags)
	sort.Strings(newTags)

	// Match old tags against the new tags and separate removed tags from added.
	iold, inew := 0, 0
	lold, lnew := len(oldTags), len(newTags)
	for iold < lold || inew < lnew {
		if (iold == lold && inew < lnew) || oldTags[iold] > newTags[inew] {
			// Present in new, missing in old: added
			added = append(added, newTags[inew])
			inew++

		} else if (inew == lnew && iold < lold) || oldTags[iold] < newTags[inew] {
			// Present in old, missing in new: removed
			removed = append(removed, oldTags[iold])

			iold++

		} else {
			// present in both
			if iold < lold {
				iold++
			}
			if inew < lnew {
				inew++
			}
		}
	}
	return filterUniqueTags(unique, added), filterUniqueTags(unique, removed)
}

func filterUniqueTags(unique, tags []string) []string {
	var out []string
	if unique != nil && len(unique) > 0 && tags != nil {
		for _, s := range tags {
			parts := strings.SplitN(s, ":", 2)

			if len(parts) < 2 {
				continue
			}

			for _, u := range unique {
				if parts[0] == u {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// Messages
func (a *adapter) MessageSave(msg *t.Message) error {
	msg.SetUid(store.GetUid())
	_, err := rdb.DB(a.dbName).Table("messages").Insert(msg).RunWrite(a.conn)
	return err
}

func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.BrowseOpt) ([]t.Message, error) {
	//log.Println("Loading messages for topic ", topic, opts)

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
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.BrowseOpt) ([]t.DelMessage, error) {
	var limit = 1024 // TODO(gene): pass into adapter as a config param
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
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) (err error) {
	var indexVals []interface{}

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
		_, err := rdb.DB(a.dbName).Table("dellog").Insert(toDel).RunWrite(a.conn)
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

		if toDel.DeletedFor == "" {
			// Hard-deleting for all users{
			// Hard-delete of individual messages. Mark some messages as deleted.
			_, err = query.Filter(rdb.Row.HasFields("DelId").Not()).
				Update(map[string]interface{}{"DeletedAt": t.TimeNow(), "DelId": toDel.DelId, "From": nil,
					"Head": nil, "Content": nil}).RunWrite(a.conn)
		} else {
			// Soft-deleting: adding DelId to DeletedFor
			_, err = query.
				// Skip hard-deleted messages
				Filter(rdb.Row.HasFields("DelId").Not()).
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

func init() {
	store.Register(adapterName, &adapter{})
}
