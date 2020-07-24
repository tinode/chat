// +build mysql

// Package mysql is a database adapter for MySQL.
package mysql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	ms "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// adapter holds MySQL connection data.
type adapter struct {
	db     *sqlx.DB
	dsn    string
	dbName string
	// Maximum number of records to return
	maxResults int
	// Maximum number of message records to return
	maxMessageResults int
	version           int
}

const (
	defaultDSN      = "root:@tcp(localhost:3306)/tinode?parseTime=true"
	defaultDatabase = "tinode"

	adpVersion = 111

	adapterName = "mysql"

	defaultMaxResults = 1024
	// This is capped by the Session's send queue limit (128).
	defaultMaxMessageResults = 100
)

type configType struct {
	DSN    string `json:"dsn,omitempty"`
	DBName string `json:"database,omitempty"`
}

// Open initializes database session
func (a *adapter) Open(jsonconfig json.RawMessage) error {
	if a.db != nil {
		return errors.New("mysql adapter is already connected")
	}

	var err error
	var config configType

	if err = json.Unmarshal(jsonconfig, &config); err != nil {
		return errors.New("mysql adapter failed to parse config: " + err.Error())
	}

	a.dsn = config.DSN
	if a.dsn == "" {
		a.dsn = defaultDSN
	}

	a.dbName = config.DBName
	if a.dbName == "" {
		a.dbName = defaultDatabase
	}

	if a.maxResults <= 0 {
		a.maxResults = defaultMaxResults
	}

	if a.maxMessageResults <= 0 {
		a.maxMessageResults = defaultMaxMessageResults
	}

	// This just initializes the driver but does not open the network connection.
	a.db, err = sqlx.Open("mysql", a.dsn)
	if err != nil {
		return err
	}

	// Actually opening the network connection.
	err = a.db.Ping()
	if isMissingDb(err) {
		// Ignore missing database here. If we are initializing the database
		// missing DB is OK.
		err = nil
	}
	return err
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

// GetDbVersion returns current database version.
func (a *adapter) GetDbVersion() (int, error) {
	if a.version > 0 {
		return a.version, nil
	}

	var vers int
	err := a.db.Get(&vers, "SELECT `value` FROM kvmeta WHERE `key`='version'")
	if err != nil {
		if isMissingDb(err) || isMissingTable(err) || err == sql.ErrNoRows {
			err = errors.New("Database not initialized")
		}
		return -1, err
	}

	a.version = vers

	return vers, nil
}

func (a *adapter) updateDbVersion(v int) error {
	a.version = -1
	if _, err := a.db.Exec("UPDATE kvmeta SET `value`=? WHERE `key`='version'", v); err != nil {
		return err
	}
	return nil
}

// CheckDbVersion checks whether the actual DB version matches the expected version of this adapter.
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

// Version returns adapter version.
func (adapter) Version() int {
	return adpVersion
}

// GetName returns string that adapter uses to register itself with store.
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

// CreateDb initializes the storage.
func (a *adapter) CreateDb(reset bool) error {
	var err error
	var tx *sql.Tx

	// Can't use an existing connection because it's configured with a database name which may not exist.
	// Don't care if it does not close cleanly.
	a.db.Close()

	// This DSN has been parsed before and produced no error, not checking for errors here.
	cfg, _ := ms.ParseDSN(a.dsn)
	// Clear database name
	cfg.DBName = ""

	a.db, err = sqlx.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}

	if tx, err = a.db.Begin(); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			// FIXME: This is useless: MySQL auto-commits on every CREATE TABLE.
			// Maybe DROP DATABASE instead.
			tx.Rollback()
		}
	}()

	if _, err = tx.Exec("DROP DATABASE IF EXISTS " + a.dbName); err != nil {
		return err
	}

	if _, err = tx.Exec("CREATE DATABASE " + a.dbName + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		return err
	}

	if _, err = tx.Exec("USE " + a.dbName); err != nil {
		return err
	}

	if _, err = tx.Exec(
		`CREATE TABLE users(
			id        BIGINT NOT NULL,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			state     SMALLINT NOT NULL DEFAULT 0,
			stateat   DATETIME(3),
			access    JSON,
			lastseen  DATETIME,
			useragent VARCHAR(255) DEFAULT '',
			public    JSON,
			tags      JSON,
			PRIMARY KEY(id),
			INDEX users_state_stateat(state, stateat)
		)`); err != nil {
		return err
	}

	// Indexed user tags.
	if _, err = tx.Exec(
		`CREATE TABLE usertags(
			id     INT NOT NULL AUTO_INCREMENT,
			userid BIGINT NOT NULL,
			tag    VARCHAR(96) NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id),
			INDEX usertags_tag(tag),
			UNIQUE INDEX usertags_userid_tag(userid, tag)
		)`); err != nil {
		return err
	}

	// Indexed devices. Normalized into a separate table.
	if _, err = tx.Exec(
		`CREATE TABLE devices(
			id       INT NOT NULL AUTO_INCREMENT,
			userid   BIGINT NOT NULL,
			hash     CHAR(16) NOT NULL,
			deviceid TEXT NOT NULL,
			platform VARCHAR(32),
			lastseen DATETIME NOT NULL,
			lang     VARCHAR(8),
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id),
			UNIQUE INDEX devices_hash (hash)
		)`); err != nil {
		return err
	}

	// Authentication records for the basic authentication scheme.
	if _, err = tx.Exec(
		`CREATE TABLE auth(
			id      INT NOT NULL AUTO_INCREMENT,
			uname   VARCHAR(32) NOT NULL,
			userid  BIGINT NOT NULL,
			scheme  VARCHAR(16) NOT NULL,
			authlvl INT NOT NULL,
			secret  VARCHAR(255) NOT NULL,
			expires DATETIME,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id),
			UNIQUE INDEX auth_userid_scheme(userid, scheme),
			UNIQUE INDEX auth_uname(uname)
		)`); err != nil {
		return err
	}

	// Topics
	if _, err = tx.Exec(
		`CREATE TABLE topics(
			id        INT NOT NULL AUTO_INCREMENT,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			state     SMALLINT NOT NULL DEFAULT 0,
			stateat   DATETIME(3),
			touchedat DATETIME(3),
			name      CHAR(25) NOT NULL,
			usebt     TINYINT DEFAULT 0,
			owner     BIGINT NOT NULL DEFAULT 0,
			access    JSON,
			seqid     INT NOT NULL DEFAULT 0,
			delid     INT DEFAULT 0,
			public    JSON,
			tags      JSON,
			PRIMARY KEY(id),
			UNIQUE INDEX topics_name(name),
			INDEX topics_owner(owner),
			INDEX topics_state_stateat(state, stateat)
		)`); err != nil {
		return err
	}

	// Create system topic 'sys'.
	if err = createSystemTopic(tx); err != nil {
		return err
	}

	// Indexed topic tags.
	if _, err = tx.Exec(
		`CREATE TABLE topictags(
			id    INT NOT NULL AUTO_INCREMENT,
			topic CHAR(25) NOT NULL,
			tag   VARCHAR(96) NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name),
			INDEX topictags_tag(tag),
			UNIQUE INDEX topictags_userid_tag(topic, tag)
		)`); err != nil {
		return err
	}

	// Subscriptions
	if _, err = tx.Exec(
		`CREATE TABLE subscriptions(
			id        INT NOT NULL AUTO_INCREMENT,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			deletedat DATETIME(3),
			userid    BIGINT NOT NULL,
			topic     CHAR(25) NOT NULL,
			delid     INT DEFAULT 0,
			recvseqid INT DEFAULT 0,
			readseqid INT DEFAULT 0,
			modewant  CHAR(8),
			modegiven CHAR(8),
			private   JSON,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id),
			UNIQUE INDEX subscriptions_topic_userid(topic, userid),
			INDEX subscriptions_topic(topic),
			INDEX subscriptions_deletedat(deletedat)
		)`); err != nil {
		return err
	}

	// Messages
	if _, err = tx.Exec(
		`CREATE TABLE messages(
			id        INT NOT NULL AUTO_INCREMENT,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			deletedat DATETIME(3),
			delid     INT DEFAULT 0,
			seqid     INT NOT NULL,
			topic     CHAR(25) NOT NULL,` +
			"`from`   BIGINT NOT NULL," +
			`head     JSON,
			content   JSON,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name),
			UNIQUE INDEX messages_topic_seqid(topic, seqid)
		);`); err != nil {
		return err
	}

	// Deletion log
	if _, err = tx.Exec(
		`CREATE TABLE dellog(
			id         INT NOT NULL AUTO_INCREMENT,
			topic      VARCHAR(25) NOT NULL,
			deletedfor BIGINT NOT NULL DEFAULT 0,
			delid      INT NOT NULL,
			low        INT NOT NULL,
			hi         INT NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name),
			INDEX dellog_topic_delid_deletedfor(topic,delid,deletedfor),
			INDEX dellog_topic_deletedfor_low_hi(topic,deletedfor,low,hi),
			INDEX dellog_deletedfor(deletedfor)
		);`); err != nil {
		return err
	}

	// User credentials
	if _, err = tx.Exec(
		`CREATE TABLE credentials(
			id        INT NOT NULL AUTO_INCREMENT,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			deletedat DATETIME(3),
			method    VARCHAR(16) NOT NULL,
			value     VARCHAR(128) NOT NULL,
			synthetic VARCHAR(192) NOT NULL,
			userid    BIGINT NOT NULL,
			resp      VARCHAR(255),
			done      TINYINT NOT NULL DEFAULT 0,
			retries   INT NOT NULL DEFAULT 0,
			PRIMARY KEY(id),
			UNIQUE credentials_uniqueness(synthetic),
			FOREIGN KEY(userid) REFERENCES users(id)
		);`); err != nil {
		return err
	}

	// Records of uploaded files.
	// Don't add FOREIGN KEY on userid. It's not needed and it will break user deletion.
	if _, err = tx.Exec(
		`CREATE TABLE fileuploads(
			id        BIGINT NOT NULL,
			createdat DATETIME(3) NOT NULL,
			updatedat DATETIME(3) NOT NULL,
			userid    BIGINT NOT NULL,
			status    INT NOT NULL,
			mimetype  VARCHAR(255) NOT NULL,
			size      BIGINT NOT NULL,
			location  VARCHAR(2048) NOT NULL,
			PRIMARY KEY(id)
		)`); err != nil {
		return err
	}

	// Links between uploaded files and the messages they are attached to.
	if _, err = tx.Exec(
		`CREATE TABLE filemsglinks(
			id			INT NOT NULL AUTO_INCREMENT,
			createdat	DATETIME(3) NOT NULL,
			fileid		BIGINT NOT NULL,
			msgid 		INT NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(fileid) REFERENCES fileuploads(id) ON DELETE CASCADE,
			FOREIGN KEY(msgid) REFERENCES messages(id) ON DELETE CASCADE
		)`); err != nil {
		return err
	}

	if _, err = tx.Exec(
		`CREATE TABLE kvmeta(` +
			"`key`   CHAR(32)," +
			"`value` TEXT," +
			"PRIMARY KEY(`key`)" +
			`)`); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO kvmeta(`key`, `value`) VALUES('version', ?)", adpVersion); err != nil {
		return err
	}

	return tx.Commit()
}

// UpgradeDb upgrades the database, if necessary.
func (a *adapter) UpgradeDb() error {
	bumpVersion := func(a *adapter, x int) error {
		if err := a.updateDbVersion(x); err != nil {
			return err
		}
		_, err := a.GetDbVersion()
		return err
	}

	if _, err := a.GetDbVersion(); err != nil {
		return err
	}

	if a.version == 106 {
		// Perform database upgrade from version 106 to version 107.

		if _, err := a.db.Exec("CREATE UNIQUE INDEX usertags_userid_tag ON usertags(userid, tag)"); err != nil {
			return err
		}

		if _, err := a.db.Exec("CREATE UNIQUE INDEX topictags_userid_tag ON topictags(topic, tag)"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE credentials ADD deletedat DATETIME(3) AFTER updatedat"); err != nil {
			return err
		}

		if err := bumpVersion(a, 107); err != nil {
			return err
		}
	}

	if a.version == 107 {
		// Perform database upgrade from version 107 to version 108.

		// Replace default user access JRWPA with JRWPAS.
		if _, err := a.db.Exec(`UPDATE users SET access=JSON_REPLACE(access, '$.Auth', 'JRWPAS') 
			WHERE CAST(JSON_EXTRACT(access, '$.Auth') AS CHAR) LIKE '"JRWPA"'`); err != nil {
			return err
		}

		if err := bumpVersion(a, 108); err != nil {
			return err
		}
	}

	if a.version == 108 {
		// Perform database upgrade from version 108 to version 109.

		tx, err := a.db.Begin()
		if err != nil {
			return err
		}
		if err = createSystemTopic(tx); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}

		if err = bumpVersion(a, 109); err != nil {
			return err
		}
	}

	if a.version == 109 {
		// Perform database upgrade from version 109 to version 110.
		if _, err := a.db.Exec(`UPDATE topics SET touchedat=updatedat WHERE touchedat IS NULL`); err != nil {
			return err
		}

		if err := bumpVersion(a, 110); err != nil {
			return err
		}
	}

	if a.version == 110 {
		// Users
		if _, err := a.db.Exec("ALTER TABLE users MODIFY state SMALLINT NOT NULL DEFAULT 0 AFTER updatedat"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE users CHANGE deletedat stateat DATETIME(3)"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE users DROP INDEX users_deletedat"); err != nil {
			return err
		}

		// Add status to formerly soft-deleted users.
		if _, err := a.db.Exec("UPDATE users SET state=? WHERE stateat IS NOT NULL", t.StateDeleted); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE users ADD INDEX users_state(state)"); err != nil {
			return err
		}

		// Topics
		if _, err := a.db.Exec("ALTER TABLE topics ADD state SMALLINT NOT NULL DEFAULT 0 AFTER updatedat"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE topics CHANGE deletedat stateat DATETIME(3)"); err != nil {
			return err
		}

		// Add status to formerly soft-deleted topics.
		if _, err := a.db.Exec("UPDATE topics SET state=? WHERE stateat IS NOT NULL", t.StateDeleted); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE topics ADD INDEX topics_state(state)"); err != nil {
			return err
		}

		// Subscriptions
		if _, err := a.db.Exec("ALTER TABLE subscriptions ADD INDEX topics_deletedat(deletedat)"); err != nil {
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

func createSystemTopic(tx *sql.Tx) error {
	now := t.TimeNow()
	sql := `INSERT INTO topics(createdat,updatedat,state,touchedat,name,access,public)
				VALUES(?,?,?,?,'sys','{"Auth": "N","Anon": "N"}','{"fn": "System"}')`
	_, err := tx.Exec(sql, now, now, t.StateOK, now)
	return err
}

func addTags(tx *sqlx.Tx, table, keyName string, keyVal interface{}, tags []string, ignoreDups bool) error {

	if len(tags) == 0 {
		return nil
	}

	var insert *sql.Stmt
	var err error
	insert, err = tx.Prepare("INSERT INTO " + table + "(" + keyName + ",tag) VALUES(?,?)")
	if err != nil {
		return err
	}

	for _, tag := range tags {
		_, err = insert.Exec(keyVal, tag)

		if err != nil {
			if isDupe(err) {
				if ignoreDups {
					continue
				}
				return t.ErrDuplicate
			}
			return err
		}
	}

	return nil
}

func removeTags(tx *sqlx.Tx, table, keyName string, keyVal interface{}, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	var args []interface{}
	for _, tag := range tags {
		args = append(args, tag)
	}

	query, args, _ := sqlx.In("DELETE FROM "+table+" WHERE "+keyName+"=? AND tag IN (?)", keyVal, args)
	query = tx.Rebind(query)
	_, err := tx.Exec(query, args...)

	return err
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
			tx.Rollback()
		}
	}()

	decoded_uid := store.DecodeUid(user.Uid())
	if _, err = tx.Exec("INSERT INTO users(id,createdat,updatedat,state,access,public,tags) VALUES(?,?,?,?,?,?,?)",
		decoded_uid,
		user.CreatedAt, user.UpdatedAt,
		user.State, user.Access,
		toJSON(user.Public), user.Tags); err != nil {
		return err
	}

	// Save user's tags to a separate table to make user findable.
	if err = addTags(tx, "usertags", "userid", decoded_uid, user.Tags, false); err != nil {
		return err
	}

	return tx.Commit()
}

// Add user's authentication record
func (a *adapter) AuthAddRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) error {

	var exp *time.Time
	if !expires.IsZero() {
		exp = &expires
	}
	_, err := a.db.Exec("INSERT INTO auth(uname,userid,scheme,authLvl,secret,expires) VALUES(?,?,?,?,?,?)",
		unique, store.DecodeUid(uid), scheme, authLvl, secret, exp)
	if err != nil {
		if isDupe(err) {
			return t.ErrDuplicate
		}
		return err
	}
	return nil
}

// AuthDelScheme deletes an existing authentication scheme for the user.
func (a *adapter) AuthDelScheme(user t.Uid, scheme string) error {
	_, err := a.db.Exec("DELETE FROM auth WHERE userid=? AND scheme=?", store.DecodeUid(user), scheme)
	return err
}

// AuthDelAllRecords deletes all authentication records for the user.
func (a *adapter) AuthDelAllRecords(user t.Uid) (int, error) {
	res, err := a.db.Exec("DELETE FROM auth WHERE userid=?", store.DecodeUid(user))
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()

	return int(count), nil
}

// Update user's authentication secret
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) error {
	var exp *time.Time
	if !expires.IsZero() {
		exp = &expires
	}

	_, err := a.db.Exec("UPDATE auth SET uname=?,authLvl=?,secret=?,expires=? WHERE userid=? AND scheme=?",
		unique, authLvl, secret, exp, store.DecodeUid(uid), scheme)
	if isDupe(err) {
		return t.ErrDuplicate
	}

	return err
}

// Retrieve user's authentication record
func (a *adapter) AuthGetRecord(uid t.Uid, scheme string) (string, auth.Level, []byte, time.Time, error) {
	var expires time.Time

	var record struct {
		Uname   string
		Authlvl auth.Level
		Secret  []byte
		Expires *time.Time
	}

	if err := a.db.Get(&record, "SELECT uname,secret,expires,authlvl FROM auth WHERE userid=? AND scheme=?",
		store.DecodeUid(uid), scheme); err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return "", 0, nil, expires, err
	}

	if record.Expires != nil {
		expires = *record.Expires
	}

	return record.Uname, record.Authlvl, record.Secret, expires, nil
}

// Retrieve user's authentication record
func (a *adapter) AuthGetUniqueRecord(unique string) (t.Uid, auth.Level, []byte, time.Time, error) {
	var expires time.Time

	var record struct {
		Userid  int64
		Authlvl auth.Level
		Secret  []byte
		Expires *time.Time
	}

	if err := a.db.Get(&record, "SELECT userid,secret,expires,authlvl FROM auth WHERE uname=?", unique); err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return t.ZeroUid, 0, nil, expires, err
	}

	if record.Expires != nil {
		expires = *record.Expires
	}

	return store.EncodeUid(record.Userid), record.Authlvl, record.Secret, expires, nil
}

// UserGet fetches a single user by user id. If user is not found it returns (nil, nil)
func (a *adapter) UserGet(uid t.Uid) (*t.User, error) {
	var user t.User
	err := a.db.Get(&user, "SELECT * FROM users WHERE id=? AND state!=?", store.DecodeUid(uid), t.StateDeleted)
	if err == nil {
		user.SetUid(uid)
		user.Public = fromJSON(user.Public)
		return &user, nil
	}

	if err == sql.ErrNoRows {
		// Clear the error if user does not exist or marked as soft-deleted.
		return nil, nil
	}

	return nil, err
}

func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = store.DecodeUid(id)
	}

	users := []t.User{}
	q, uids, _ := sqlx.In("SELECT * FROM users WHERE id IN (?) AND state!=?", uids, t.StateDeleted)
	q = a.db.Rebind(q)
	rows, err := a.db.Queryx(q, uids...)
	if err != nil {
		return nil, err
	}

	var user t.User
	for rows.Next() {
		if err = rows.StructScan(&user); err != nil {
			users = nil
			break
		}

		if user.State == t.StateDeleted {
			continue
		}

		user.SetUid(encodeUidString(user.Id))
		user.Public = fromJSON(user.Public)

		users = append(users, user)
	}
	rows.Close()

	return users, err
}

// UserDelete deletes specified user: wipes completely (hard-delete) or marks as deleted.
// TODO: report when the user is not found.
func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	decoded_uid := store.DecodeUid(uid)

	if hard {
		// Delete user's devices
		// t.ErrNotFound = user has no devices.
		if err = deviceDelete(tx, uid, ""); err != nil && err != t.ErrNotFound {
			return err
		}

		// Delete user's subscriptions in all topics.
		if err = subsDelForUser(tx, uid, true); err != nil {
			return err
		}

		// Delete records of messages soft-deleted for the user.
		if _, err = tx.Exec("DELETE FROM dellog WHERE deletedfor=?", decoded_uid); err != nil {
			return err
		}

		// Can't delete user's messages in all topics because we cannot notify topics of such deletion.
		// Just leave the messages there marked as sent by "not found" user.

		// Delete topics where the user is the owner.

		// First delete all messages in those topics.
		if _, err = tx.Exec("DELETE dellog FROM dellog LEFT JOIN topics ON topics.name=dellog.topic WHERE topics.owner=?",
			decoded_uid); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE messages FROM messages LEFT JOIN topics ON topics.name=messages.topic WHERE topics.owner=?",
			decoded_uid); err != nil {
			return err
		}

		// Delete all subscriptions.
		if _, err = tx.Exec("DELETE sub FROM subscriptions AS sub LEFT JOIN topics ON topics.name=sub.topic WHERE topics.owner=?",
			decoded_uid); err != nil {
			return err
		}

		// Delete topic tags
		if _, err = tx.Exec("DELETE topictags FROM topictags LEFT JOIN topics ON topics.name=topictags.topic WHERE topics.owner=?",
			decoded_uid); err != nil {
			return err
		}

		// And finally delete the topics.
		if _, err = tx.Exec("DELETE FROM topics WHERE owner=?", decoded_uid); err != nil {
			return err
		}

		// Delete user's authentication records.
		if _, err = tx.Exec("DELETE FROM auth WHERE userid=?", decoded_uid); err != nil {
			return err
		}

		// Delete all credentials.
		if err = credDel(tx, uid, "", ""); err != nil && err != t.ErrNotFound {
			return err
		}

		if _, err = tx.Exec("DELETE FROM usertags WHERE userid=?", decoded_uid); err != nil {
			return err
		}

		if _, err = tx.Exec("DELETE FROM users WHERE id=?", decoded_uid); err != nil {
			return err
		}
	} else {
		now := t.TimeNow()
		// Disable all user's subscriptions. That includes p2p subscriptions. No need to delete them.
		if err = subsDelForUser(tx, uid, false); err != nil {
			return err
		}

		// Disable all subscriptions to topics where the user is the owner.
		if _, err = tx.Exec("UPDATE subscriptions LEFT JOIN topics ON subscriptions.topic=topics.name "+
			"SET subscriptions.updatedat=?, subscriptions.deletedat=? WHERE topics.owner=?",
			now, now, decoded_uid); err != nil {
			return err
		}
		// Disable group topics where the user is the owner.
		if _, err = tx.Exec("UPDATE topics SET updatedat=?, state=?, stateat=? WHERE owner=?",
			now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}
		// Disable p2p topics with the user (p2p topic's owner is 0).
		if _, err = tx.Exec("UPDATE topics LEFT JOIN subscriptions ON topics.name=subscriptions.topic "+
			"SET topics.updatedat=?, topics.state=?, topics.stateat=? WHERE topics.owner=0 AND subscriptions.userid=?",
			now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}

		// Disable the other user's subscription to a disabled p2p topic.
		if _, err = tx.Exec("UPDATE subscriptions AS s_one LEFT JOIN subscriptions AS s_two "+
			"ON s_one.topic=s_two.topic "+
			"SET s_two.updatedat=?, s_two.deletedat=? WHERE s_one.userid=?",
			now, now, decoded_uid); err != nil {
			return err
		}

		// Disable user.
		if _, err = tx.Exec("UPDATE users SET updatedat=?, state=?, stateat=? WHERE id=?",
			now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// topicStateForUser is called by UserUpdate when the update contains state change.
func (a *adapter) topicStateForUser(tx *sqlx.Tx, decoded_uid int64, now time.Time, update interface{}) error {
	var err error

	state, ok := update.(t.ObjState)
	if !ok {
		return t.ErrMalformed
	}

	if now.IsZero() {
		now = t.TimeNow()
	}

	// Change state of all topics where the user is the owner.
	if _, err = tx.Exec("UPDATE topics SET state=?, stateat=? WHERE owner=? AND state!=?",
		state, now, decoded_uid, t.StateDeleted); err != nil {
		return err
	}

	// Change state of p2p topics with the user (p2p topic's owner is 0)
	if _, err = tx.Exec("UPDATE topics LEFT JOIN subscriptions ON topics.name=subscriptions.topic "+
		"SET topics.state=?, topics.stateat=? WHERE topics.owner=0 AND subscriptions.userid=? AND topics.state!=?",
		state, now, decoded_uid, t.StateDeleted); err != nil {
		return err
	}

	// Subscriptions don't need to be updated:
	// subscriptions of a disabled user are not disabled and still can be manipulated.

	return nil
}

// UserUpdate updates user object.
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cols, args := updateByMap(update)
	decoded_uid := store.DecodeUid(uid)
	args = append(args, decoded_uid)
	_, err = tx.Exec("UPDATE users SET "+strings.Join(cols, ",")+" WHERE id=?", args...)
	if err != nil {
		return err
	}

	if state, ok := update["State"]; ok {
		now, _ := update["StateAt"].(time.Time)
		err = a.topicStateForUser(tx, decoded_uid, now, state)
		if err != nil {
			return err
		}
	}

	// Tags are also stored in a separate table
	if tags := extractTags(update); tags != nil {
		// First delete all user tags
		_, err = tx.Exec("DELETE FROM usertags WHERE userid=?", decoded_uid)
		if err != nil {
			return err
		}
		// Now insert new tags
		err = addTags(tx, "usertags", "userid", decoded_uid, tags, false)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// UserUpdateTags adds or resets user's tags
func (a *adapter) UserUpdateTags(uid t.Uid, add, remove, reset []string) ([]string, error) {
	tx, err := a.db.Beginx()
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	decoded_uid := store.DecodeUid(uid)

	if reset != nil {
		// Delete all tags first if resetting.
		_, err = tx.Exec("DELETE FROM usertags WHERE userid=?", decoded_uid)
		if err != nil {
			return nil, err
		}
		add = reset
		remove = nil
	}

	// Now insert new tags. Ignore duplicates if resetting.
	err = addTags(tx, "usertags", "userid", decoded_uid, add, reset == nil)
	if err != nil {
		return nil, err
	}

	// Delete tags.
	err = removeTags(tx, "usertags", "userid", decoded_uid, remove)
	if err != nil {
		return nil, err
	}

	var allTags []string
	err = tx.Select(&allTags, "SELECT tag FROM usertags WHERE userid=?", decoded_uid)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec("UPDATE users SET tags=? WHERE id=?", t.StringSlice(allTags), decoded_uid)
	if err != nil {
		return nil, err
	}

	return allTags, tx.Commit()
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	var decoded_uid int64
	err := a.db.Get(&decoded_uid, "SELECT userid FROM credentials WHERE synthetic=?", method+":"+value)
	if err == nil {
		return store.EncodeUid(decoded_uid), nil
	}

	if err == sql.ErrNoRows {
		// Clear the error if user does not exist
		return t.ZeroUid, nil
	}
	return t.ZeroUid, err
}

// UserUnreadCount returns the total number of unread messages in all topics with
// the R permission.
func (a *adapter) UserUnreadCount(uid t.Uid) (int, error) {
	var count int
	err := a.db.Get(&count, "SELECT SUM(t.seqid)-SUM(s.readseqid) FROM topics AS t, subscriptions AS s "+
		"WHERE s.userid=? AND t.name=s.topic AND s.deletedat IS NULL AND t.state!=? AND "+
		"INSTR(s.modewant, 'R')>0 AND INSTR(s.modegiven, 'R')>0", store.DecodeUid(uid), t.StateDeleted)
	if err == nil {
		return count, nil
	}

	if err == sql.ErrNoRows {
		return 0, nil
	}

	return -1, err
}

// *****************************

func (a *adapter) topicCreate(tx *sqlx.Tx, topic *t.Topic) error {
	_, err := tx.Exec("INSERT INTO topics(createdat,updatedat,touchedat,state,name,usebt,owner,access,public,tags) "+
		"VALUES(?,?,?,?,?,?,?,?,?,?)",
		topic.CreatedAt, topic.UpdatedAt, topic.TouchedAt, topic.State, topic.Id, topic.UseBt,
		store.DecodeUid(t.ParseUid(topic.Owner)), topic.Access, toJSON(topic.Public), topic.Tags)
	if err != nil {
		return err
	}

	// Save topic's tags to a separate table to make topic findable.
	return addTags(tx, "topictags", "topic", topic.Id, topic.Tags, false)
}

// TopicCreate saves topic object to database.
func (a *adapter) TopicCreate(topic *t.Topic) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	err = a.topicCreate(tx, topic)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// If undelete = true - update subscription on duplicate key, otherwise ignore the duplicate.
func createSubscription(tx *sqlx.Tx, sub *t.Subscription, undelete bool) error {

	isOwner := (sub.ModeGiven & sub.ModeWant).IsOwner()

	jpriv := toJSON(sub.Private)
	decoded_uid := store.DecodeUid(t.ParseUid(sub.User))
	_, err := tx.Exec(
		"INSERT INTO subscriptions(createdat,updatedat,deletedat,userid,topic,modeWant,modeGiven,private) "+
			"VALUES(?,?,NULL,?,?,?,?,?)",
		sub.CreatedAt, sub.UpdatedAt, decoded_uid, sub.Topic, sub.ModeWant.String(), sub.ModeGiven.String(), jpriv)

	if err != nil && isDupe(err) {
		if undelete {
			_, err = tx.Exec("UPDATE subscriptions SET createdat=?,updatedat=?,deletedat=NULL,modeGiven=? "+
				"WHERE topic=? AND userid=?",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeGiven.String(), sub.Topic, decoded_uid)

		} else {
			_, err = tx.Exec(
				"UPDATE subscriptions SET createdat=?,updatedat=?,deletedat=NULL,modeWant=?,modeGiven=?,private=? "+
					"WHERE topic=? AND userid=?",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeWant.String(), sub.ModeGiven.String(),
				jpriv, sub.Topic, decoded_uid)
		}
	}
	if err == nil && isOwner {
		_, err = tx.Exec("UPDATE topics SET owner=? WHERE name=?", decoded_uid, sub.Topic)
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

	err = createSubscription(tx, initiator, false)
	if err != nil {
		return err
	}

	err = createSubscription(tx, invited, true)
	if err != nil {
		return err
	}

	topic := &t.Topic{ObjHeader: t.ObjHeader{Id: initiator.Topic}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	topic.TouchedAt = initiator.GetTouchedAt()
	err = a.topicCreate(tx, topic)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	// Fetch topic by name
	var tt = new(t.Topic)
	err := a.db.Get(tt,
		"SELECT createdat,updatedat,state,stateat,touchedat,name AS id,usebt,access,owner,seqid,delid,public,tags "+
			"FROM topics WHERE name=?",
		topic)

	if err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	tt.Owner = encodeUidString(tt.Owner).String()
	tt.Public = fromJSON(tt.Public)

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' & 'fnd' subscriptions.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch user's subscriptions
	q := `SELECT createdat,updatedat,deletedat,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`
	args := []interface{}{store.DecodeUid(uid)}
	if !keepDeleted {
		// Filter out deleted rows.
		q += " AND deletedat IS NULL"
	}

	limit := a.maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			q += " AND topic=?"
			args = append(args, opts.Topic)
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := a.db.Queryx(q, args...)
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

		tname := sub.Topic
		sub.User = uid.String()
		tcat := t.GetTopicCat(tname)

		// 'me' or 'fnd' subscription, skip
		if tcat == t.TopicCatMe || tcat == t.TopicCatFnd {
			continue

			// p2p subscription, find the other user to get user.Public
		} else if tcat == t.TopicCatP2P {
			uid1, uid2, _ := t.ParseP2P(tname)
			if uid1 == uid {
				usrq = append(usrq, store.DecodeUid(uid2))
			} else {
				usrq = append(usrq, store.DecodeUid(uid1))
			}
			topq = append(topq, tname)

			// grp subscription
		} else {
			// Convert channel names to topic names.
			tname = t.ChnToGrp(tname)
			topq = append(topq, tname)
		}
		sub.Private = fromJSON(sub.Private)
		join[tname] = sub
	}
	rows.Close()

	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	if len(topq) > 0 || len(usrq) > 0 {
		subs = make([]t.Subscription, 0, len(join))
	}

	if len(topq) > 0 {
		// Fetch grp & p2p topics
		q, topq, _ := sqlx.In(
			"SELECT createdat,updatedat,state,stateat,touchedat,name AS id,usebt,access,seqid,delid,public,tags "+
				"FROM topics WHERE name IN (?)", topq)
		// Optionally skip deleted topics.
		if !keepDeleted {
			q += " AND state!=?"
			topq = append(topq, t.StateDeleted)
		}
		q = a.db.Rebind(q)
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
			sub.SetState(top.State)
			sub.SetTouchedAt(top.TouchedAt)
			sub.SetSeqId(top.SeqId)
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
		q, usrq, _ := sqlx.In(
			"SELECT id,state,createdat,updatedat,state,stateat,access,lastseen,useragent,public,tags "+
				"FROM users WHERE id IN (?)",
			usrq)
		// Optionally skip deleted users.
		if !keepDeleted {
			q += " AND state!=?"
			usrq = append(usrq, t.StateDeleted)
		}
		rows, err = a.db.Queryx(q, usrq...)
		if err != nil {
			return nil, err
		}

		var usr t.User
		for rows.Next() {
			if err = rows.StructScan(&usr); err != nil {
				break
			}

			uid2 := encodeUidString(usr.Id)
			if sub, ok := join[uid.P2PName(uid2)]; ok {
				sub.ObjHeader.MergeTimes(&usr.ObjHeader)
				sub.SetState(usr.State)
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

// UsersForTopic loads users subscribed to the given topic.
// The difference between UsersForTopic vs SubsForTopic is that the former loads user.public,
// the latter does not.
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	tcat := t.GetTopicCat(topic)

	// Fetch all subscribed users. The number of users is not large
	q := `SELECT s.createdat,s.updatedat,s.deletedat,s.userid,s.topic,s.delid,s.recvseqid,
		s.readseqid,s.modewant,s.modegiven,u.public,s.private
		FROM subscriptions AS s JOIN users AS u ON s.userid=u.id 
		WHERE s.topic=?`
	args := []interface{}{topic}
	if !keepDeleted {
		// Filter out rows with users deleted
		q += " AND u.state!=?"
		args = append(args, t.StateDeleted)

		// For p2p topics we must load all subscriptions including deleted.
		// Otherwise it will be impossible to swipe Public values.
		if tcat != t.TopicCatP2P {
			// Filter out deleted subscriptions.
			q += " AND s.deletedat IS NULL"
		}
	}

	limit := a.maxResults
	var oneUser t.Uid
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			// For p2p topics we have to fetch both users otherwise public cannot be swapped.
			if tcat != t.TopicCatP2P {
				q += " AND s.userid=?"
				args = append(args, store.DecodeUid(opts.User))
			}
			oneUser = opts.User
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := a.db.Queryx(q, args...)
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

		sub.User = encodeUidString(sub.User).String()
		sub.Private = fromJSON(sub.Private)
		sub.SetPublic(fromJSON(public))
		subs = append(subs, sub)
	}
	rows.Close()

	if err == nil && tcat == t.TopicCatP2P && len(subs) > 0 {
		// Swap public values of P2P topics as expected.
		if len(subs) == 1 {
			// The other user is deleted, nothing we can do.
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

	return subs, err
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid) ([]string, error) {
	rows, err := a.db.Queryx("SELECT name FROM topics WHERE owner=?", store.DecodeUid(uid))
	if err != nil {
		return nil, err
	}

	var names []string
	var name string
	for rows.Next() {
		if err = rows.Scan(&name); err != nil {
			break
		}
		names = append(names, name)
	}
	rows.Close()

	return names, err
}

func (a *adapter) TopicShare(shares []*t.Subscription) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for _, sub := range shares {
		err = createSubscription(tx, sub, true)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// TopicDelete deletes specified topic.
func (a *adapter) TopicDelete(topic string, hard bool) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if hard {
		if _, err = tx.Exec("DELETE FROM subscriptions WHERE topic=?", topic); err != nil {
			return err
		}

		if err = messageDeleteList(tx, topic, nil); err != nil {
			return err
		}

		if _, err = tx.Exec("DELETE FROM topictags WHERE topic=?", topic); err != nil {
			return err
		}

		if _, err = tx.Exec("DELETE FROM topics WHERE name=?", topic); err != nil {
			return err
		}
	} else {
		now := t.TimeNow()
		if _, err = tx.Exec("UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic=?",
			now, now, topic); err != nil {
			return err
		}

		if _, err = tx.Exec("UPDATE topics SET updatedat=?,state=?,stateat=? WHERE name=?",
			now, t.StateDeleted, now, topic); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	_, err := a.db.Exec("UPDATE topics SET seqid=?,touchedat=? WHERE name=?", msg.SeqId, msg.CreatedAt, topic)

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cols, args := updateByMap(update)
	args = append(args, topic)
	_, err = tx.Exec("UPDATE topics SET "+strings.Join(cols, ",")+" WHERE name=?", args...)
	if err != nil {
		return err
	}

	// Tags are also stored in a separate table
	if tags := extractTags(update); tags != nil {
		// First delete all user tags
		_, err = tx.Exec("DELETE FROM topictags WHERE topic=?", topic)
		if err != nil {
			return err
		}
		// Now insert new tags
		err = addTags(tx, "topictags", "topic", topic, tags, false)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	_, err := a.db.Exec("UPDATE topics SET owner=? WHERE name=?", store.DecodeUid(newOwner), topic)
	return err
}

// Get a subscription of a user to a topic
func (a *adapter) SubscriptionGet(topic string, user t.Uid) (*t.Subscription, error) {
	var sub t.Subscription
	err := a.db.Get(&sub, `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=? AND userid=?`,
		topic, store.DecodeUid(user))

	if err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	if sub.DeletedAt != nil {
		return nil, nil
	}

	sub.Private = fromJSON(sub.Private)

	return &sub, nil
}

/*
// Update time when the user was last attached to the topic.
// TODO: remove, use SubsUpdate().
func (a *adapter) SubsLastSeen(topic string, user t.Uid, lastSeen map[string]time.Time) error {
	_, err := a.db.Exec("UPDATE subscriptions SET lastseen=?,useragent=? WHERE topic=? AND userid=?",
		lastSeen["LastSeen"], lastSeen["UserAgent"], topic, store.DecodeUid(user))

	return err
}
*/

// SubsForUser loads a list of user's subscriptions to topics. Does NOT load Public value.
// TODO: this is used only for presence notifications, no need to load Private either.
func (a *adapter) SubsForUser(forUser t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`

	args := []interface{}{store.DecodeUid(forUser)}
	if !keepDeleted {
		// Filter out deleted rows.
		q += " AND deletedat IS NULL"
	}

	limit := a.maxResults // maxResults here, not maxSubscribers
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if opts.Topic != "" {
			q += " AND topic=?"
			args = append(args, opts.Topic)
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := a.db.Queryx(q, args...)
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
		ss.Private = fromJSON(ss.Private)
		subs = append(subs, ss)
	}
	rows.Close()

	return subs, err
}

// SubsForTopic fetches all subsciptions for a topic. Does NOT load Public value.
// The difference between UsersForTopic vs SubsForTopic is that the former loads user.public,
// the latter does not.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=?`

	args := []interface{}{topic}
	if !keepDeleted {
		// Filter out deleted rows.
		q += " AND deletedat IS NULL"
	}
	limit := a.maxResults
	if opts != nil {
		// Ignore IfModifiedSince - we must return all entries
		// Those unmodified will be stripped of Public & Private.

		if !opts.User.IsZero() {
			q += " AND userid=?"
			args = append(args, store.DecodeUid(opts.User))
		}
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := a.db.Queryx(q, args...)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next() {
		if err = rows.StructScan(&ss); err != nil {
			break
		}

		ss.User = encodeUidString(ss.User).String()
		ss.Private = fromJSON(ss.Private)
		subs = append(subs, ss)
	}
	rows.Close()

	return subs, err
}

// SubsUpdate updates one or multiple subscriptions to a topic.
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cols, args := updateByMap(update)
	q := "UPDATE subscriptions SET " + strings.Join(cols, ",") + " WHERE topic=?"
	args = append(args, topic)
	if !user.IsZero() {
		// Update just one topic subscription
		q += " AND userid=?"
		args = append(args, store.DecodeUid(user))
	}

	if _, err = tx.Exec(q, args...); err != nil {
		return err
	}

	return tx.Commit()
}

// SubsDelete marks subscription as deleted.
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	now := t.TimeNow()
	res, err := a.db.Exec(
		"UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic=? AND userid=? AND deletedat IS NULL",
		now, now, topic, store.DecodeUid(user))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		err = t.ErrNotFound
	}
	return err
}

// SubsDelForTopic marks all subscriptions to the given topic as deleted
func (a *adapter) SubsDelForTopic(topic string, hard bool) error {
	var err error
	if hard {
		_, err = a.db.Exec("DELETE FROM subscriptions WHERE topic=?", topic)
	} else {
		now := t.TimeNow()
		_, err = a.db.Exec("UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic=? AND deletedat IS NULL",
			now, t.StateDeleted, now, topic)
	}
	return err
}

// subsDelForTopic marks user's subscriptions as deleted
func subsDelForUser(tx *sqlx.Tx, user t.Uid, hard bool) error {
	var err error
	if hard {
		_, err = tx.Exec("DELETE FROM subscriptions WHERE userid=?", store.DecodeUid(user))
	} else {
		now := t.TimeNow()
		_, err = tx.Exec("UPDATE subscriptions SET updatedat=?,deletedat=? WHERE userid=? AND deletedat IS NULL",
			now, now, store.DecodeUid(user))
	}
	return err
}

// SubsDelForTopic marks user's subscriptions as deleted
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if err = subsDelForUser(tx, user, hard); err != nil {
		return err
	}

	return tx.Commit()

}

// Returns a list of users who match given tags, such as "email:jdoe@example.com" or "tel:+18003287448".
// Searching the 'users.Tags' for the given tags using respective index.
func (a *adapter) FindUsers(uid t.Uid, req [][]string, opt []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var args []interface{}
	args = append(args, t.StateOK)
	allReq := t.FlattenDoubleSlice(req)
	for _, tag := range append(allReq, opt...) {
		args = append(args, tag)
		index[tag] = struct{}{}
	}

	query := "SELECT u.id,u.createdat,u.updatedat,u.access,u.public,u.tags,COUNT(*) AS matches " +
		"FROM users AS u LEFT JOIN usertags AS t ON t.userid=u.id " +
		"WHERE u.state=? AND t.tag IN (?" + strings.Repeat(",?", len(allReq)+len(opt)-1) + ") " +
		"GROUP BY u.id,u.createdat,u.updatedat,u.public,u.tags "
	if len(allReq) > 0 {
		query += "HAVING"
		first := true
		for _, reqDisjunction := range req {
			if len(reqDisjunction) > 0 {
				if !first {
					query += " AND"
				} else {
					first = false
				}
				// At least one of the tags must be present.
				query += " COUNT(t.tag IN (?" + strings.Repeat(",?", len(reqDisjunction)-1) + ") OR NULL)>=1 "
				for _, tag := range reqDisjunction {
					args = append(args, tag)
				}
			}
		}
	}
	query += "ORDER BY matches DESC LIMIT ?"

	// Get users matched by tags, sort by number of matches from high to low.
	rows, err := a.db.Queryx(query, append(args, a.maxResults)...)

	if err != nil {
		return nil, err
	}

	var userId int64
	var public interface{}
	var access t.DefaultAccess
	var userTags t.StringSlice
	var ignored int
	var sub t.Subscription
	var subs []t.Subscription
	thisUser := store.DecodeUid(uid)
	for rows.Next() {
		if err = rows.Scan(&userId, &sub.CreatedAt, &sub.UpdatedAt, &access, &public, &userTags, &ignored); err != nil {
			subs = nil
			break
		}

		if userId == thisUser {
			// Skip the callee
			continue
		}
		sub.User = store.EncodeUid(userId).String()
		sub.SetPublic(fromJSON(public))
		sub.SetDefaultAccess(access.Auth, access.Anon)
		foundTags := make([]string, 0, 1)
		for _, tag := range userTags {
			if _, ok := index[tag]; ok {
				foundTags = append(foundTags, tag)
			}
		}
		sub.Private = foundTags
		subs = append(subs, sub)
	}
	rows.Close()

	return subs, err

}

// Returns a list of topics with matching tags.
// Searching the 'topics.Tags' for the given tags using respective index.
func (a *adapter) FindTopics(req [][]string, opt []string) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var args []interface{}
	args = append(args, t.StateOK)
	var allReq []string
	for _, el := range req {
		allReq = append(allReq, el...)
	}
	for _, tag := range append(allReq, opt...) {
		args = append(args, tag)
		index[tag] = struct{}{}
	}

	query := "SELECT t.name AS topic,t.createdat,t.updatedat,t.usebt,t.access,t.public,t.tags,COUNT(*) AS matches " +
		"FROM topics AS t LEFT JOIN topictags AS tt ON t.name=tt.topic " +
		"WHERE t.state=? AND tt.tag IN (?" + strings.Repeat(",?", len(allReq)+len(opt)-1) + ") " +
		"GROUP BY t.name,t.createdat,t.updatedat,t.usebt,t.access,t.public,t.tags "
	if len(allReq) > 0 {
		query += "HAVING"
		first := true
		for _, reqDisjunction := range req {
			if len(reqDisjunction) > 0 {
				if !first {
					query += " AND"
				} else {
					first = false
				}
				// At least one of the tags must be present.
				query += " COUNT(tt.tag IN (?" + strings.Repeat(",?", len(reqDisjunction)-1) + ") OR NULL)>=1 "
				for _, tag := range reqDisjunction {
					args = append(args, tag)
				}
			}
		}
	}
	query += "ORDER BY matches DESC LIMIT ?"
	rows, err := a.db.Queryx(query, append(args, a.maxResults)...)

	if err != nil {
		return nil, err
	}

	var access t.DefaultAccess
	var public interface{}
	var topicTags t.StringSlice
	var ignored int
	var isChan int
	var sub t.Subscription
	var subs []t.Subscription
	for rows.Next() {
		if err = rows.Scan(&sub.Topic, &sub.CreatedAt, &sub.UpdatedAt, &isChan, &access,
			&public, &topicTags, &ignored); err != nil {
			subs = nil
			break
		}

		if isChan != 0 {
			sub.Topic = t.GrpToChn(sub.Topic)
		}
		sub.SetPublic(fromJSON(public))
		sub.SetDefaultAccess(access.Auth, access.Anon)
		foundTags := make([]string, 0, 1)
		for _, tag := range topicTags {
			if _, ok := index[tag]; ok {
				foundTags = append(foundTags, tag)
			}
		}
		sub.Private = foundTags
		subs = append(subs, sub)
	}
	rows.Close()

	if err != nil {
		return nil, err
	}
	return subs, nil

}

// Messages
func (a *adapter) MessageSave(msg *t.Message) error {
	// store assignes message ID, but we don't use it. Message IDs are not used anywhere.
	// Using a sequential ID provided by the database.
	res, err := a.db.Exec(
		"INSERT INTO messages(createdAt,updatedAt,seqid,topic,`from`,head,content) VALUES(?,?,?,?,?,?,?)",
		msg.CreatedAt, msg.UpdatedAt, msg.SeqId, msg.Topic,
		store.DecodeUid(t.ParseUid(msg.From)), msg.Head, toJSON(msg.Content))
	if err == nil {
		id, _ := res.LastInsertId()
		// Replacing ID given by store by ID given by the DB.
		msg.SetUid(t.Uid(id))
	}
	return err
}

func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {
	var limit = a.maxMessageResults
	var lower = 0
	var upper = 1<<31 - 1

	if opts != nil {
		if opts.Since > 0 {
			lower = opts.Since
		}
		if opts.Before > 0 {
			// MySQL BETWEEN is inclusive-inclusive, Tinode API requires inclusive-exclusive, thus -1
			upper = opts.Before - 1
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	unum := store.DecodeUid(forUser)
	rows, err := a.db.Queryx(
		"SELECT m.createdat,m.updatedat,m.deletedat,m.delid,m.seqid,m.topic,m.`from`,m.head,m.content"+
			" FROM messages AS m LEFT JOIN dellog AS d"+
			" ON d.topic=m.topic AND m.seqid BETWEEN d.low AND d.hi-1 AND d.deletedfor=?"+
			" WHERE m.delid=0 AND m.topic=? AND m.seqid BETWEEN ? AND ? AND d.deletedfor IS NULL"+
			" ORDER BY m.seqid DESC LIMIT ?",
		unum, topic, lower, upper, limit)

	if err != nil {
		return nil, err
	}

	var msgs []t.Message
	for rows.Next() {
		var msg t.Message
		if err = rows.StructScan(&msg); err != nil {
			break
		}
		msg.From = encodeUidString(msg.From).String()
		msg.Content = fromJSON(msg.Content)
		msgs = append(msgs, msg)
	}
	rows.Close()
	return msgs, err
}

var dellog struct {
	Topic      string
	Deletedfor int64
	Delid      int
	Low        int
	Hi         int
}

// Get ranges of deleted messages
func (a *adapter) MessageGetDeleted(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.DelMessage, error) {
	var limit = a.maxResults
	var lower = 0
	var upper = 1<<31 - 1

	if opts != nil {
		if opts.Since > 0 {
			lower = opts.Since
		}
		if opts.Before > 1 {
			// DelRange is inclusive-exclusive, while BETWEEN is inclusive-inclisive.
			upper = opts.Before - 1
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	// Fetch log of deletions
	rows, err := a.db.Queryx("SELECT topic,deletedfor,delid,low,hi FROM dellog WHERE topic=? AND delid BETWEEN ? AND ?"+
		" AND (deletedFor=0 OR deletedFor=?)"+
		" ORDER BY delid LIMIT ?", topic, lower, upper, store.DecodeUid(forUser), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dmsgs []t.DelMessage
	var dmsg t.DelMessage
	for rows.Next() {
		if err = rows.StructScan(&dellog); err != nil {
			dmsgs = nil
			break
		}

		if dellog.Delid != dmsg.DelId {
			if dmsg.DelId > 0 {
				dmsgs = append(dmsgs, dmsg)
			}
			dmsg.DelId = dellog.Delid
			dmsg.Topic = dellog.Topic
			if dellog.Deletedfor > 0 {
				dmsg.DeletedFor = store.EncodeUid(dellog.Deletedfor).String()
			} else {
				dmsg.DeletedFor = ""
			}
			dmsg.SeqIdRanges = nil
		}
		if dellog.Hi <= dellog.Low+1 {
			dellog.Hi = 0
		}
		dmsg.SeqIdRanges = append(dmsg.SeqIdRanges, t.Range{dellog.Low, dellog.Hi})
	}

	if err == nil {
		if dmsg.DelId > 0 {
			dmsgs = append(dmsgs, dmsg)
		}
	}

	return dmsgs, err
}

func messageDeleteList(tx *sqlx.Tx, topic string, toDel *t.DelMessage) error {
	var err error
	if toDel == nil {
		// Whole topic is being deleted, thus also deleting all messages.
		_, err = tx.Exec("DELETE FROM dellog WHERE topic=?", topic)
		if err == nil {
			_, err = tx.Exec("DELETE FROM messages WHERE topic=?", topic)
		}
		// filemsglinks will be deleted because of ON DELETE CASCADE

	} else {
		// Only some messages are being deleted
		// Start with making log entries
		forUser := decodeUidString(toDel.DeletedFor)
		var insert *sql.Stmt
		if insert, err = tx.Prepare(
			"INSERT INTO dellog(topic,deletedfor,delid,low,hi) VALUES(?,?,?,?,?)"); err != nil {
			return err
		}

		// Counter of deleted messages
		seqCount := 0
		for _, rng := range toDel.SeqIdRanges {
			if rng.Hi == 0 {
				// Dellog must contain valid Low and *Hi*.
				rng.Hi = rng.Low + 1
			}
			seqCount += rng.Hi - rng.Low
			if _, err = insert.Exec(topic, forUser, toDel.DelId, rng.Low, rng.Hi); err != nil {
				break
			}
		}

		if err == nil && toDel.DeletedFor == "" {
			// Hard-deleting messages requires updates to the messages table
			where := "m.topic=? AND "
			args := []interface{}{topic}
			if len(toDel.SeqIdRanges) > 1 || toDel.SeqIdRanges[0].Hi == 0 {
				for _, r := range toDel.SeqIdRanges {
					if r.Hi == 0 {
						args = append(args, r.Low)
					} else {
						for i := r.Low; i < r.Hi; i++ {
							args = append(args, i)
						}
					}
				}

				where += "m.seqid IN (?" + strings.Repeat(",?", seqCount-1) + ")"
			} else {
				// Optimizing for a special case of single range low..hi.
				where += "m.seqid BETWEEN ? AND ?"
				// MySQL's BETWEEN is inclusive-inclusive thus decrement Hi by 1.
				args = append(args, toDel.SeqIdRanges[0].Low, toDel.SeqIdRanges[0].Hi-1)
			}
			where += " AND m.deletedAt IS NULL"

			_, err = tx.Exec("DELETE fml.* FROM filemsglinks AS fml INNER JOIN messages AS m ON m.id=fml.msgid WHERE "+
				where, args...)
			if err != nil {
				return err
			}

			_, err = tx.Exec("UPDATE messages AS m SET m.deletedAt=?,m.delId=?,m.head=NULL,m.content=NULL WHERE "+
				where,
				append([]interface{}{t.TimeNow(), toDel.DelId}, args...)...)
		}
	}

	return err
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) (err error) {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if err = messageDeleteList(tx, topic, toDel); err != nil {
		return err
	}

	return tx.Commit()
}

// MessageAttachments connects given message to a list of file record IDs.
func (a *adapter) MessageAttachments(msgId t.Uid, fids []string) error {
	var args []interface{}
	var values []string
	strNow := t.TimeNow().Format("2006-01-02T15:04:05.999")
	// createdat,fileid,msgid
	val := "('" + strNow + "',?," + strconv.FormatInt(int64(msgId), 10) + ")"
	for _, fid := range fids {
		id := t.ParseUid(fid)
		if id.IsZero() {
			return t.ErrMalformed
		}
		values = append(values, val)
		args = append(args, store.DecodeUid(id))
	}
	if len(args) == 0 {
		return t.ErrMalformed
	}

	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	_, err = a.db.Exec("INSERT INTO filemsglinks(createdat,fileid,msgid) VALUES "+strings.Join(values, ","), args...)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE fileuploads SET updatedat='"+strNow+"' WHERE id IN (?"+
		strings.Repeat(",?", len(args)-1)+")", args...)
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
	_, err = tx.Exec("DELETE FROM devices WHERE hash=?", hash)
	if err != nil {
		return err
	}

	// Actually add/update DeviceId for the new user
	_, err = tx.Exec("INSERT INTO devices(userid, hash, deviceId, platform, lastseen, lang) VALUES(?,?,?,?,?,?)",
		store.DecodeUid(uid), hash, def.DeviceId, def.Platform, def.LastSeen, def.Lang)
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

	q, unums, _ := sqlx.In("SELECT userid,deviceid,platform,lastseen,lang FROM devices WHERE userid IN (?)", unums)
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

func deviceDelete(tx *sqlx.Tx, uid t.Uid, deviceID string) error {
	var err error
	var res sql.Result
	if deviceID == "" {
		res, err = tx.Exec("DELETE FROM devices WHERE userid=?", store.DecodeUid(uid))
	} else {
		res, err = tx.Exec("DELETE FROM devices WHERE userid=? AND hash=?", store.DecodeUid(uid), deviceHasher(deviceID))
	}

	if err == nil {
		if count, _ := res.RowsAffected(); count == 0 {
			err = t.ErrNotFound
		}
	}

	return err
}

func (a *adapter) DeviceDelete(uid t.Uid, deviceID string) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	err = deviceDelete(tx, uid, deviceID)
	if err != nil {
		return err
	}

	return tx.Commit()
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
	var err error

	tx, err := a.db.Beginx()
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := t.TimeNow()
	userId := decodeUidString(cred.User)

	// Enforce uniqueness: if credential is confirmed, "method:value" must be unique.
	// if credential is not yet confirmed, "userid:method:value" is unique.
	synth := cred.Method + ":" + cred.Value

	if !cred.Done {
		// Check if this credential is already validated.
		var done bool
		err = tx.Get(&done, "SELECT done FROM credentials WHERE synthetic=?", synth)
		if err == nil {
			return false, t.ErrDuplicate
		}
		if err != sql.ErrNoRows {
			return false, err
		}
		// We are going to insert new record.
		synth = cred.User + ":" + synth

		// Adding new unvalidated credential. Deactivate all unvalidated records of this user and method.
		_, err = tx.Exec("UPDATE credentials SET deletedat=? WHERE userid=? AND method=? AND done=false",
			now, userId, cred.Method)
		// Assume that the record exists and try to update it: undelete, update timestamp and response value.
		res, err := tx.Exec("UPDATE credentials SET updatedat=?,deletedat=NULL,resp=?,done=0 WHERE synthetic=?",
			cred.UpdatedAt, cred.Resp, synth)
		if err != nil {
			return false, err
		}
		// If record was updated, then all is fine.
		if numrows, _ := res.RowsAffected(); numrows > 0 {
			return false, tx.Commit()
		}
	} else {
		// Hard-deleting unconformed record if it exists.
		_, err = tx.Exec("DELETE FROM credentials WHERE synthetic=?", cred.User+":"+synth)
		if err != nil {
			return false, err
		}
	}
	// Add new record.
	_, err = tx.Exec("INSERT INTO credentials(createdat,updatedat,method,value,synthetic,userid,resp,done) "+
		"VALUES(?,?,?,?,?,?,?,?)",
		cred.CreatedAt, cred.UpdatedAt, cred.Method, cred.Value, synth, userId, cred.Resp, cred.Done)
	if err != nil {
		if isDupe(err) {
			return true, t.ErrDuplicate
		}
		return true, err
	}
	return true, tx.Commit()
}

// credDel deletes given validation method or all methods of the given user.
// 1. If user is being deleted, hard-delete all records (method == "")
// 2. If one value is being deleted:
// 2.1 Delete it if it's valiated or if there were no attempts at validation
// (otherwise it could be used to circumvent the limit on validation attempts).
// 2.2 In that case mark it as soft-deleted.
func credDel(tx *sqlx.Tx, uid t.Uid, method, value string) error {
	constraints := " WHERE userid=?"
	args := []interface{}{store.DecodeUid(uid)}

	if method != "" {
		constraints += " AND method=?"
		args = append(args, method)

		if value != "" {
			constraints += " AND value=?"
			args = append(args, value)
		}
	}

	var err error
	var res sql.Result
	if method == "" {
		// Case 1
		res, err = tx.Exec("DELETE FROM credentials"+constraints, args...)
		if err == nil {
			if count, _ := res.RowsAffected(); count == 0 {
				err = t.ErrNotFound
			}
		}
		return err
	}

	// Case 2.1
	res, err = tx.Exec("DELETE FROM credentials"+constraints+" AND (done=true OR retries=0)", args...)
	if err != nil {
		return err
	}
	if count, _ := res.RowsAffected(); count > 0 {
		return nil
	}

	// Case 2.2
	args = append([]interface{}{t.TimeNow()}, args...)
	res, err = tx.Exec("UPDATE credentials SET deletedat=?"+constraints, args...)
	if err == nil {
		if count, _ := res.RowsAffected(); count >= 0 {
			err = t.ErrNotFound
		}
	}

	return err
}

// CredDel deletes either credentials of the given user. If method is blank all
// credentials are removed. If value is blank all credentials of the given the
// method are removed.
func (a *adapter) CredDel(uid t.Uid, method, value string) error {
	tx, err := a.db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	err = credDel(tx, uid, method, value)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CredConfirm marks given credential method as confirmed.
func (a *adapter) CredConfirm(uid t.Uid, method string) error {
	res, err := a.db.Exec(
		"UPDATE credentials SET updatedat=?,done=true,synthetic=CONCAT(method,':',value) "+
			"WHERE userid=? AND method=? AND deletedat IS NULL AND done=false",
		t.TimeNow(), store.DecodeUid(uid), method)
	if err != nil {
		if isDupe(err) {
			return t.ErrDuplicate
		}
		return err
	}
	if numrows, _ := res.RowsAffected(); numrows < 1 {
		return t.ErrNotFound
	}
	return nil
}

// CredFail increments failure count of the given validation method.
func (a *adapter) CredFail(uid t.Uid, method string) error {
	_, err := a.db.Exec("UPDATE credentials SET updatedat=?,retries=retries+1 WHERE userid=? AND method=? AND done=false",
		t.TimeNow(), store.DecodeUid(uid), method)
	return err
}

// CredGetActive returns currently active unvalidated credential of the given user and method.
func (a *adapter) CredGetActive(uid t.Uid, method string) (*t.Credential, error) {
	var cred t.Credential
	err := a.db.Get(&cred, "SELECT createdat,updatedat,method,value,resp,done,retries "+
		"FROM credentials WHERE userid=? AND deletedat IS NULL AND method=? AND done=false",
		store.DecodeUid(uid), method)
	if err != nil {
		if err == sql.ErrNoRows {
			err = nil
		}
		return nil, err
	}
	cred.User = uid.String()

	return &cred, nil
}

// CredGetAll returns credential records for the given user and method, all or validated only.
func (a *adapter) CredGetAll(uid t.Uid, method string, validatedOnly bool) ([]t.Credential, error) {
	query := "SELECT createdat,updatedat,method,value,resp,done,retries FROM credentials WHERE userid=? AND deletedat IS NULL"
	args := []interface{}{store.DecodeUid(uid)}
	if method != "" {
		query += " AND method=?"
		args = append(args, method)
	}
	if validatedOnly {
		query += " AND done=true"
	}

	var credentials []t.Credential
	err := a.db.Select(&credentials, query, args...)
	if err != nil {
		return nil, err
	}

	user := uid.String()
	for i := range credentials {
		credentials[i].User = user
	}

	return credentials, err
}

// FileUploads

// FileStartUpload initializes a file upload
func (a *adapter) FileStartUpload(fd *t.FileDef) error {
	_, err := a.db.Exec("INSERT INTO fileuploads(id,createdat,updatedat,userid,status,mimetype,size,location)"+
		" VALUES(?,?,?,?,?,?,?,?)",
		store.DecodeUid(fd.Uid()), fd.CreatedAt, fd.UpdatedAt,
		store.DecodeUid(t.ParseUid(fd.User)), fd.Status, fd.MimeType, fd.Size, fd.Location)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise
func (a *adapter) FileFinishUpload(fid string, status int, size int64) (*t.FileDef, error) {
	id := t.ParseUid(fid)
	if id.IsZero() {
		return nil, t.ErrMalformed
	}

	fd, err := a.FileGet(fid)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, t.ErrNotFound
	}

	fd.UpdatedAt = t.TimeNow()
	_, err = a.db.Exec("UPDATE fileuploads SET updatedat=?,status=?,size=? WHERE id=?",
		fd.UpdatedAt, status, size, store.DecodeUid(id))
	if err == nil {
		fd.Status = status
		fd.Size = size
	} else {
		fd = nil
	}
	return fd, err
}

// FileGet fetches a record of a specific file
func (a *adapter) FileGet(fid string) (*t.FileDef, error) {
	id := t.ParseUid(fid)
	if id.IsZero() {
		return nil, t.ErrMalformed
	}

	var fd t.FileDef
	err := a.db.Get(&fd, "SELECT id,createdat,updatedat,userid AS user,status,mimetype,size,location "+
		"FROM fileuploads WHERE id=?", store.DecodeUid(id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	fd.Id = encodeUidString(fd.Id).String()
	fd.User = encodeUidString(fd.User).String()

	return &fd, nil

}

// FileDeleteUnused deletes file upload records.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	query := "SELECT fu.id,fu.location FROM fileuploads AS fu LEFT JOIN filemsglinks AS fml ON fml.fileid=fu.id WHERE fml.id IS NULL "
	var args []interface{}
	if !olderThan.IsZero() {
		query += "AND fu.updatedat<? "
		args = append(args, olderThan)
	}
	if limit > 0 {
		query += "LIMIT ?"
		args = append(args, limit)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}

	var locations []string
	var ids []interface{}
	for rows.Next() {
		var id int
		var loc string
		if err = rows.Scan(&id, &loc); err != nil {
			break
		}
		locations = append(locations, loc)
		ids = append(ids, id)
	}
	rows.Close()

	if err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		query, ids, _ = sqlx.In("DELETE FROM fileuploads WHERE id IN (?)", ids)
		_, err = tx.Exec(query, ids...)
		if err != nil {
			return nil, err
		}
	}

	return locations, tx.Commit()
}

// Helper functions

// Check if MySQL error is a Error Code: 1062. Duplicate entry ... for key ...
func isDupe(err error) bool {
	if err == nil {
		return false
	}

	myerr, ok := err.(*ms.MySQLError)
	return ok && myerr.Number == 1062
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}

	myerr, ok := err.(*ms.MySQLError)
	return ok && myerr.Number == 1146
}

func isMissingDb(err error) bool {
	if err == nil {
		return false
	}

	myerr, ok := err.(*ms.MySQLError)
	return ok && myerr.Number == 1049
}

// Convert to JSON before storing to JSON field.
func toJSON(src interface{}) []byte {
	if src == nil {
		return nil
	}

	jval, _ := json.Marshal(src)
	return jval
}

// Deserialize JSON data from DB.
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

// UIDs are stored as decoded int64 values. Take decoded string representation of int64, produce UID.
func encodeUidString(str string) t.Uid {
	unum, _ := strconv.ParseInt(str, 10, 64)
	return store.EncodeUid(unum)
}

func decodeUidString(str string) int64 {
	uid := t.ParseUid(str)
	return store.DecodeUid(uid)
}

// Convert update to a list of columns and arguments.
func updateByMap(update map[string]interface{}) (cols []string, args []interface{}) {
	for col, arg := range update {
		col = strings.ToLower(col)
		if col == "public" || col == "private" {
			arg = toJSON(arg)
		}
		cols = append(cols, col+"=?")
		args = append(args, arg)
	}
	return
}

// If Tags field is updated, get the tags so tags table cab be updated too.
func extractTags(update map[string]interface{}) []string {
	var tags []string

	val := update["Tags"]
	if val != nil {
		tags, _ = val.(t.StringSlice)
	}

	return []string(tags)
}

func init() {
	store.RegisterAdapter(&adapter{})
}
