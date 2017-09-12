package rethinkdb

import (
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	rdb "gopkg.in/gorethink/gorethink.v2"
)

type RethinkDbAdapter struct {
	conn   *rdb.Session
	dbName string
}

const (
	defaultHost     = "localhost:28015"
	defaultDatabase = "tinode"
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
	MAX_RESULTS         = 1024
	MAX_SUBSCRIBERS     = 128
	MAX_DELETE_MESSAGES = 128
)

// Open initializes rethinkdb session
func (a *RethinkDbAdapter) Open(jsonconfig string) error {
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

	return err
}

// Close closes the underlying database connection
func (a *RethinkDbAdapter) Close() error {
	var err error
	if a.conn != nil {
		// Close will wait for all outstanding requests to finish
		err = a.conn.Close()
		a.conn = nil
	}
	return err
}

// IsOpen returns true if connection to database has been established. It does not check if
// connection is actually live.
func (a *RethinkDbAdapter) IsOpen() bool {
	return a.conn != nil
}

// CreateDb initializes the storage. If reset is true, the database is first deleted losing all the data.
func (a *RethinkDbAdapter) CreateDb(reset bool) error {

	// Drop database if exists, ignore error if it does not.
	if reset {
		rdb.DBDrop("tinode").RunWrite(a.conn)
	}

	if _, err := rdb.DBCreate("tinode").RunWrite(a.conn); err != nil {
		return err
	}

	// Users
	if _, err := rdb.DB("tinode").TableCreate("users", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Create secondary index on User.Tags array so user can be found by tags
	if _, err := rdb.DB("tinode").Table("users").IndexCreate("Tags", rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}
	// User authentication records {unique, userid, secret}
	if _, err := rdb.DB("tinode").TableCreate("auth", rdb.TableCreateOpts{PrimaryKey: "unique"}).RunWrite(a.conn); err != nil {
		return err
	}
	// Should be able to access user's auth records by user id
	if _, err := rdb.DB("tinode").Table("auth").IndexCreate("userid").RunWrite(a.conn); err != nil {
		return err
	}

	// Subscription to a topic. The primary key is a Topic:User string
	if _, err := rdb.DB("tinode").TableCreate("subscriptions", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("subscriptions").IndexCreate("User").RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("subscriptions").IndexCreate("Topic").RunWrite(a.conn); err != nil {
		return err
	}

	// Topic stored in database
	if _, err := rdb.DB("tinode").TableCreate("topics", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}

	// Stored message
	if _, err := rdb.DB("tinode").TableCreate("messages", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("messages").IndexCreateFunc("Topic_SeqId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("SeqId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("messages").IndexCreateFunc("Topic_UpdatedAt",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("UpdatedAt")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

	// Index of unique user contact information as strings, such as "email:jdoe@example.com" or "tel:18003287448":
	// {Id: <tag>, Source: <uid>} to ensure uniqueness of tags.
	if _, err := rdb.DB("tinode").TableCreate("tagunique", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}

	return nil
}

// UserCreate creates a new user. Returns error and true if error is due to duplicate user name,
// false for any other error
func (a *RethinkDbAdapter) UserCreate(user *t.User) (error, bool) {
	// Save user's tags to a separate table to ensure uniquness
	// TODO(gene): add support for non-unique tags
	if user.Tags != nil {
		type tag struct {
			Id     string
			Source string
		}
		tags := make([]tag, 0, len(user.Tags))
		for _, t := range user.Tags {
			tags = append(tags, tag{Id: t, Source: user.Id})
		}
		res, err := rdb.DB(a.dbName).Table("tagunique").Insert(tags).RunWrite(a.conn)
		if err != nil || res.Inserted != len(user.Tags) {
			if res.Inserted > 0 {
				// Something went wrong, do best effort delete of inserted tags
				rdb.DB(a.dbName).Table("tagunique").GetAll(user.Tags).
					Filter(map[string]interface{}{"Source": user.Id}).Delete().RunWrite(a.conn)
			}
			return err, false
		}
	}

	_, err := rdb.DB(a.dbName).Table("users").Insert(&user).RunWrite(a.conn)
	if err != nil {
		return err, false
	}

	return nil, false
}

// Add user's authentication record
func (a *RethinkDbAdapter) AddAuthRecord(uid t.Uid, authLvl int, unique string,
	secret []byte, expires time.Time) (error, bool) {

	_, err := rdb.DB(a.dbName).Table("auth").Insert(
		map[string]interface{}{
			"unique":  unique,
			"userid":  uid.String(),
			"authLvl": authLvl,
			"secret":  secret,
			"expires": expires}).RunWrite(a.conn)
	if err != nil {
		if rdb.IsConflictErr(err) {
			return errors.New("duplicate credential"), true
		}
		return err, false
	}
	return nil, false
}

// Delete user's authentication record
func (a *RethinkDbAdapter) DelAuthRecord(unique string) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").Get(unique).Delete().RunWrite(a.conn)
	return res.Deleted, err
}

// Delete user's all authentication records
func (a *RethinkDbAdapter) DelAllAuthRecords(uid t.Uid) (int, error) {
	res, err := rdb.DB(a.dbName).Table("auth").GetAllByIndex("userid", uid.String()).Delete().RunWrite(a.conn)
	return res.Deleted, err
}

// Update user's authentication secret
func (a *RethinkDbAdapter) UpdAuthRecord(unique string, authLvl int, secret []byte, expires time.Time) (int, error) {
	log.Println("Updating for unique", unique)

	res, err := rdb.DB(a.dbName).Table("auth").Get(unique).Update(
		map[string]interface{}{
			"authLvl": authLvl,
			"secret":  secret,
			"expires": expires}).RunWrite(a.conn)
	return res.Updated, err
}

// Retrieve user's authentication record
func (a *RethinkDbAdapter) GetAuthRecord(unique string) (t.Uid, int, []byte, time.Time, error) {
	// Default() is needed to prevent Pluck from returning an error
	rows, err := rdb.DB(a.dbName).Table("auth").Get(unique).Pluck(
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

	if !rows.Next(&record) {
		return t.ZeroUid, 0, nil, time.Time{}, rows.Err()
	}
	rows.Close()

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return t.ParseUid(record.Userid), record.AuthLvl, record.Secret, record.Expires, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *RethinkDbAdapter) UserGet(uid t.Uid) (*t.User, error) {
	if row, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Run(a.conn); err == nil && !row.IsNil() {
		var user t.User
		if err = row.One(&user); err == nil {
			return &user, nil
		}
		return nil, err
	} else {
		if row != nil {
			row.Close()
		}
		// If user does not exist, it returns nil, nil
		return nil, err
	}
}

func (a *RethinkDbAdapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = id.String()
	}

	users := []t.User{}
	if rows, err := rdb.DB(a.dbName).Table("users").GetAll(uids...).Run(a.conn); err != nil {
		return nil, err
	} else {
		var user t.User
		for rows.Next(&user) {
			users = append(users, user)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (a *RethinkDbAdapter) UserDelete(uid t.Uid, soft bool) error {
	var err error
	q := rdb.DB(a.dbName).Table("users").Get(uid.String())
	if soft {
		now := t.TimeNow()
		_, err = q.Update(map[string]interface{}{"DeletedAt": now, "UpdatedAt": now}).RunWrite(a.conn)
	} else {
		_, err = q.Delete().Run(a.conn)
	}
	return err
}

func (a *RethinkDbAdapter) UserUpdateLastSeen(uid t.Uid, userAgent string, when time.Time) error {
	update := struct {
		LastSeen  time.Time
		UserAgent string
	}{when, userAgent}

	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

/*
func (a *RethinkDbAdapter) UserUpdateStatus(uid t.Uid, status interface{}) error {
	update := map[string]interface{}{"Status": status}

	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}
*/

func (a *RethinkDbAdapter) ChangePassword(id t.Uid, password string) error {
	return errors.New("ChangePassword: not implemented")
}

func (a *RethinkDbAdapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	// FIXME(gene): add Tag re-indexing
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(update).RunWrite(a.conn)
	return err
}

// *****************************

// TopicCreate creates a topic from template
func (a *RethinkDbAdapter) TopicCreate(topic *t.Topic) error {
	_, err := rdb.DB(a.dbName).Table("topics").Insert(&topic).RunWrite(a.conn)
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *RethinkDbAdapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
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

func (a *RethinkDbAdapter) TopicGet(topic string) (*t.Topic, error) {
	// Fetch topic by name
	rows, err := rdb.DB(a.dbName).Table("topics").Get(topic).Run(a.conn)
	if err != nil {
		return nil, err
	}

	if rows.IsNil() {
		rows.Close()
		return nil, nil
	}

	var tt = new(t.Topic)
	if err = rows.One(tt); err != nil {
		return nil, err
	}

	return tt, rows.Err()
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' subscription.
// Reads and denormalizes Public value.
func (a *RethinkDbAdapter) TopicsForUser(uid t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	// Subscription have Topic.UpdatedAt denormalized into Subscription.UpdatedAt
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", uid.String())
	if !keepDeleted {
		// Filter out rows with defined DeletedAt
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(MAX_RESULTS)
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
		if tcat == t.TopicCat_Me || tcat == t.TopicCat_Fnd {
			continue

			// p2p subscription, find the other user to get user.Public
		} else if tcat == t.TopicCat_P2P {
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
			sub.SetHardClearId(top.ClearId)
			if t.GetTopicCat(sub.Topic) == t.TopicCat_Grp {
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
func (a *RethinkDbAdapter) UsersForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch topic subscribers
	// Fetch all subscribed users. The number of users is not large
	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("Topic", topic)
	if !keepDeleted {
		// Filter out rows with DeletedAt being not null
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(MAX_SUBSCRIBERS)
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

func (a *RethinkDbAdapter) TopicShare(shares []*t.Subscription) (int, error) {
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

func (a *RethinkDbAdapter) TopicDelete(topic string) error {
	_, err := rdb.DB(a.dbName).Table("topics").Get(topic).Delete().RunWrite(a.conn)
	return err
}

func (a *RethinkDbAdapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {

	update := struct {
		SeqId int
	}{msg.SeqId}

	// Invite - 'me' topic
	var err error
	if strings.HasPrefix(topic, "usr") {
		// Topic is passed as usrABCD, but the 'users' table expectes Id without the 'usr' prefix.
		user := t.ParseUserId(topic).String()
		_, err = rdb.DB("tinode").Table("users").Get(user).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

		// All other messages
	} else {
		_, err = rdb.DB("tinode").Table("topics").Get(topic).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)
	}

	return err
}

func (a *RethinkDbAdapter) TopicUpdate(topic string, update map[string]interface{}) error {
	_, err := rdb.DB("tinode").Table("topics").Get(topic).Update(update).RunWrite(a.conn)
	return err
}

// Get a subscription of a user to a topic
func (a *RethinkDbAdapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {

	rows, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Run(a.conn)
	if err != nil {
		return nil, err
	}

	if rows.IsNil() {
		rows.Close()
		return nil, nil
	}

	var sub t.Subscription
	err = rows.One(&sub)

	if sub.DeletedAt != nil {
		return nil, rows.Err()
	}
	return &sub, rows.Err()
}

// Update time when the user was last attached to the topic
func (a *RethinkDbAdapter) SubsLastSeen(topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic+":"+user.String()).
		Update(map[string]interface{}{"LastSeen": lastSeen}, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

// SubsForUser loads a list of user's subscriptions to topics. Does NOT read Public value.
func (a *RethinkDbAdapter) SubsForUser(forUser t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	if forUser.IsZero() {
		return nil, errors.New("RethinkDb adapter: invalid user ID in SubsForUser")
	}

	q := rdb.DB(a.dbName).Table("subscriptions").GetAllByIndex("User", forUser.String())
	if !keepDeleted {
		q = q.Filter(rdb.Row.HasFields("DeletedAt").Not())
	}
	q = q.Limit(MAX_RESULTS)

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
func (a *RethinkDbAdapter) SubsForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	//log.Println("Loading subscriptions for topic ", topic)

	// must load User.Public for p2p topics
	var p2p []t.User
	var err error
	if t.GetTopicCat(topic) == t.TopicCat_P2P {
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
	q = q.Limit(MAX_SUBSCRIBERS)
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
func (a *RethinkDbAdapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").
		Get(topic + ":" + user.String()).Update(update).RunWrite(a.conn)
	return err
}

// SubsDelete marks subscription as deleted.
func (a *RethinkDbAdapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	_, err := rdb.DB(a.dbName).Table("subscriptions").
		Get(topic + ":" + user.String()).Update(map[string]interface{}{
		"UpdatedAt": now,
		"DeletedAt": now,
	}).RunWrite(a.conn)
	// _, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Delete().RunWrite(a.conn)
	return err
}

// SubsDelForTopic markes all subscriptions to the given topic as deleted
func (a *RethinkDbAdapter) SubsDelForTopic(topic string) error {
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
// Just search the 'users.Tags' for the given tags using respective index.
func (a *RethinkDbAdapter) FindSubs(uid t.Uid, query []interface{}) ([]t.Subscription, error) {
	// Query may contain redundant records, i.e. the same email twice.
	// User could be matched on multiple tags, i.e on email and phone#. Thus the query may
	// return duplicate users. Thus the need for distinct.
	if rows, err := rdb.DB(a.dbName).Table("users").GetAllByIndex("Tags", query...).Limit(MAX_RESULTS).
		Pluck("Id", "Access", "CreatedAt", "UpdatedAt", "Public", "Tags").Distinct().Run(a.conn); err != nil {
		return nil, err
	} else {
		index := make(map[string]struct{})
		for _, q := range query {
			if tag, ok := q.(string); ok {
				index[tag] = struct{}{}
			}
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
			// TODO(gene): maybe remove it
			// sub.ModeWant, sub.ModeGiven = user.Access.Auth, user.Access.Auth
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
}

// Messages
func (a *RethinkDbAdapter) MessageSave(msg *t.Message) error {
	msg.SetUid(store.GetUid())
	_, err := rdb.DB(a.dbName).Table("messages").Insert(msg).RunWrite(a.conn)
	return err
}

func (a *RethinkDbAdapter) MessageGetAll(topic string, forUser t.Uid, opts *t.BrowseOpt) ([]t.Message, error) {
	//log.Println("Loading messages for topic ", topic, opts)

	var limit uint = 1024 // TODO(gene): pass into adapter as a config param
	var lower, upper interface{}

	// Default index
	useIndex := "Topic_SeqId"

	upper = rdb.MaxVal
	lower = rdb.MinVal

	if opts != nil {
		if opts.ByTime {
			useIndex = "Topic_UpdatedAt"

			if opts.After != nil && !opts.After.IsZero() {
				lower = opts.After
			}
			if opts.Until != nil && !opts.Until.IsZero() {
				upper = opts.Until
			}
		} else {
			useIndex = "Topic_SeqId"

			if opts.Since > 0 {
				lower = opts.Since
			}
			if opts.Before > 0 {
				upper = opts.Before
			}
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	lower = []interface{}{topic, lower}
	upper = []interface{}{topic, upper}

	rows, err := rdb.DB(a.dbName).Table("messages").Between(lower, upper, rdb.BetweenOpts{Index: useIndex}).
		OrderBy(rdb.OrderByOpts{Index: rdb.Desc("Topic_SeqId")}).Limit(limit).Run(a.conn)

	if err != nil {
		return nil, err
	}

	var msgs []t.Message
	rows.All(&msgs)

	requester := forUser.String()

	for i := 0; i < len(msgs); i++ {
		if msgs[i].DeletedFor != nil {
			for j := 0; j < len(msgs[i].DeletedFor); j++ {
				if msgs[i].DeletedFor[j].User == requester {
					msgs[i].DeletedAt = &msgs[i].DeletedFor[j].Timestamp
				}
			}
		}
	}

	return msgs, rows.Err()
}

// MessageDeleteAll hard-deletes messages in the given topic
func (a *RethinkDbAdapter) MessageDeleteAll(topic string, clear int) error {
	var maxval interface{} = clear
	if clear < 0 {
		maxval = rdb.MaxVal
	}
	_, err := rdb.DB(a.dbName).Table("messages").
		Between([]interface{}{topic, 0}, []interface{}{topic, maxval},
			rdb.BetweenOpts{Index: "Topic_SeqId", RightBound: "closed"}).
		Delete().RunWrite(a.conn)

	return err
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *RethinkDbAdapter) MessageDeleteList(topic string, forUser t.Uid, hard bool, list []int) (err error) {
	var indexVals []interface{}
	for _, seq := range list {
		indexVals = append(indexVals, []interface{}{topic, seq})
	}
	if hard {
		_, err = rdb.DB(a.dbName).Table("messages").GetAllByIndex("Topic_SeqId", indexVals...).
			Update(map[string]interface{}{"DeletedAt": t.TimeNow(),
				"Content": nil}).RunWrite(a.conn)
	} else {
		_, err = rdb.DB(a.dbName).Table("messages").GetAllByIndex("Topic_SeqId", indexVals...).
			Update(map[string]interface{}{"DeletedFor": rdb.Row.Field("DeletedFor").Append(&t.SoftDelete{
				User:      forUser.String(),
				Timestamp: t.TimeNow()})}).
			RunWrite(a.conn)
	}

	return err
}

/*
func addOptions(q rdb.Term, value string, index string, opts *t.BrowseOpt) rdb.Term {
	var limit uint = 1024 // TODO(gene): pass into adapter as a config param
	var lower, upper interface{}

	if opts != nil {
		if opts.Since > 0 {
			lower = opts.Since
		} else {
			lower = rdb.MinVal
		}

		if opts.Before > 0 {
			upper = opts.Before
		} else {
			upper = rdb.MaxVal
		}

		if value != "" {
			lower = []interface{}{value, lower}
			upper = []interface{}{value, upper}
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	} else {
		lower = []interface{}{value, rdb.MinVal}
		upper = []interface{}{value, rdb.MaxVal}
	}

	return q.Between(lower, upper, rdb.BetweenOpts{Index: index}).
		OrderBy(rdb.OrderByOpts{Index: rdb.Desc(index)}).Limit(limit)
}
*/

func deviceHasher(deviceId string) string {
	// Generate custom key as [64-bit hash of device id] to ensure predictable
	// length of the key
	hasher := fnv.New64()
	hasher.Write([]byte(deviceId))
	return strconv.FormatUint(uint64(hasher.Sum64()), 16)
}

// Device management for push notifications
func (a *RethinkDbAdapter) DeviceUpsert(user t.Uid, def *t.DeviceDef) error {
	hash := deviceHasher(def.DeviceId)
	_, err := rdb.DB(a.dbName).Table("users").Get(user.String()).
		Update(map[string]interface{}{
			"Devices": map[string]*t.DeviceDef{
				hash: def,
			}}).RunWrite(a.conn)
	return err
}

func (a *RethinkDbAdapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	ids := make([]interface{}, len(uids))
	for i, id := range uids {
		ids[i] = id.String()
	}

	// {Id: "userid", Devices: {"hash1": {..def1..}, "hash2": {..def2..}}
	rows, err := rdb.DB(a.dbName).Table("users").GetAll(ids...).Pluck("Id", "Devices").
		Default(nil).Limit(MAX_RESULTS).Run(a.conn)
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
				log.Print(err.Error())
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

func (a *RethinkDbAdapter) DeviceDelete(uid t.Uid, deviceId string) error {
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Replace(rdb.Row.Without(
		map[string]string{"Devices": deviceHasher(deviceId)})).RunWrite(a.conn)
	return err
}

func init() {
	store.Register("rethinkdb", &RethinkDbAdapter{})
}
