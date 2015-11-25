package rethinkdb

import (
	"encoding/json"
	"errors"
	rdb "github.com/dancannon/gorethink"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	//	"log"
	"strings"
	"time"
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

var uGen t.UidGenerator

// Open initializes rethinkdb session
func (a *RethinkDbAdapter) Open(jsonconfig string, workerId int, uidkey []byte) error {
	if a.conn != nil {
		return errors.New("adapter rethinkdb is already connected")
	}

	var err error
	var config configType

	if err = json.Unmarshal([]byte(jsonconfig), &config); err != nil {
		return errors.New("adapter rethinkdb failed to parse config: " + err.Error())
	}

	// Initialise snowflake
	if err = uGen.Init(uint(workerId), uidkey); err != nil {
		return errors.New("adapter rethinkdb failed to init snowflake: " + err.Error())
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
		err = a.conn.Close(rdb.CloseOpts{NoReplyWait: false})
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
	if _, err := rdb.DB("tinode").Table("users").IndexCreate("Username").RunWrite(a.conn); err != nil {
		return err
	}

	// Subscription to a topic. The primary key is a Topic:User string
	if _, err := rdb.DB("tinode").TableCreate("subscriptions", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("subscriptions").IndexCreateFunc("User_UpdatedAt",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("User"), row.Field("UpdatedAt")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

	if _, err := rdb.DB("tinode").Table("subscriptions").IndexCreateFunc("Topic_UpdatedAt",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("UpdatedAt")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

	// Topic stored in database
	if _, err := rdb.DB("tinode").TableCreate("topics", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("topics").IndexCreate("Name").RunWrite(a.conn); err != nil {
		return err
	}

	// Stored message
	if _, err := rdb.DB("tinode").TableCreate("messages", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("messages").IndexCreateFunc("Topic_CreatedAt",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("CreatedAt")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

	// Index for unique fields
	if _, err := rdb.DB("tinode").TableCreate("_uniques").RunWrite(a.conn); err != nil {
		return err
	}

	return nil
}

// UserCreate creates a new user. Returns error and bool - true if error is due to duplicate user name
func (a *RethinkDbAdapter) UserCreate(appId uint32, user *t.User) (error, bool) {
	// FIXME(gene): Rethink has no support for transactions. Prevent other routines from using
	// the username currently being processed. For instance, keep it in a shared map for the duration of transaction

	// Check username for uniqueness (no built-in support for secondary unique indexes in RethinkDB)
	value := "users!username!" + user.Username // unique value=primary key
	_, err := rdb.DB(a.dbName).Table("_uniques").Insert(map[string]string{"id": value}).RunWrite(a.conn)
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate primary key") {
			return errors.New("duplicate credential"), true
		}
		return err, false
	}

	// Validated unique username, inserting user now
	user.SetUid(uGen.Get())
	_, err = rdb.DB(a.dbName).Table("users").Insert(&user).RunWrite(a.conn)
	if err != nil {
		// Delete inserted _uniques entries in case of errors
		rdb.DB(a.dbName).Table("_uniques").Get(value).Delete(rdb.DeleteOpts{Durability: "soft"}).RunWrite(a.conn)
		return err, false
	}

	return nil, false
}

// Users
func (a *RethinkDbAdapter) GetPasswordHash(appid uint32, uname string) (t.Uid, []byte, error) {

	var err error

	rows, err := rdb.DB(a.dbName).Table("users").GetAllByIndex("Username", uname).
		Pluck("Id", "Passhash").Run(a.conn)
	if err != nil {
		return t.ZeroUid, nil, err
	}

	var user t.User
	if rows.Next(&user) {
		//log.Println("loggin in user Id=", user.Uid(), user.Id)
		if user.Uid().IsZero() {
			return t.ZeroUid, nil, errors.New("internal: invalid Uid")
		}
	} else {
		// User not found
		return t.ZeroUid, nil, nil
	}

	return user.Uid(), user.Passhash, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *RethinkDbAdapter) UserGet(appid uint32, uid t.Uid) (*t.User, error) {
	if row, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Run(a.conn); err == nil && !row.IsNil() {
		var user t.User
		if err = row.One(&user); err == nil {
			user.Passhash = nil
			return &user, nil
		}
		return nil, err
	} else {
		// If user does not exist, it returns nil, nil
		return nil, err
	}
}

func (a *RethinkDbAdapter) UserGetAll(appId uint32, ids ...t.Uid) ([]t.User, error) {
	uids := []interface{}{}
	for _, id := range ids {
		uids = append(uids, id.String())
	}

	users := []t.User{}
	if rows, err := rdb.DB(a.dbName).Table("users").GetAll(uids...).Run(a.conn); err != nil {
		return nil, err
	} else {
		var user t.User
		for rows.Next(&user) {
			user.Passhash = nil
			users = append(users, user)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (a *RethinkDbAdapter) UserFind(appId uint32, params map[string]interface{}) ([]t.User, error) {
	return nil, errors.New("UserFind: not implemented")
}

func (a *RethinkDbAdapter) UserDelete(appId uint32, id t.Uid, soft bool) error {
	return errors.New("UserDelete: not implemented")
}

func (a *RethinkDbAdapter) UserUpdateStatus(appid uint32, uid t.Uid, status interface{}) error {
	update := map[string]interface{}{"Status": status}

	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

func (a *RethinkDbAdapter) ChangePassword(appid uint32, id t.Uid, password string) error {

	return nil
}

func (a *RethinkDbAdapter) UserUpdate(appid uint32, uid t.Uid, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("users").Get(uid.String()).Update(update).RunWrite(a.conn)
	return err
}

// *****************************

// TopicCreate creates a topic from template
func (a *RethinkDbAdapter) TopicCreate(appId uint32, topic *t.Topic) error {
	// Validated unique username, inserting user now
	topic.SetUid(uGen.Get())
	_, err := rdb.DB(a.dbName).Table("topics").Insert(&topic).RunWrite(a.conn)
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *RethinkDbAdapter) TopicCreateP2P(appId uint32, initiator, invited *t.Subscription) error {
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
		if !strings.Contains(err.Error(), "Duplicate primary key") {
			return err
		}
	}

	topic := &t.Topic{
		Name:   initiator.Topic,
		Access: t.DefaultAccess{Auth: t.ModeBanned, Anon: t.ModeBanned}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	return a.TopicCreate(appId, topic)
}

func (a *RethinkDbAdapter) TopicGet(appid uint32, topic string) (*t.Topic, error) {
	// Fetch topic by name
	rows, err := rdb.DB(a.dbName).Table("topics").GetAllByIndex("Name", topic).Run(a.conn)
	if err != nil {
		return nil, err
	}

	if rows.IsNil() {
		return nil, nil
	}

	var tt = new(t.Topic)
	if err = rows.One(tt); err != nil {
		return nil, err
	}

	return tt, rows.Err()
}

// TopicsForUser loads user's topics and contacts
func (a *RethinkDbAdapter) TopicsForUser(appid uint32, uid t.Uid, opts *t.BrowseOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	// Subscription have Topic.UpdatedAt denormalized into Subscription.UpdatedAt
	q := rdb.DB(a.dbName).Table("subscriptions")
	q = addLimitAndFilter(q, uid.String(), "User_UpdatedAt", opts)

	//log.Printf("RethinkDbAdapter.TopicsForUser q: %+v", q)
	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: to users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	var sub t.Subscription
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]interface{}, 0, 16)
	usrq := make([]interface{}, 0, 16)
	for rows.Next(&sub) {
		// 'me' subscription, skip
		if strings.HasPrefix(sub.Topic, "usr") {
			continue

			// p2p subscription, find the other user to get user.Public
		} else if strings.HasPrefix(sub.Topic, "p2p") {
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
		rows, err = rdb.DB(a.dbName).Table("topics").GetAllByIndex("Name", topq...).Run(a.conn)
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for rows.Next(&top) {
			sub = join[top.Name]
			sub.ObjHeader.MergeTimes(&top.ObjHeader)
			sub.SetLastMessageAt(top.LastMessageAt)
			if strings.HasPrefix(sub.Topic, "grp") {
				// all done with a grp topic
				sub.SetPublic(top.Public)
				subs = append(subs, sub)
			} else {
				// put back the updated value of a p2p subsription, will process further below
				join[top.Name] = sub
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
				sub.SetWith(uid2.UserId())
				sub.SetPublic(usr.Public)
				subs = append(subs, sub)
			}
		}

		//log.Printf("RethinkDbAdapter.TopicsForUser 2: %#+v", subs)
	}

	return subs, nil
}

// UsersForTopic loads users subscribed to the given topic
func (a *RethinkDbAdapter) UsersForTopic(appid uint32, topic string, opts *t.BrowseOpt) ([]t.Subscription, error) {
	// Fetch topic subscribers
	// Fetch all subscribed users. The number of users is not large
	q := rdb.DB(a.dbName).Table("subscriptions")
	q = addLimitAndFilter(q, topic, "Topic_UpdatedAt", nil)

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

func (a *RethinkDbAdapter) TopicShare(appid uint32, shares []t.Subscription) (int, error) {
	// Assign Ids
	for i := 0; i < len(shares); i++ {
		shares[i].Id = shares[i].Topic + ":" + shares[i].User
	}

	resp, err := rdb.DB(a.dbName).Table("subscriptions").Insert(shares).RunWrite(a.conn)
	if err != nil {
		return resp.Inserted, err
	}

	return resp.Inserted, nil
}

func (a *RethinkDbAdapter) TopicDelete(appId uint32, userDbId, topic string) error {
	return errors.New("TopicDelete: not implemented")
}

func (a *RethinkDbAdapter) TopicUpdateOnMessage(appid uint32, topic string, msg *t.Message) error {
	update := struct {
		SeqId         int
		LastMessageAt *time.Time
	}{msg.SeqId, &msg.CreatedAt}

	// Invite - 'me' topic
	var err error
	if strings.HasPrefix(topic, "usr") {
		_, err = rdb.DB("tinode").Table("users").Get(topic).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

		// All other messages
	} else {
		_, err = rdb.DB("tinode").Table("topics").GetAllByIndex("Name", topic).
			Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)
	}

	return err
}

// UpdateLastSeen records the time when a session with a given device ID detached from a topic
func (a *RethinkDbAdapter) UpdateLastSeen(appid uint32, topic string, user t.Uid, tag string, when time.Time) error {

	update := struct {
		LastSeen map[string]time.Time
	}{map[string]time.Time{tag: when}}
	_, err := rdb.DB("tinode").Table("subscriptions").Get(topic+":"+user.String()).
		Update(update, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

func (a *RethinkDbAdapter) TopicUpdate(appid uint32, topic string, update map[string]interface{}) error {
	_, err := rdb.DB("tinode").Table("topics").GetAllByIndex("Name", topic).Update(update).RunWrite(a.conn)
	return err
}

// Get a subscription of a user to a topic
func (a *RethinkDbAdapter) SubscriptionGet(appid uint32, topic string, user t.Uid) (*t.Subscription, error) {

	rows, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Run(a.conn)
	if err != nil {
		return nil, err
	}

	var sub t.Subscription
	err = rows.One(&sub)
	return &sub, rows.Err()
}

// Update time when the user was last attached to the topic
func (a *RethinkDbAdapter) SubsLastSeen(appid uint32, topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic+":"+user.String()).
		Update(map[string]interface{}{"LastSeen": lastSeen}, rdb.UpdateOpts{Durability: "soft"}).RunWrite(a.conn)

	return err
}

// SubsForUser loads a list of user's subscriptions to topics
func (a *RethinkDbAdapter) SubsForUser(appid uint32, forUser t.Uid, opts *t.BrowseOpt) ([]t.Subscription, error) {
	if forUser.IsZero() {
		return nil, errors.New("RethinkDb adapter: invalid user ID in TopicGetAll")
	}

	q := rdb.DB(a.dbName).Table("subscriptions")
	q = addLimitAndFilter(q, forUser.String(), "User_UpdatedAt", opts)

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
func (a *RethinkDbAdapter) SubsForTopic(appId uint32, topic string, opts *t.BrowseOpt) ([]t.Subscription, error) {
	//log.Println("Loading subscriptions for topic ", topic)

	// must load User.Public for p2p topics
	var p2p []t.User
	if strings.HasPrefix(topic, "p2p") {
		uid1, uid2, _ := t.ParseP2P(topic)
		if p2p, err := a.UserGetAll(appId, uid1, uid2); err != nil {
			return nil, err
		} else if len(p2p) != 2 {
			return nil, errors.New("failed to load two p2p users")
		}
	}

	q := rdb.DB(a.dbName).Table("subscriptions")
	q = addLimitAndFilter(q, topic, "Topic_UpdatedAt", opts)
	//log.Println("Loading subscription q=", q)

	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next(&ss) {
		if p2p != nil {
			if p2p[0].Id == ss.User {
				ss.SetPublic(p2p[1].Public)
				ss.SetWith(p2p[1].Id)
			} else {
				ss.SetPublic(p2p[0].Public)
				ss.SetWith(p2p[0].Id)
			}
		}
		subs = append(subs, ss)
		//log.Printf("SubsForTopic: loaded sub %#+v", ss)
	}
	return subs, rows.Err()
}

// Update a single subscription.
func (a *RethinkDbAdapter) SubsUpdate(appid uint32, topic string, user t.Uid, update map[string]interface{}) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Update(update).RunWrite(a.conn)
	return err
}

// Delete a subscription.
func (a *RethinkDbAdapter) SubsDelete(appid uint32, topic string, user t.Uid) error {
	_, err := rdb.DB(a.dbName).Table("subscriptions").Get(topic + ":" + user.String()).Delete().RunWrite(a.conn)
	return err
}

// Messages
func (a *RethinkDbAdapter) MessageSave(appId uint32, msg *t.Message) error {
	msg.SetUid(uGen.Get())
	_, err := rdb.DB(a.dbName).Table("messages").Insert(msg).RunWrite(a.conn)
	return err
}

func (a *RethinkDbAdapter) MessageGetAll(appId uint32, topic string, opts *t.BrowseOpt) ([]t.Message, error) {
	//log.Println("Loading messages for topic ", topic)

	q := rdb.DB(a.dbName).Table("messages")
	q = addLimitAndFilter(q, topic, "Topic_CreatedAt", opts)

	rows, err := q.Run(a.conn)
	if err != nil {
		return nil, err
	}

	var msgs []t.Message
	var mm t.Message
	for rows.Next(&mm) {
		msgs = append(msgs, mm)
	}
	return msgs, rows.Err()
}

func (a *RethinkDbAdapter) MessageDelete(appId uint32, id t.Uid) error {
	return errors.New("MessageDelete: not implemented")
}

func addLimitAndFilter1(q rdb.Term, value string, index string, opts *t.BrowseOpt) rdb.Term {
	var limit uint = 1024 // TODO(gene): pass into adapter as a config param
	var lower, upper interface{}
	var order rdb.Term

	if opts != nil {
		if !opts.Since.IsZero() {
			lower = opts.Since
		} else {
			lower = rdb.MinVal
		}

		if !opts.Before.IsZero() {
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

		if opts.AscOrder {
			order = rdb.Asc(index)
		} else {
			order = rdb.Desc(index)
		}
	} else {
		lower = []interface{}{value, rdb.MinVal}
		upper = []interface{}{value, rdb.MaxVal}
		order = rdb.Desc(index)
	}

	return q.Between(lower, upper, rdb.BetweenOpts{Index: index}).
		OrderBy(rdb.OrderByOpts{Index: order}).Limit(limit)
}

/*
func remapP2PTopic(topic string, user t.Uid) (string, error) {
	if strings.HasPrefix(topic, "p2p") {
		uid1, uid2, err := t.ParseP2P(topic)
		if err != nil {
			return "", err
		}
		if user == uid1 {
			topic = uid2.UserId()
		} else {
			topic = uid1.UserId()
		}
	}
	return topic, nil
}
*/

func init() {
	store.Register("rethinkdb", &RethinkDbAdapter{})
}
