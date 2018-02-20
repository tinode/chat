// +build mysql

package mysql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	ms "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// adapter holds RethinkDb connection data.
type adapter struct {
	db      *sqlx.DB
	dbName  string
	version int
}

const (
	defaultDSN      = "root:@tcp(localhost:3306)/tinode?parseTime=true"
	defaultDatabase = "tinode"

	dbVersion = 100

	adapterName = "mysql"
)

type configType struct {
	DSN    string `json:"dsn,omitempty"`
	DBName string `json:database,omitempty"`
}

const (
	// Maximum number of records to return
	maxResults = 1024
	// Maximum number of topic subscribers to return
	maxSubscribers = 256
)

// Open initializes rethinkdb session
func (a *adapter) Open(jsonconfig string) error {
	if a.db != nil {
		return errors.New("mysql adapter is already connected")
	}

	var err error
	var config configType

	if err = json.Unmarshal([]byte(jsonconfig), &config); err != nil {
		return errors.New("mysql adapter failed to parse config: " + err.Error())
	}

	dsn := config.DSN
	if dsn == "" {
		dsn = defaultDSN
	}

	a.dbName = config.DBName
	if a.dbName == "" {
		a.dbName = defaultDatabase
	}

	a.db, err = sqlx.Open("mysql", dsn)
	if err != nil {
		return err
	}

	// sql.Open does not open the network connection.
	// Force network connection here.
	err = a.db.Ping()
	if err != nil {
		return err
	}

	a.version = -1

	return nil
}

// Close closes the underlying database connection
func (a *adapter) Close() error {
	var err error
	if a.db != nil {
		err = a.db.Close()
		a.db = nil
		a.version = -1
	}
	return err
}

// IsOpen returns true if connection to database has been established. It does not check if
// connection is actually live.
func (a *adapter) IsOpen() bool {
	return a.db != nil
}

// Read current database version
func (a *adapter) getDbVersion() (int, error) {
	var vers int
	err := a.db.Get(&vers, "SELECT `value` FROM kvmeta WHERE `key`='version'")
	if err != nil {
		return -1, err
	}
	a.version = vers

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

// CreateDb initializes the storage.
func (a *adapter) CreateDb(reset bool) error {
	// Checks if database exists.
	var db interface{}
	err := a.db.Get(&db, "SHOW DATABASES LIKE '"+a.dbName+"'")
	if err == sql.ErrNoRows {
		return errors.New("unsupported: use schema.sql to create database")
	}
	return err
}

// Indexable tag as stored in 'tagunique'
type storedTag struct {
	Id     string
	Source string
}

// UserCreate creates a new user. Returns error and true if error is due to duplicate user name,
// false for any other error
func (a *adapter) UserCreate(user *t.User) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			log.Println("transaction failed", err)
			tx.Rollback()
		}
	}()

	decoded_uid := store.DecodeUid(user.Uid())
	_, err = tx.Exec("INSERT INTO users(id,createdAt,updatedAt,access,public,tags) VALUES(?,?,?,?,?,?)",
		decoded_uid,
		user.CreatedAt, user.UpdatedAt,
		toJSON(user.Access),
		toJSON(user.Public), toJSON(user.Tags))

	if err != nil {
		return err
	}

	// Save user's tags to a separate table to ensure uniquness
	if len(user.Tags) > 0 {
		var insert *sql.Stmt
		insert, err = tx.Prepare("INSERT INTO usertags(userid, tag) VALUES(?,?)")
		if err != nil {
			return err
		}

		for _, t := range user.Tags {
			_, err = insert.Exec(decoded_uid, t)

			if err != nil {
				if isDupe(err) {
					err = errors.New("duplicate tag(s)")
				}
				return err
			}
		}
	}

	return tx.Commit()
}

// Add user's authentication record
func (a *adapter) AddAuthRecord(uid t.Uid, authLvl int, unique string,
	secret []byte, expires time.Time) (bool, error) {

	var exp *time.Time
	if !expires.IsZero() {
		exp = &expires
	}
	_, err := a.db.Exec("INSERT INTO basicauth(login,userid,authLvl,secret,expires) VALUES(?,?,?,?,?)",
		unique, store.DecodeUid(uid), authLvl, secret, exp)
	if err != nil {
		if isDupe(err) {
			return true, errors.New("duplicate credential")
		}
		return false, err
	}
	return false, nil
}

// Delete user's authentication record
func (a *adapter) DelAuthRecord(unique string) (int, error) {
	res, err := a.db.Exec("DELETE FROM auth WHERE `unique`=?", unique)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()

	return int(count), nil
}

// Delete user's all authentication records
func (a *adapter) DelAllAuthRecords(uid t.Uid) (int, error) {
	res, err := a.db.Exec("DELETE FROM auth WHERE userid=?", uid)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()

	return int(count), nil
}

// Update user's authentication secret
func (a *adapter) UpdAuthRecord(unique string, authLvl int, secret []byte, expires time.Time) (int, error) {
	res, err := a.db.Exec("UPDATE auth SET authLvl=?,secret=?,expires=? WHERE login=?",
		authLvl, secret, expires, unique)

	if err != nil {
		return 0, err
	}

	count, _ := res.RowsAffected()
	return int(count), nil
}

// Retrieve user's authentication record
func (a *adapter) GetAuthRecord(unique string) (t.Uid, int, []byte, time.Time, error) {
	var expires time.Time

	var record struct {
		Userid  int64
		Authlvl int
		Secret  []byte
		Expires *time.Time
	}

	err := a.db.Get(&record, "SELECT userid, secret, expires, authlvl FROM basicauth WHERE login=?", unique)
	if err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return t.ZeroUid, 0, nil, expires, err
	}

	if record.Expires != nil {
		expires = *record.Expires
	}

	// log.Println("loggin in user Id=", user.Uid(), user.Id)
	return store.EncodeUid(record.Userid), record.Authlvl, record.Secret, expires, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *adapter) UserGet(uid t.Uid) (*t.User, error) {
	var user t.User
	err := a.db.Get(&user, "SELECT * FROM users WHERE id=?", store.DecodeUid(uid))
	if err == nil {
		user.SetUid(uid)
		user.Public = fromJSON(user.Public)
		return &user, nil
	}

	if err == sql.ErrNoRows {
		// Clear the error if user does not exist
		err = nil
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
	if rows, err := a.db.Query("SELECT * FROM users WHERE id IN (?)", uids); err == nil {
		defer rows.Close()

		var user t.User
		for err = rows.Scan(&user); err == nil; {
			users = append(users, user)
		}

		if err != nil {
			return nil, err
		}

	} else {
		return nil, err
	}

	return users, nil
}

func (a *adapter) UserDelete(uid t.Uid, soft bool) error {
	var err error
	if soft {
		now := t.TimeNow()
		_, err = a.db.Exec("UPDATE users set updatedAt=?, deletedAt=? WHERE id=?", now, now, uid)
	} else {
		_, err = a.db.Exec("DELETE FROM users WHERE id=?", uid)
	}
	return err
}

func (a *adapter) UserUpdateLastSeen(uid t.Uid, userAgent string, when time.Time) error {
	_, err := a.db.Exec("UPDATE users SET lastseen=?, useragent=? WHERE id=?", when, userAgent, uid)

	return err
}

// UserUpdate updates user object. Use UserTagsUpdate when updating Tags.
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	_, err := a.db.Exec("UPDATE users SET var=? WHERE id=?", update, uid)
	return err
}

// *****************************

func (a *adapter) topicCreate(tx *sqlx.Tx, topic *t.Topic) error {
	var err error
	q := "INSERT INTO topics(createdAt,updatedAt,name,access,public,tags) VALUES(?,?,?,?,?,?)"
	if tx == nil {
		_, err = a.db.Exec(q, topic.CreatedAt, topic.UpdatedAt, topic.Id,
			toJSON(topic.Access), toJSON(topic.Public), toJSON(topic.Tags))
	} else {
		_, err = tx.Exec(q, topic.CreatedAt, topic.UpdatedAt, topic.Id,
			toJSON(topic.Access), toJSON(topic.Public), toJSON(topic.Tags))
	}

	// FIXME(gene): handle tags

	return err
}

// TopicCreate saves topic object to database.
func (a *adapter) TopicCreate(topic *t.Topic) error {
	return a.topicCreate(nil, topic)
}

// If upsert = true - update subscription on duplicate key, otherwise ignore the duplicate.
func createSubscription(tx *sqlx.Tx, sub *t.Subscription, upsert bool) error {

	jpriv := toJSON(sub.Private)
	_, err := tx.Exec(
		"INSERT INTO subscriptions(createdAt,updatedAt,deletedAt,userid,topic,modeWant,modeGiven,private) "+
			"VALUES(?,?,NULL,?,?,?,?,?)",
		sub.CreatedAt, sub.UpdatedAt,
		store.DecodeUid(t.ParseUid(sub.User)), sub.Topic,
		sub.ModeWant.String(), sub.ModeGiven.String(),
		jpriv)

	if err != nil && isDupe(err) {
		if upsert {
			_, err = tx.Exec(
				"UPDATE subscriptions SET createdAt=?,updatedAt=?,deletedAt=NULL,modeWant=?,modeGiven=?,private=?",
				sub.CreatedAt, sub.UpdatedAt,
				sub.ModeWant.String(), sub.ModeGiven.String(),
				jpriv)
		} else {
			err = nil
		}
	}
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	err = createSubscription(tx, initiator, true)
	if err != nil {
		return err
	}

	err = createSubscription(tx, invited, false)
	if err != nil {
		return err
	}

	topic := &t.Topic{ObjHeader: t.ObjHeader{Id: initiator.Topic}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	err = a.topicCreate(tx, topic)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	// Fetch topic by name
	var tt = new(t.Topic)
	err := a.db.Get(tt,
		"SELECT createdat,updatedat,deletedat,name AS id,access,seqid,delid,public,tags FROM topics WHERE name=?",
		topic)

	if err != nil {
		return nil, err
	}

	tt.Public = fromJSON(tt.Public)

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' subscription.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	q := `SELECT createdat,updatedat,deletedat,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`
	if !keepDeleted {
		// Filter out rows with defined DeletedAt
		q += " AND deletedAt IS NULL"
	}
	q += " LIMIT ?"

	rows, err := a.db.Queryx(q, store.DecodeUid(uid), maxResults)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (me & p2p) and topics table (p2p and grp).
	// Prepare a list of Separate subscriptions to users vs topics
	var sub t.Subscription
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]interface{}, 0, 16)
	usrq := make([]interface{}, 0, 16)
	for rows.Next() {
		if err = rows.StructScan(&sub); err != nil {
			break
		}

		sub.User = uid.String()
		tcat := t.GetTopicCat(sub.Topic)

		// 'me' or 'fnd' subscription, skip
		if tcat == t.TopicCatMe || tcat == t.TopicCatFnd {
			continue

			// p2p subscription, find the other user to get user.Public
		} else if tcat == t.TopicCatP2P {
			uid1, uid2, _ := t.ParseP2P(sub.Topic)
			if uid1 == uid {
				usrq = append(usrq, store.DecodeUid(uid2))
			} else {
				usrq = append(usrq, store.DecodeUid(uid1))
			}
			topq = append(topq, sub.Topic)

			// grp subscription
		} else {
			topq = append(topq, sub.Topic)
		}
		sub.Private = fromJSON(sub.Private)
		join[sub.Topic] = sub
	}
	rows.Close()

	if err != nil {
		return nil, err
	}

	//log.Printf("TopicsForUser topq, usrq: %+v, %+v", topq, usrq)

	var subs []t.Subscription
	if len(topq) > 0 || len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(join))
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		q, _, _ := sqlx.In(
			"SELECT createdat,updatedat,deletedat,name AS id,access,seqid,delid,public,tags "+
				"FROM topics WHERE name IN (?)", topq)
		rows, err = a.db.Queryx(q, topq...)
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for rows.Next() {
			if err = rows.StructScan(&top); err != nil {
				break
			}

			sub = join[top.Id]
			sub.ObjHeader.MergeTimes(&top.ObjHeader)
			sub.SetSeqId(top.SeqId)
			// sub.SetDelId(top.DelId)
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				// all done with a grp topic
				sub.SetPublic(fromJSON(top.Public))
				subs = append(subs, sub)
			} else {
				// put back the updated value of a p2p subsription, will process further below
				join[top.Id] = sub
			}
		}
		rows.Close()
	}

	// Fetch p2p users and join to p2p tables
	if err == nil && len(usrq) > 0 {
		q, _, _ := sqlx.In("SELECT * FROM users WHERE id IN (?)", usrq)
		rows, err = a.db.Queryx(q, usrq...)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for rows.Next() {
			if err = rows.StructScan(&usr); err != nil {
				break
			}
			uid2 := encodeString(usr.Id)
			topic := uid.P2PName(uid2)
			if sub, ok := join[topic]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetPublic(fromJSON(usr.Public))
				sub.SetWith(uid2.UserId())
				sub.SetDefaultAccess(usr.Access.Auth, usr.Access.Anon)
				sub.SetLastSeenAndUA(usr.LastSeen, usr.UserAgent)
				subs = append(subs, sub)
			}
		}
		rows.Close()
	}
	return subs, err
}

// UsersForTopic loads users subscribed to the given topic
func (a *adapter) UsersForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	// Fetch all subscribed users. The number of users is not large
	q := `SELECT s.createdat,s.updatedat,s.deletedat,s.userid,s.topic,s.delid,s.recvseqid,
		s.readseqid,s.modewant,s.modegiven,u.public,s.private
		FROM subscriptions AS s JOIN users AS u ON s.userid=u.id 
		WHERE s.topic=?`
	if !keepDeleted {
		// Filter out rows with DeletedAt being not null
		q += " AND s.deletedAt IS NULL"
	}
	q += " LIMIT ?"
	rows, err := a.db.Queryx(q, topic, maxSubscribers)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	var public interface{}
	for rows.Next() {
		if err = rows.Scan(
			&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt,
			&sub.User, &sub.Topic, &sub.DelId, &sub.RecvSeqId,
			&sub.ReadSeqId, &sub.ModeWant, &sub.ModeGiven,
			&public, &sub.Private); err != nil {
			break
		}
		sub.User = encodeString(sub.User).String()
		sub.Private = fromJSON(sub.Private)
		sub.SetPublic(fromJSON(public))
		subs = append(subs, sub)
	}
	rows.Close()

	log.Println("UsersForTopic 3", err, len(subs))
	return subs, err
}

func (a *adapter) TopicShare(shares []*t.Subscription) (int, error) {
	tx, err := a.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for _, sub := range shares {
		err = createSubscription(tx, sub, true)
		if err != nil {
			return 0, err
		}
	}

	return len(shares), tx.Commit()
}

func (a *adapter) TopicDelete(topic string) error {
	_, err := a.db.Exec("DELETE FROM topics WHERE name=?", topic)
	return err
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	_, err := a.db.Exec("UPDATE topics SET seqid=? WHERE name=?", msg.SeqId, topic)

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	_, err := a.db.Exec("UPDATE topics SET ? WHERE name=?", update, topic)
	return err
}

// Get a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {
	var sub t.Subscription
	err := a.db.Get(&sub, "SELECT * FROM subscriptions WHERE topic=? AND userid=", topic, store.DecodeUid(user))
	if err != nil {
		return nil, err
	}

	if sub.DeletedAt != nil {
		return nil, nil
	}
	return &sub, nil
}

// Update time when the user was last attached to the topic
func (a *adapter) SubsLastSeen(topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := a.db.Exec("UPDATE subscriptions SET lastseen=? WHERE id=?", lastSeen, topic+":"+user.String())

	return err
}

// SubsForUser loads a list of user's subscriptions to topics. Does NOT read Public value.
func (a *adapter) SubsForUser(forUser t.Uid, keepDeleted bool) ([]t.Subscription, error) {
	if forUser.IsZero() {
		return nil, errors.New("mysql adapter: invalid user ID in SubsForUser")
	}

	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`
	if !keepDeleted {
		q += " AND deletedAt IS NULL"
	}
	q += " LIMIT ?"

	rows, err := a.db.Queryx(q, store.DecodeUid(forUser), maxResults)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next() {
		if err = rows.StructScan(&ss); err != nil {
			break
		}
		ss.User = forUser.String()
		subs = append(subs, ss)
	}
	rows.Close()

	return subs, err
}

// SubsForTopic fetches all subsciptions for a topic.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool) ([]t.Subscription, error) {
	// log.Println("Loading subscriptions for topic ", topic)

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

	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=?`
	if !keepDeleted {
		// Filter out rows where DeletedAt is defined
		q += " AND deletedAt IS NULL"
	}
	q += " LIMIT ?"

	rows, err := a.db.Queryx(q, topic, maxSubscribers)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next() {
		if err = rows.StructScan(&ss); err != nil {
			break
		}

		ss.User = sub.User(ss.User).String()
		ss.Private = fromJSON(ss.Private)
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
		// log.Printf("SubsForTopic: loaded sub %#+v", ss)
	}
	rows.Close()

	return subs, err
}

// SubsUpdate updates a single subscription.
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	var args []interface{}
	var cols []string
	for col, arg := range update {
		cols = append(cols, col+"=?")
		// Maps, slices and structs (except time.Time) should be converted to JSON.
		if _, ok := arg.(time.Time); !ok {
			switch reflect.ValueOf(arg).Kind() {
			case reflect.Map, reflect.Struct, reflect.Slice:
				arg = toJSON(arg)
			}
		}
		args = append(args, arg)
	}
	q := "UPDATE subscriptions SET " + strings.Join(cols, ",") + " WHERE "
	if !user.IsZero() {
		// Update one topic subscription
		q += "userid=?"
		args = append(args, store.DecodeUid(user))
	} else {
		// Update all topic subscriptions
		q += "topic=?"
		args = append(args, topic)
	}

	_, err := a.db.Exec(q, args...)

	return err
}

// SubsDelete marks subscription as deleted.
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	_, err := a.db.Exec("UPDATE subscriptions SET updatedAt=?, deletedAt=? WHERE topic=? AND userid=?",
		now, now, topic, store.DecodeUid(user))
	return err
}

// SubsDelForTopic marks all subscriptions to the given topic as deleted
func (a *adapter) SubsDelForTopic(topic string) error {
	now := t.TimeNow()
	_, err := a.db.Exec("UPDATE subscriptions SET updatedAt=?, deletedAt=? WHERE topic=?", now, now, topic)
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
	// Use JOIN users  -> tags
	rows, err := a.db.Query("SELECT id, access, createdAt, updatedAt, public, tags "+
		"FROM users WHERE tags IN (?) GROUP BY id ORDER BY matchCount LIMIT ?",
		query, maxResults)

	if err != nil {
		return nil, err
	}

	var user t.User
	var sub t.Subscription
	var subs []t.Subscription
	for err = rows.Scan(&user); err == nil; {
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
	rows.Close()

	return subs, err

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
	rows, err := a.db.Query(
		"SELECT t.id, t.access, t.createdAt, t.updatedAt, t.public, t.tags, count(*) AS matchcount "+
			"FROM topics AS t, topictags AS tt "+
			"WHERE tt.tag IN (?) AND t.name=tt.topic "+
			"GROUP BY t.id, t.access, t.createdAt, t.updatedAt, t.public, t.tags "+
			"ORDER BY matchcount DESC LIMIT ?", query, maxResults)

	if err != nil {
		return nil, err
	}

	var topic t.Topic
	var sub t.Subscription
	var subs []t.Subscription
	for err = rows.Scan(&topic); err != nil; {
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
	rows.Close()

	if err != nil {
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
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if added != nil && len(added) > 0 {
		toAdd := make([]storedTag, 0, len(added))
		for _, tag := range added {
			toAdd = append(toAdd, storedTag{Id: tag, Source: source})
		}

		_, err = tx.Exec("INSERT INTO tagunique() VALUES(?)", toAdd)
		if err != nil {
			if isDupe(err) {
				return errors.New("duplicate tag(s)")
			}
			return err
		}
	}

	if removed != nil && len(removed) > 0 {
		_, err = a.db.Exec("DELETE FROM tagunique WHERE tag IN (?) AND source=?", removed, source)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
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
	_, err := a.db.Exec(
		"INSERT INTO messages(createdAt,updatedAt,seqid,topic,`from`,head,content) VALUES(?,?,?,?,?,?,?)",
		msg.CreatedAt, msg.UpdatedAt,
		msg.SeqId, msg.Topic,
		store.DecodeUid(t.ParseUid(msg.From)),
		toJSON(msg.Head), toJSON(msg.Content))
	return err
}

func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.BrowseOpt) ([]t.Message, error) {
	var limit = maxResults // TODO(gene): pass into adapter as a config param
	var lower = 0
	var upper = 1 << 31

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

	// FIXME(gene): filter out soft-deleted messages
	rows, err := a.db.Queryx("SELECT * FROM messages WHERE topic=? AND seqid BETWEEN ? AND ? "+
		"AND delid=0 ORDER BY seqid DESC LIMIT ?",
		topic, lower, upper, limit)

	if err != nil {
		return nil, err
	}

	var msgs []t.Message
	var msg t.Message
	for rows.Next() {
		if err = rows.StructScan(&msg); err != nil {
			break
		}
		msg.From = encodeString(msg.From).String()
		msg.Content = fromJSON(msg.Content)
		msgs = append(msgs, msg)
	}
	rows.Close()

	return msgs, err
}

// Get ranges of deleted messages
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.BrowseOpt) ([]t.DelMessage, error) {
	var limit = maxResults
	var lower = 0
	var upper = 1 << 31

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
	rows, err := a.db.Query("SELECT * FROM dellog WHERE topic=? AND delid BETWEEN ? and ? "+
		"AND (deletedFor IS NULL OR deletedFor=?)"+
		"ORDER BY delid LIMIT ?", topic, lower, upper, store.DecodeUid(forUser), limit)
	if err != nil {
		return nil, err
	}

	var dmsgs []t.DelMessage
	if err = rows.Scan(&dmsgs); err != nil {
		return nil, err
	}

	return dmsgs, nil
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) (err error) {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if toDel == nil {
		// Whole topic is being deleted, thus also deleting all messages
		_, err = a.db.Exec("DELETE FROM messages WHERE topic=?", topic)
	} else {
		// Only some messages are being deleted
		toDel.SetUid(store.GetUid())

		// Start with making a log entry
		_, err = a.db.Exec("INSERT INTO dellog() VALUES(?)", toDel)
		if err != nil {
			return err
		}
		/*
			where := ""
			if len(toDel.SeqIdRanges) > 1 || toDel.SeqIdRanges[0].Hi <= toDel.SeqIdRanges[0].Low {
				var indexVals []int
				for _, rng := range toDel.SeqIdRanges {
					if rng.Hi == 0 {
						indexVals = append(indexVals, rng.Low)
					} else {
						for i := rng.Low; i <= rng.Hi; i++ {
							indexVals = append(indexVals, i)
						}
					}
				}
				where = "topic=? AND seqId IN (?)"
			} else {
				// Optimizing for a special case of single range low..hi
				where = "topic=? AND seqId BETWEEN ? AND ?", toDel.SeqIdRanges[0].Low, toDel.SeqIdRanges[0].Hi
			}

			if toDel.DeletedFor == "" {
				// Hard-deleting for all users
				// Hard-delete of individual messages. Mark some messages as deleted.
				_, err = a.db.Exec("UPDATE messages SET deletedAt=?, delId=? head=NULL, content=NULL WHERE "+
					where+
					" AND deletedAt IS NULL", t.TimeNow(), toDel.DelId)
			} else {
				// Handle Soft-deleting messages
			}
		*/
	}

	if err != nil {
		return err
	}

	return tx.Commit()
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

	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Ensure uniqueness of the device ID: delete all records of the device ID
	_, err = a.db.Exec("DELETE FROM devices WHERE hash=?", hash)
	if err != nil {
		return err
	}

	// Actually add/update DeviceId for the new user
	_, err = a.db.Exec("INSERT INTO devices(userid, hash, deviceId) VALUES(?,?,?)", uid, hash, def.DeviceId)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (a *adapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	var unums []interface{}
	for _, uid := range uids {
		unums = append(unums, store.DecodeUid(uid))
	}

	q, _, _ := sqlx.In("SELECT userid,deviceid,platform,lastseen,lang FROM devices WHERE userid IN (?)", unums)
	rows, err := a.db.Queryx(q, unums...)
	if err != nil {
		return nil, 0, err
	}

	var device struct {
		Userid   int64
		Deviceid string
		Platform string
		Lastseen time.Time
		Lang     string
	}

	result := make(map[t.Uid][]t.DeviceDef)
	count := 0
	for rows.Next() {
		if err = rows.StructScan(&device); err != nil {
			break
		}
		uid := store.EncodeUid(device.Userid)
		udev := result[uid]
		if udev == nil {
			udev = []t.DeviceDef{}
		}
		udev = append(udev, t.DeviceDef{
			DeviceId: device.Deviceid,
			Platform: device.Platform,
			LastSeen: device.Lastseen,
			Lang:     device.Lang,
		})
		result[uid] = udev
		count++
	}
	rows.Close()

	return result, count, err
}

func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	_, err := a.db.Exec("DELETE FROM devices WHERE userid=? AND hash=?", uid, deviceHasher(deviceID))
	return err
}

func isDupe(err error) bool {
	myerr, ok := err.(*ms.MySQLError)
	return ok && myerr.Number == 1062
}

func toJSON(src interface{}) []byte {
	if src == nil {
		return nil
	}

	jval, _ := json.Marshal(src)
	return jval
}

func fromJSON(src interface{}) interface{} {
	if src == nil {
		return nil
	}
	if bb, ok := src.([]byte); ok {
		var out interface{}
		json.Unmarshal(bb, &out)
		return out
	}
	return nil
}

func encodeString(str string) t.Uid {
	unum, _ := strconv.ParseInt(str, 10, 64)
	return store.EncodeUid(unum)
}

func init() {
	store.Register(adapterName, &adapter{})
}
