//go:build mysql
// +build mysql

// Package mysql is a database adapter for MySQL.
package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	ms "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/db/common"
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

	// Single query timeout.
	sqlTimeout time.Duration
	// DB transaction timeout.
	txTimeout time.Duration
}

const (
	defaultDSN      = "root:@tcp(localhost:3306)/tinode?parseTime=true"
	defaultDatabase = "tinode"

	adpVersion = 115

	adapterName = "mysql"

	defaultMaxResults = 1024
	// This is capped by the Session's send queue limit (128).
	defaultMaxMessageResults = 100

	// If DB request timeout is specified,
	// we allocate txTimeoutMultiplier times more time for transactions.
	txTimeoutMultiplier = 1.5
)

type configType struct {
	// DB connection settings.
	// Please, see https://pkg.go.dev/github.com/go-sql-driver/mysql#Config
	// for the full list of fields.
	ms.Config
	// Deprecated.
	DSN string `json:"dsn,omitempty"`

	// Connection pool settings.
	//
	// Maximum number of open connections to the database.
	MaxOpenConns int `json:"max_open_conns,omitempty"`
	// Maximum number of connections in the idle connection pool.
	MaxIdleConns int `json:"max_idle_conns,omitempty"`
	// Maximum amount of time a connection may be reused (in seconds).
	ConnMaxLifetime int `json:"conn_max_lifetime,omitempty"`

	// DB request timeout (in seconds).
	// If 0 (or negative), no timeout is applied.
	SqlTimeout int `json:"sql_timeout,omitempty"`
}

func (a *adapter) getContext() (context.Context, context.CancelFunc) {
	if a.sqlTimeout > 0 {
		return context.WithTimeout(context.Background(), a.sqlTimeout)
	}
	return context.Background(), nil
}

func (a *adapter) getContextForTx() (context.Context, context.CancelFunc) {
	if a.txTimeout > 0 {
		return context.WithTimeout(context.Background(), a.txTimeout)
	}
	return context.Background(), nil
}

// Open initializes database session
func (a *adapter) Open(jsonconfig json.RawMessage) error {
	if a.db != nil {
		return errors.New("mysql adapter is already connected")
	}

	if len(jsonconfig) < 2 {
		return errors.New("adapter mysql missing config")
	}

	var err error
	defaultCfg := ms.NewConfig()
	config := configType{Config: *defaultCfg}
	if err = json.Unmarshal(jsonconfig, &config); err != nil {
		return errors.New("mysql adapter failed to parse config: " + err.Error())
	}

	if dsn := config.FormatDSN(); dsn != defaultCfg.FormatDSN() {
		// MySql config is specified. Use it.
		a.dbName = config.DBName
		a.dsn = dsn
		if config.DSN != "" {
			return errors.New("mysql config: conflicting config and DSN are provided")
		}
	} else {
		// Otherwise, use DSN to configure database connection.
		// Note: this method is deprecated.
		if config.DSN != "" {
			// Remove optional schema.
			a.dsn = strings.TrimPrefix(config.DSN, "mysql://")
		} else {
			a.dsn = defaultDSN
		}

		// Parse out the database name from the DSN.
		// Add schema to create a valid URL.
		if uri, err := url.Parse("mysql://" + a.dsn); err == nil {
			a.dbName = strings.TrimPrefix(uri.Path, "/")
		} else {
			return err
		}
	}

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
	if err == nil {
		if config.MaxOpenConns > 0 {
			a.db.SetMaxOpenConns(config.MaxOpenConns)
		}
		if config.MaxIdleConns > 0 {
			a.db.SetMaxIdleConns(config.MaxIdleConns)
		}
		if config.ConnMaxLifetime > 0 {
			a.db.SetConnMaxLifetime(time.Duration(config.ConnMaxLifetime) * time.Second)
		}
		if config.SqlTimeout > 0 {
			a.sqlTimeout = time.Duration(config.SqlTimeout) * time.Second
			// We allocate txTimeoutMultiplier times sqlTimeout for transactions.
			a.txTimeout = time.Duration(float64(config.SqlTimeout)*txTimeoutMultiplier) * time.Second
		}
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var vers int
	err := a.db.GetContext(ctx, &vers, "SELECT `value` FROM kvmeta WHERE `key`='version'")
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	a.version = -1
	if _, err := a.db.ExecContext(ctx, "UPDATE kvmeta SET `value`=? WHERE `key`='version'", v); err != nil {
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

// DB connection stats object.
func (a *adapter) Stats() any {
	if a.db == nil {
		return nil
	}
	return a.db.Stats()
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

	if reset {
		if _, err = tx.Exec("DROP DATABASE IF EXISTS " + a.dbName); err != nil {
			return err
		}
	}

	if _, err = tx.Exec("CREATE DATABASE " + a.dbName + " CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
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
			trusted   JSON,
			tags      JSON,
			PRIMARY KEY(id),
			INDEX users_state_stateat(state, stateat),
			INDEX users_lastseen_updatedat(lastseen, updatedat)
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
			subcnt		INT DEFAULT 0,
			public    JSON,
			trusted   JSON,
			tags      JSON,
			aux       JSON,
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
			UNIQUE INDEX topictags_topic_tag(topic, tag)
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
			topic      CHAR(25) NOT NULL,
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
			userid    BIGINT,
			status    INT NOT NULL,
			mimetype  VARCHAR(255) NOT NULL,
			size      BIGINT NOT NULL,
			etag      VARCHAR(128),
			location  VARCHAR(2048) NOT NULL,
			PRIMARY KEY(id),
			INDEX fileuploads_status(status)
		)`); err != nil {
		return err
	}

	// Links between uploaded files and the topics, users or messages they are attached to.
	if _, err = tx.Exec(
		`CREATE TABLE filemsglinks(
			id        INT NOT NULL AUTO_INCREMENT,
			createdat DATETIME(3) NOT NULL,
			fileid    BIGINT NOT NULL,
			msgid     INT,
			topic     CHAR(25),
			userid    BIGINT,
			PRIMARY KEY(id),
			FOREIGN KEY(fileid) REFERENCES fileuploads(id) ON DELETE CASCADE,
			FOREIGN KEY(msgid) REFERENCES messages(id) ON DELETE CASCADE,
			FOREIGN KEY(topic) REFERENCES topics(name) ON DELETE CASCADE,
			FOREIGN KEY(userid) REFERENCES users(id) ON DELETE CASCADE
		)`); err != nil {
		return err
	}

	if _, err = tx.Exec(
		`CREATE TABLE kvmeta(` +
			"`key`       VARCHAR(64) NOT NULL," +
			"createdat   DATETIME(3)," +
			"`value`     TEXT," +
			"PRIMARY KEY(`key`)," +
			"INDEX kvmeta_createdat_key(createdat, `key`)" +
			`)`); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO kvmeta(`key`, `value`) VALUES('version',?)", adpVersion); err != nil {
		return err
	}

	// Find relevant subscriptions for given users efficiently, and use the join key too.
	if _, err = tx.Exec("CREATE INDEX idx_subs_user_topic_del ON subscriptions(userid, topic, deletedat)"); err != nil {
		return err
	}

	// Optimizes join; state filters; seqid supports the SUM operation.
	if _, err = tx.Exec("CREATE INDEX idx_topics_name_state_seqid ON topics(name, state, seqid)"); err != nil {
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

		if _, err := a.db.Exec("CREATE UNIQUE INDEX topictags_topic_tag ON topictags(topic, tag)"); err != nil {
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
		if _, err := a.db.Exec("UPDATE topics SET touchedat=updatedat WHERE touchedat IS NULL"); err != nil {
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

	if a.version == 111 {
		// Perform database upgrade from version 111 to version 112.
		if _, err := a.db.Exec("ALTER TABLE users ADD trusted JSON AFTER public"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE topics ADD trusted JSON AFTER public"); err != nil {
			return err
		}

		// Remove NOT NULL constraint, so an avatar upload can be done at registration.
		if _, err := a.db.Exec("ALTER TABLE fileuploads MODIFY userid BIGINT"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE fileuploads ADD INDEX fileuploads_status(status)"); err != nil {
			return err
		}

		// Remove NOT NULL constraint to enable links to users and topics.
		if _, err := a.db.Exec("ALTER TABLE filemsglinks MODIFY msgid INT"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE filemsglinks ADD topic CHAR(25)"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE filemsglinks ADD userid BIGINT"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE filemsglinks ADD FOREIGN KEY(topic) REFERENCES topics(name) ON DELETE CASCADE"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE filemsglinks ADD FOREIGN KEY(userid) REFERENCES users(id) ON DELETE CASCADE"); err != nil {
			return err
		}

		if err := bumpVersion(a, 112); err != nil {
			return err
		}
	}

	if a.version == 112 {
		// Perform database upgrade from version 112 to version 113.

		// Index for deleting unvalidated accounts.
		if _, err := a.db.Exec("ALTER TABLE users ADD INDEX users_lastseen_updatedat(lastseen,updatedat)"); err != nil {
			return err
		}

		// Add timestamp to kvmeta.
		if _, err := a.db.Exec("ALTER TABLE kvmeta MODIFY `key` VARCHAR(64) NOT NULL"); err != nil {
			return err
		}

		// Add timestamp to kvmeta.
		if _, err := a.db.Exec("ALTER TABLE kvmeta ADD createdat DATETIME(3) AFTER `key`"); err != nil {
			return err
		}

		// Add compound index on the new field and key (could be searched by key prefix).
		if _, err := a.db.Exec("ALTER TABLE kvmeta ADD INDEX kvmeta_createdat_key(createdat, `key`)"); err != nil {
			return err
		}

		if err := bumpVersion(a, 113); err != nil {
			return err
		}
	}

	if a.version == 113 {
		// Perform database upgrade from version 113 to version 114.

		if _, err := a.db.Exec("ALTER TABLE topics ADD aux JSON"); err != nil {
			return err
		}

		if _, err := a.db.Exec("ALTER TABLE fileuploads ADD etag VARCHAR(128) AFTER size"); err != nil {
			return err
		}

		if err := bumpVersion(a, 114); err != nil {
			return err
		}
	}

	if a.version == 114 {
		// Perform database upgrade from version 114 to version 115.

		// Find relevant subscriptions for given users efficiently, and use the join key too.
		if _, err := a.db.Exec("CREATE INDEX idx_subs_user_topic_del ON subscriptions(userid, topic, deletedat)"); err != nil {
			return err
		}

		// Optimizes join; state filters; seqid supports the SUM operation.
		if _, err := a.db.Exec("CREATE INDEX idx_topics_name_state_seqid ON topics(name, state, seqid)"); err != nil {
			return err
		}

		if err := bumpVersion(a, 115); err != nil {
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
	query := `INSERT INTO topics(createdat,updatedat,state,touchedat,name,access,public)
				VALUES(?,?,?,?,'sys','{"Auth": "N","Anon": "N"}','{"fn": "System"}')`
	_, err := tx.Exec(query, now, now, t.StateOK, now)
	return err
}

func addTags(tx *sqlx.Tx, table, keyName string, keyVal any, tags []string, ignoreDups bool) error {

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

func removeTags(tx *sqlx.Tx, table, keyName string, keyVal any, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	var args []any
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
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	decoded_uid := store.DecodeUid(user.Uid())
	if _, err = tx.Exec("INSERT INTO users(id,createdat,updatedat,state,access,public,trusted,tags) VALUES(?,?,?,?,?,?,?,?)",
		decoded_uid,
		user.CreatedAt, user.UpdatedAt,
		user.State, user.Access,
		common.ToJSON(user.Public), common.ToJSON(user.Trusted), user.Tags); err != nil {
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.ExecContext(ctx, "INSERT INTO auth(uname,userid,scheme,authLvl,secret,expires) VALUES(?,?,?,?,?,?)",
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.ExecContext(ctx, "DELETE FROM auth WHERE userid=? AND scheme=?", store.DecodeUid(user), scheme)
	return err
}

// AuthDelAllRecords deletes all authentication records for the user.
func (a *adapter) AuthDelAllRecords(user t.Uid) (int, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	res, err := a.db.ExecContext(ctx, "DELETE FROM auth WHERE userid=?", store.DecodeUid(user))
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()

	return int(count), nil
}

// Update user's authentication unique, secret, auth level.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) error {

	params := []string{"authLvl=?"}
	args := []any{authLvl}

	if unique != "" {
		params = append(params, "uname=?")
		args = append(args, unique)
	}
	if len(secret) > 0 {
		params = append(params, "secret=?")
		args = append(args, secret)
	}
	if !expires.IsZero() {
		params = append(params, "expires=?")
		args = append(args, expires)
	}
	args = append(args, store.DecodeUid(uid), scheme)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	sql := "UPDATE auth SET " + strings.Join(params, ",") + " WHERE userid=? AND scheme=?"
	resp, err := a.db.ExecContext(ctx, sql, args...)
	if isDupe(err) {
		return t.ErrDuplicate
	}

	if count, _ := resp.RowsAffected(); count <= 0 {
		return t.ErrNotFound
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	if err := a.db.GetContext(ctx, &record, "SELECT uname,secret,expires,authlvl FROM auth WHERE userid=? AND scheme=?",
		store.DecodeUid(uid), scheme); err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - use standard error.
			err = t.ErrNotFound
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	if err := a.db.GetContext(ctx, &record, "SELECT userid,secret,expires,authlvl FROM auth WHERE uname=?", unique); err != nil {
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var user t.User
	err := a.db.GetContext(ctx, &user, "SELECT * FROM users WHERE id=? AND state!=?", store.DecodeUid(uid), t.StateDeleted)
	if err == nil {
		user.SetUid(uid)
		user.Public = common.FromJSON(user.Public)
		user.Trusted = common.FromJSON(user.Trusted)
		return &user, nil
	}

	if err == sql.ErrNoRows {
		// Clear the error if user does not exist or marked as soft-deleted.
		return nil, nil
	}

	return nil, err
}

func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]any, len(ids))
	for i, id := range ids {
		uids[i] = store.DecodeUid(id)
	}

	users := []t.User{}
	q, uids, _ := sqlx.In("SELECT * FROM users WHERE id IN (?) AND state!=?", uids, t.StateDeleted)
	q = a.db.Rebind(q)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, uids...)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var user t.User
		if err = rows.StructScan(&user); err != nil {
			users = nil
			break
		}

		if user.State == t.StateDeleted {
			continue
		}

		user.SetUid(common.EncodeUidString(user.Id))
		user.Public = common.FromJSON(user.Public)
		user.Trusted = common.FromJSON(user.Trusted)

		users = append(users, user)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return users, err
}

// UserDelete deletes specified user: wipes completely (hard-delete) or marks as deleted.
// TODO: report when the user is not found.
func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := t.TimeNow()
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

		// Delete topic tags.
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
		if _, err = tx.Exec("UPDATE topics SET updatedat=?,touchedat=?,state=?,stateat=? WHERE owner=?",
			now, now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}
		// Disable p2p topics with the user (p2p topic's owner is 0).
		if _, err = tx.Exec("UPDATE topics LEFT JOIN subscriptions ON topics.name=subscriptions.topic "+
			"SET topics.updatedat=?,topics.touchedat=?,topics.state=?,topics.stateat=? "+
			"WHERE topics.owner=0 AND subscriptions.userid=?",
			now, now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}

		// Disable the other user's subscription to a disabled p2p topic.
		if _, err = tx.Exec("UPDATE subscriptions AS s_one LEFT JOIN subscriptions AS s_two "+
			"ON s_one.topic=s_two.topic "+
			"SET s_two.updatedat=?, s_two.deletedat=? WHERE s_one.userid=? AND s_one.topic LIKE 'p2p%'",
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
func (a *adapter) topicStateForUser(tx *sqlx.Tx, decoded_uid int64, now time.Time, update any) error {
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
func (a *adapter) UserUpdate(uid t.Uid, update map[string]any) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cols, args := common.UpdateByMap(update)
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
	if tags := common.ExtractTags(update); tags != nil {
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
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var decoded_uid int64
	err := a.db.GetContext(ctx, &decoded_uid, "SELECT userid FROM credentials WHERE synthetic=?", method+":"+value)
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
// the R permission. If read fails, the counts are still returned with the original
// user IDs but with the unread count undefined and non-nil error.
func (a *adapter) UserUnreadCount(ids ...t.Uid) (map[t.Uid]int, error) {
	uids := make([]any, len(ids))
	counts := make(map[t.Uid]int, len(ids))
	for i, id := range ids {
		uids[i] = store.DecodeUid(id)
		// Ensure all original uids are always present.
		counts[id] = 0
	}

	q, uids, _ := sqlx.In("SELECT s.userid, SUM(t.seqid)-SUM(s.readseqid) AS unreadcount FROM topics AS t, subscriptions AS s "+
		"WHERE s.userid IN (?) AND t.name=s.topic AND s.deletedat IS NULL AND t.state!=? AND "+
		"INSTR(s.modewant, 'R')>0 AND INSTR(s.modegiven, 'R')>0 GROUP BY s.userid", uids, t.StateDeleted)
	q = a.db.Rebind(q)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.QueryxContext(ctx, q, uids...)
	if err != nil {
		return counts, err
	}

	var userId int64
	var unreadCount int
	for rows.Next() {
		if err = rows.Scan(&userId, &unreadCount); err != nil {
			break
		}
		counts[store.EncodeUid(userId)] = unreadCount
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return counts, err
}

// UserGetUnvalidated returns a list of uids which have never logged in, have no
// validated credentials and haven't been updated since lastUpdatedBefore.
func (a *adapter) UserGetUnvalidated(lastUpdatedBefore time.Time, limit int) ([]t.Uid, error) {
	var uids []t.Uid

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.QueryxContext(ctx,
		"SELECT u.id, IFNULL(SUM(c.done),0) AS total FROM users AS u "+
			"LEFT JOIN credentials AS c ON u.id=c.userid WHERE u.lastseen IS NULL AND u.updatedat<? "+
			"GROUP BY u.id, u.updatedat HAVING total=0 ORDER BY u.updatedat ASC LIMIT ?", lastUpdatedBefore, limit)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var userId int64
		var unused int
		if err = rows.Scan(&userId, &unused); err != nil {
			break
		}
		uids = append(uids, store.EncodeUid(userId))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return uids, err
}

// *****************************

func (a *adapter) topicCreate(tx *sqlx.Tx, topic *t.Topic) error {
	_, err := tx.Exec("INSERT INTO topics(createdat,updatedat,touchedat,state,name,usebt,owner,access,public,trusted,tags,aux) "+
		"VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
		topic.CreatedAt, topic.UpdatedAt, topic.TouchedAt, topic.State, topic.Id, topic.UseBt,
		store.DecodeUid(t.ParseUid(topic.Owner)), topic.Access, common.ToJSON(topic.Public), common.ToJSON(topic.Trusted),
		topic.Tags, common.ToJSON(topic.Aux))
	if err != nil {
		return err
	}

	// Save topic's tags to a separate table to make topic findable.
	return addTags(tx, "topictags", "topic", topic.Id, topic.Tags, false)
}

// TopicCreate saves topic object to database.
func (a *adapter) TopicCreate(topic *t.Topic) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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

	jpriv := common.ToJSON(sub.Private)
	decoded_uid := store.DecodeUid(t.ParseUid(sub.User))
	_, err := tx.Exec(
		"INSERT INTO subscriptions(createdat,updatedat,deletedat,userid,topic,modeWant,modeGiven,private) "+
			"VALUES(?,?,NULL,?,?,?,?,?)",
		sub.CreatedAt, sub.UpdatedAt, decoded_uid, sub.Topic, sub.ModeWant.String(), sub.ModeGiven.String(), jpriv)

	if err != nil && isDupe(err) {
		if undelete {
			_, err = tx.Exec("UPDATE subscriptions SET createdat=?,updatedat=?,deletedat=NULL,modeWant=?,modeGiven=?,"+
				"delid=0,recvseqid=0,readseqid=0 WHERE topic=? AND userid=?",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeWant.String(), sub.ModeGiven.String(), sub.Topic, decoded_uid)
		} else {
			_, err = tx.Exec("UPDATE subscriptions SET createdat=?,updatedat=?,deletedat=NULL,modeWant=?,modeGiven=?,"+
				"delid=0,recvseqid=0,readseqid=0,private=? WHERE topic=? AND userid=?",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeWant.String(), sub.ModeGiven.String(), jpriv,
				sub.Topic, decoded_uid)
		}
	}
	if err == nil && isOwner {
		_, err = tx.Exec("UPDATE topics SET owner=? WHERE name=?", decoded_uid, sub.Topic)
	}
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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
	topic.SubCnt = 2
	err = a.topicCreate(tx, topic)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	// Fetch topic by name
	var tt = new(t.Topic)
	if err := a.db.GetContext(ctx, tt,
		"SELECT createdat,updatedat,state,stateat,touchedat,name AS id,usebt,access,owner,seqid,delid,subcnt,public,trusted,tags,aux "+
			"FROM topics WHERE name=?",
		topic); err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	// Topic found, get subsription count. Try both topic and channel names.
	channel := t.GrpToChn(topic)
	if err := a.db.GetContext(ctx, &tt.SubCnt,
		"SELECT COUNT(*) FROM subscriptions WHERE topic IN (?,?) AND deletedat IS NULL", topic, channel); err != nil {
		return nil, err
	}

	tt.Owner = common.EncodeUidString(tt.Owner).String()
	tt.Public = common.FromJSON(tt.Public)
	tt.Trusted = common.FromJSON(tt.Trusted)

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' & 'fnd' subscriptions.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch ALL user's subscriptions, even those which has not been modified recently.
	// We are going to use these subscriptions to fetch topics and users which may have been modified recently.
	q := `SELECT createdat,updatedat,deletedat,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`
	args := []any{store.DecodeUid(uid)}
	if !keepDeleted {
		// Filter out deleted rows.
		q += " AND deletedat IS NULL"
	}

	limit := 0
	ims := time.Time{}
	if opts != nil {
		if opts.Topic != "" {
			q += " AND topic=?"
			args = append(args, opts.Topic)
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

	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (p2p) and topics table (grp).
	// Prepare a list of separate subscriptions to users vs topics
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]any, 0, 16)
	usrq := make([]any, 0, 16)
	for rows.Next() {
		var sub t.Subscription
		if err = rows.StructScan(&sub); err != nil {
			break
		}
		tname := sub.Topic
		sub.User = uid.String()
		tcat := t.GetTopicCat(tname)

		if tcat == t.TopicCatMe || tcat == t.TopicCatFnd {
			// One of 'me', 'fnd' subscriptions, skip. Don't skip 'sys' subscription.
			continue
		} else if tcat == t.TopicCatP2P {
			// P2P subscription, find the other user to get user.Public and user.Trusted.
			uid1, uid2, _ := t.ParseP2P(tname)
			if uid1 == uid {
				usrq = append(usrq, store.DecodeUid(uid2))
				sub.SetWith(uid2.UserId())
			} else {
				usrq = append(usrq, store.DecodeUid(uid1))
				sub.SetWith(uid1.UserId())
			}
			topq = append(topq, tname)
		} else {
			// Group or 'sys' subscription.
			if tcat == t.TopicCatGrp {
				// Maybe convert channel name to topic name.
				tname = t.ChnToGrp(tname)
			}
			topq = append(topq, tname)
		}
		sub.Private = common.FromJSON(sub.Private)
		join[tname] = sub
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	if len(join) == 0 {
		return subs, nil
	}

	// Fetch grp topics and join to subscriptions.
	if len(topq) > 0 {
		q = "SELECT updatedat,state,touchedat,name AS id,usebt,access,seqid,delid,public,trusted " +
			"FROM topics WHERE name IN (?)"
		q, args, _ = sqlx.In(q, topq)

		if !keepDeleted {
			// Optionally skip deleted topics.
			q += " AND state!=?"
			args = append(args, t.StateDeleted)
		}

		if !ims.IsZero() {
			// Use cache timestamp if provided: get newer entries only.
			q += " AND touchedat>?"
			args = append(args, ims)

			if limit > 0 && limit < len(topq) {
				// No point in fetching more than the requested limit.
				q += " ORDER BY touchedat LIMIT ?"
				args = append(args, limit)
			}
		}
		q = a.db.Rebind(q)

		ctx2, cancel2 := a.getContext()
		if cancel2 != nil {
			defer cancel2()
		}
		rows, err = a.db.QueryxContext(ctx2, q, args...)
		if err != nil {
			return nil, err
		}

		var top t.Topic
		for rows.Next() {
			if err = rows.StructScan(&top); err != nil {
				break
			}

			sub := join[top.Id]
			// Check if sub.UpdatedAt needs to be adjusted to earlier or later time.
			sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, top.UpdatedAt)
			sub.SetState(top.State)
			sub.SetTouchedAt(top.TouchedAt)
			sub.SetSeqId(top.SeqId)
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				sub.SetPublic(common.FromJSON(top.Public))
				sub.SetTrusted(common.FromJSON(top.Trusted))
			}
			// Put back the updated value of a subsription, will process further below
			join[top.Id] = sub
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()

		if err != nil {
			return nil, err
		}
	}

	// Fetch p2p users and join to p2p subscriptions.
	if len(usrq) > 0 {
		q = "SELECT id,updatedat,state,access,lastseen,useragent,public,trusted " +
			"FROM users WHERE id IN (?)"
		q, args, _ = sqlx.In(q, usrq)
		if !keepDeleted {
			// Optionally skip deleted users.
			q += " AND state!=?"
			args = append(args, t.StateDeleted)
		}

		// Ignoring ims: we need all users to get LastSeen and UserAgent.

		q = a.db.Rebind(q)

		ctx3, cancel3 := a.getContext()
		if cancel3 != nil {
			defer cancel3()
		}
		rows, err = a.db.QueryxContext(ctx3, q, args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var usr2 t.User
			if err = rows.StructScan(&usr2); err != nil {
				break
			}

			joinOn := uid.P2PName(common.EncodeUidString(usr2.Id))
			if sub, ok := join[joinOn]; ok {
				sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, usr2.UpdatedAt)
				sub.SetState(usr2.State)
				sub.SetPublic(common.FromJSON(usr2.Public))
				sub.SetTrusted(common.FromJSON(usr2.Trusted))
				sub.SetDefaultAccess(usr2.Access.Auth, usr2.Access.Anon)
				sub.SetLastSeenAndUA(usr2.LastSeen, usr2.UserAgent)
				join[joinOn] = sub
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()

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

// UsersForTopic loads users subscribed to the given topic.
// The difference between UsersForTopic vs SubsForTopic is that the former loads user.Public,
// the latter does not.
func (a *adapter) UsersForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	tcat := t.GetTopicCat(topic)

	// Fetch all subscribed users. The number of users is not large
	q := `SELECT s.createdat,s.updatedat,s.deletedat,s.userid,s.topic,s.delid,s.recvseqid,
		s.readseqid,s.modewant,s.modegiven,u.public,u.trusted,u.lastseen,u.useragent,s.private
		FROM subscriptions AS s JOIN users AS u ON s.userid=u.id
		WHERE s.topic=?`
	args := []any{topic}
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
		// Ignore IfModifiedSince: loading all entries because a topic cannot have too many subscribers.
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	var lastSeen sql.NullTime
	var userAgent string
	var public, trusted any
	for rows.Next() {
		if err = rows.Scan(
			&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt,
			&sub.User, &sub.Topic, &sub.DelId, &sub.RecvSeqId,
			&sub.ReadSeqId, &sub.ModeWant, &sub.ModeGiven,
			&public, &trusted, &lastSeen, &userAgent, &sub.Private); err != nil {
			break
		}

		sub.User = common.EncodeUidString(sub.User).String()
		sub.Private = common.FromJSON(sub.Private)
		sub.SetPublic(common.FromJSON(public))
		sub.SetTrusted(common.FromJSON(trusted))
		sub.SetLastSeenAndUA(&lastSeen.Time, userAgent)
		subs = append(subs, sub)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	if err == nil && tcat == t.TopicCatP2P && len(subs) > 0 {
		// Swap public & lastSeen values of P2P topics as expected.
		if len(subs) == 1 {
			// The other user is deleted, nothing we can do.
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
			userAgent = subs[0].GetUserAgent()
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

	return subs, err
}

// topicNamesForUser reads a slice of strings using provided query.
func (a *adapter) topicNamesForUser(uid t.Uid, sqlQuery string) ([]string, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, sqlQuery, store.DecodeUid(uid))
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
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return names, err
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid) ([]string, error) {
	return a.topicNamesForUser(uid, "SELECT name FROM topics WHERE owner=?")
}

// ChannelsForUser loads a slice of topic names where the user is a channel reader and notifications (P) are enabled.
func (a *adapter) ChannelsForUser(uid t.Uid) ([]string, error) {
	return a.topicNamesForUser(uid,
		"SELECT topic FROM subscriptions WHERE userid=? AND topic LIKE 'chn%' "+
			"AND INSTR(modewant, 'P')>0 AND INSTR(modegiven, 'P')>0 AND deletedat IS NULL")
}

func (a *adapter) TopicShare(shares []*t.Subscription) error {
	if len(shares) == 0 {
		return nil // Nothing to do
	}

	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	topic := shares[0].Topic
	for _, sub := range shares {
		if sub.Topic != topic {
			return fmt.Errorf("all subscriptions must be for the same topic, got %s vs %s", sub.Topic, topic)
		}
		err = createSubscription(tx, sub, true)
		if err != nil {
			return err
		}
	}

	if _, err = tx.Exec("UPDATE topics SET subcnt=subcnt+? WHERE name=?", len(shares), topic); err != nil {
		return err
	}

	return tx.Commit()
}

// TopicDelete deletes specified topic.
func (a *adapter) TopicDelete(topic string, isChan, hard bool) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// If the topic is a channel, must try to delete subscriptions under both grpXXX and chnXXX names.
	args := []any{topic}
	if isChan {
		args = append(args, t.GrpToChn(topic))
	}

	if hard {
		// Delete subscriptions. If this is a channel, delete both group subscriptions and channel subscriptions.
		q, args, _ := sqlx.In("DELETE FROM subscriptions WHERE topic IN (?)", args)
		q = tx.Rebind(q)
		if _, err = tx.Exec(q, args...); err != nil {
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

		q, args, _ := sqlx.In("UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic IN (?)", now, now, args)
		q = tx.Rebind(q)
		if _, err = tx.Exec(q, args...); err != nil {
			return err
		}

		if _, err = tx.Exec("UPDATE topics SET updatedat=?,touchedat=?,state=?,stateat=? WHERE name=?",
			now, now, t.StateDeleted, now, topic); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.ExecContext(ctx, "UPDATE topics SET seqid=?,touchedat=? WHERE name=?", msg.SeqId, msg.CreatedAt, topic)

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]any) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if t, u := update["TouchedAt"], update["UpdatedAt"]; t == nil && u != nil {
		update["TouchedAt"] = u
	}
	cols, args := common.UpdateByMap(update)
	args = append(args, topic)
	_, err = tx.Exec("UPDATE topics SET "+strings.Join(cols, ",")+" WHERE name=?", args...)
	if err != nil {
		return err
	}

	// Tags are also stored in a separate table
	if tags := common.ExtractTags(update); tags != nil {
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.ExecContext(ctx, "UPDATE topics SET owner=? WHERE name=?", store.DecodeUid(newOwner), topic)
	return err
}

// Get a subscription of a user to a topic.
func (a *adapter) SubscriptionGet(topic string, user t.Uid, keepDeleted bool) (*t.Subscription, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var sub t.Subscription
	err := a.db.GetContext(ctx, &sub, `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=? AND userid=?`,
		topic, store.DecodeUid(user))

	if err != nil {
		if err == sql.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	if !keepDeleted && sub.DeletedAt != nil {
		return nil, nil
	}

	sub.Private = common.FromJSON(sub.Private)

	return &sub, nil
}

// SubsForUser loads all user's subscriptions. Does NOT load Public or Private values and does
// not load deleted subscriptions.
func (a *adapter) SubsForUser(forUser t.Uid) ([]t.Subscription, error) {
	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven FROM subscriptions WHERE userid=? AND deletedat IS NULL`
	args := []any{store.DecodeUid(forUser)}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, args...)
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
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return subs, err
}

// SubsForTopic fetches all subsciptions for a topic. Does NOT load Public value.
// The difference between UsersForTopic vs SubsForTopic is that the former loads user.public+trusted,
// the latter does not.
func (a *adapter) SubsForTopic(topic string, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=?`

	args := []any{topic}
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	var subs []t.Subscription
	var ss t.Subscription
	for rows.Next() {
		if err = rows.StructScan(&ss); err != nil {
			break
		}

		ss.User = common.EncodeUidString(ss.User).String()
		ss.Private = common.FromJSON(ss.Private)
		subs = append(subs, ss)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return subs, err
}

// SubsUpdate updates one or multiple subscriptions to a topic.
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]any) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	cols, args := common.UpdateByMap(update)
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
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	decoded_id := store.DecodeUid(user)
	now := t.TimeNow()
	res, err := tx.ExecContext(ctx,
		"UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic=? AND userid=? AND deletedat IS NULL",
		now, now, topic, decoded_id)
	if err != nil {
		return err
	}

	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		// ensure tx.Rollback() above is ran
		err = t.ErrNotFound
		return err
	}

	// Remove records of messages soft-deleted by this user.
	_, err = tx.Exec("DELETE FROM dellog WHERE topic=? AND deletedfor=?", topic, decoded_id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// subsDelForUser marks user's subscriptions as deleted.
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

// SubsDelForUser marks user's subscriptions as deleted.
func (a *adapter) SubsDelForUser(user t.Uid, hard bool) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}

	tx, err := a.db.BeginTxx(ctx, nil)
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

// Find returns a list of users or group topics who match given tags, such as "email:jdoe@example.com" or "tel:+18003287448".
func (a *adapter) Find(caller, promoPrefix string, req [][]string, opt []string, activeOnly bool) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var args []any
	stateConstraint := ""
	if activeOnly {
		args = append(args, t.StateOK)
		stateConstraint = "u.state=? AND "
	}
	allReq := t.FlattenDoubleSlice(req)
	for _, tag := range append(allReq, opt...) {
		args = append(args, tag)
		index[tag] = struct{}{}
	}
	var matcher string
	if promoPrefix != "" {
		// The max number of tags is 16. Using 20 to make sure one prefix match is greater than all non-prefix matches.
		matcher = "SUM(CASE WHEN LOCATE('" + promoPrefix + "', tg.tag)=1 THEN 20 ELSE 1 END)"
	} else {
		matcher = "COUNT(*)"
	}

	query := "SELECT u.id,u.createdat,u.updatedat,0,u.access,0,u.public,u.trusted,u.tags," + matcher + " AS matches " +
		"FROM users AS u LEFT JOIN usertags AS tg ON tg.userid=u.id " +
		"WHERE " + stateConstraint + "tg.tag IN (?" + strings.Repeat(",?", len(allReq)+len(opt)-1) + ") " +
		"GROUP BY u.id,u.createdat,u.updatedat,u.access,u.public,u.trusted,u.tags "
	if len(allReq) > 0 {
		q, a := common.DisjunctionSql(req, "tg.tag")
		query += q
		args = append(args, a...)
	}

	query += "UNION ALL "

	if activeOnly {
		args = append(args, t.StateOK)
		stateConstraint = "t.state=? AND "
	} else {
		stateConstraint = ""
	}
	for _, tag := range append(allReq, opt...) {
		args = append(args, tag)
	}

	query += "SELECT t.name AS topic,t.createdat,t.updatedat,t.usebt,t.access,t.subcnt,t.public,t.trusted,t.tags," + matcher + " AS matches " +
		"FROM topics AS t LEFT JOIN topictags AS tg ON t.name=tg.topic " +
		"WHERE " + stateConstraint + "tg.tag IN (?" + strings.Repeat(",?", len(allReq)+len(opt)-1) + ") " +
		"GROUP BY t.name,t.createdat,t.updatedat,t.usebt,t.access,t.subcnt,t.public,t.trusted,t.tags "
	if len(allReq) > 0 {
		q, a := common.DisjunctionSql(req, "tg.tag")
		query += q
		args = append(args, a...)
	}
	query += "ORDER BY matches DESC LIMIT ?"
	args = append(args, a.maxResults)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	// Get users matched by tags, sort by number of matches from high to low.
	rows, err := a.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Fetch subscriptions
	var public, trusted any
	var access t.DefaultAccess
	var subcnt int
	var setTags t.StringSlice
	var ignored int
	var isChan bool
	var sub t.Subscription
	var subs []t.Subscription
	for rows.Next() {
		if err = rows.Scan(&sub.Topic, &sub.CreatedAt, &sub.UpdatedAt, &isChan, &access, &subcnt,
			&public, &trusted, &setTags, &ignored); err != nil {
			subs = nil
			break
		}

		if id, err := strconv.ParseInt(sub.Topic, 10, 64); err == nil {
			sub.Topic = store.EncodeUid(id).UserId()
			if sub.Topic == caller {
				// Skip the callee
				continue
			}
		}

		if isChan {
			// This is a channel, convert grp to chn name.
			sub.Topic = t.GrpToChn(sub.Topic)
		}

		sub.SetSubCnt(subcnt)
		sub.SetPublic(common.FromJSON(public))
		sub.SetTrusted(common.FromJSON(trusted))
		sub.SetDefaultAccess(access.Auth, access.Anon)
		// Indicating that the mode is not set, not 'N'.
		sub.ModeGiven = t.ModeUnset
		sub.ModeWant = t.ModeUnset
		sub.Private = common.FilterFoundTags(setTags, index)
		subs = append(subs, sub)
	}
	if err == nil {
		err = rows.Err()
	}

	return subs, err
}

// FindOne returns topic or user which matches the given tag.
func (a *adapter) FindOne(tag string) (string, error) {
	var args []any

	query := "SELECT t.name AS topic FROM topics AS t LEFT JOIN topictags AS tt ON t.name=tt.topic " +
		"WHERE tt.tag=?"
	args = append(args, tag)

	query += " UNION ALL "

	query += "SELECT u.id AS topic FROM users AS u LEFT JOIN usertags AS ut ON ut.userid=u.id " +
		"WHERE ut.tag=?"
	args = append(args, tag)

	// LIMIT is applied to all resultant rows.
	query += " LIMIT 1"

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return "", err
	}

	var found string
	if rows.Next() {
		if err = rows.Scan(&found); err != nil {
			return "", err
		}

		// User IDs are returned as decoded decimal strings.
		if id, err := strconv.ParseInt(found, 10, 64); err == nil {
			found = store.EncodeUid(id).UserId()
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

	return found, err
}

// Messages
func (a *adapter) MessageSave(msg *t.Message) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	// store assignes message ID, but we don't use it. Message IDs are not used anywhere.
	// Using a sequential ID provided by the database.
	res, err := a.db.ExecContext(
		ctx,
		"INSERT INTO messages(createdAt,updatedAt,seqid,topic,`from`,head,content) VALUES(?,?,?,?,?,?,?)",
		msg.CreatedAt, msg.UpdatedAt, msg.SeqId, msg.Topic,
		store.DecodeUid(t.ParseUid(msg.From)), msg.Head, common.ToJSON(msg.Content))
	if err == nil {
		id, _ := res.LastInsertId()
		// Replacing ID given by store by ID given by the DB.
		msg.SetUid(t.Uid(id))
	}
	return err
}

func (a *adapter) MessageGetAll(topic string, forUser t.Uid, opts *t.QueryOpt) ([]t.Message, error) {
	var limit = a.maxMessageResults

	args := []any{store.DecodeUid(forUser), topic}
	seqIdConstraint := ""
	if opts != nil {
		seqIdConstraint = "AND m.seqid "
		if len(opts.IdRanges) > 0 {
			constr, newargs := common.RangesToSql(opts.IdRanges)
			seqIdConstraint += constr
			args = append(args, newargs...)
		} else {
			seqIdConstraint += "BETWEEN ? AND ?"
			if opts.Since > 0 {
				args = append(args, opts.Since)
			} else {
				args = append(args, 0)
			}
			if opts.Before > 0 {
				// MySQL BETWEEN is inclusive-inclusive, Tinode API requires inclusive-exclusive, thus -1
				args = append(args, opts.Before-1)
			} else {
				args = append(args, 1<<31-1)
			}
		}

		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
	}

	args = append(args, limit)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.QueryxContext(
		ctx,
		"SELECT m.createdat,m.updatedat,m.deletedat,m.delid,m.seqid,m.topic,m.`from`,m.head,m.content"+
			" FROM messages AS m LEFT JOIN dellog AS d"+
			" ON d.topic=m.topic AND m.seqid BETWEEN d.low AND d.hi-1 AND d.deletedfor=?"+
			" WHERE m.delid=0 AND m.topic=? "+seqIdConstraint+" AND d.deletedfor IS NULL"+
			" ORDER BY m.seqid DESC LIMIT ?",
		args...)

	if err != nil {
		return nil, err
	}

	msgs := make([]t.Message, 0, limit)
	for rows.Next() {
		var msg t.Message
		if err = rows.StructScan(&msg); err != nil {
			break
		}
		msg.From = common.EncodeUidString(msg.From).String()
		msg.Content = common.FromJSON(msg.Content)
		msgs = append(msgs, msg)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return msgs, err
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, "SELECT topic,deletedfor,delid,low,hi FROM dellog WHERE topic=? AND delid BETWEEN ? AND ?"+
		" AND (deletedFor=0 OR deletedFor=?)"+
		" ORDER BY delid LIMIT ?", topic, lower, upper, store.DecodeUid(forUser), limit)
	if err != nil {
		return nil, err
	}

	var dellog struct {
		Topic      string
		Deletedfor int64
		Delid      int
		Low        int
		Hi         int
	}
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
		dmsg.SeqIdRanges = append(dmsg.SeqIdRanges, t.Range{Low: dellog.Low, Hi: dellog.Hi})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()

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
		return err
	}

	// Only some messages are being deleted.

	delRanges := toDel.SeqIdRanges

	if toDel.DeletedFor == "" {
		// Hard-deleting messages requires updates to the messages table.
		where := "m.topic=?"
		args := []any{topic}

		if len(delRanges) > 0 {
			rSql, rArgs := common.RangesToSql(delRanges)
			where += " AND m.seqid " + rSql
			args = append(args, rArgs...)
		}

		where += " AND m.deletedat IS NULL"

		// We are asked to delete messages no older than newerThan.
		if newerThan := toDel.GetNewerThan(); newerThan != nil {
			where += " AND m.createdat>?"
			args = append(args, newerThan)
		}

		// Find the actual IDs still present in the database.
		var seqIDs []int
		err = tx.Select(&seqIDs, "SELECT seqid FROM messages AS m WHERE "+where, args...)
		if err != nil {
			return err
		}

		if len(seqIDs) == 0 {
			// Nothing to delete. No need to make a log entry. All done.
			return nil
		}

		// Recalculate the actual ranges to delete.
		sort.Ints(seqIDs)
		delRanges = t.SliceToRanges(seqIDs)

		// Compose a new query with the new ranges.
		where = "m.topic=?"
		args = []any{topic}
		rSql, rArgs := common.RangesToSql(delRanges)
		where += " AND m.seqid " + rSql
		args = append(args, rArgs...)

		// No need to add anything else: deletedat etc is already accounted for.

		_, err = tx.Exec("DELETE fml.* FROM filemsglinks AS fml INNER JOIN messages AS m ON m.id=fml.msgid WHERE "+
			where, args...)
		if err != nil {
			return err
		}

		// Instead of deleting messages, clear all content.
		_, err = tx.Exec("UPDATE messages AS m SET m.deletedat=?,m.delId=?,m.`from`=0,m.head=NULL,m.content=NULL WHERE "+
			where, append([]any{t.TimeNow(), toDel.DelId}, args...)...)
		if err != nil {
			return err
		}
	}

	// Now make log entries. Needed for both hard- and soft-deleting.
	var insert *sql.Stmt
	if insert, err = tx.Prepare(
		"INSERT INTO dellog(topic,deletedfor,delid,low,hi) VALUES(?,?,?,?,?)"); err != nil {
		return err
	}

	forUser := common.DecodeUidString(toDel.DeletedFor)
	for _, rng := range delRanges {
		if rng.Hi == 0 {
			// Dellog must contain valid Low and *Hi*.
			rng.Hi = rng.Low + 1
		}
		// A log entry for each range.
		if _, err = insert.Exec(topic, forUser, toDel.DelId, rng.Low, rng.Hi); err != nil {
			break
		}
	}

	return err
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list.
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) (err error) {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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

	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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
	var unums []any
	for _, uid := range uids {
		unums = append(unums, store.DecodeUid(uid))
	}

	q, unums, _ := sqlx.In("SELECT userid,deviceid,platform,lastseen,lang FROM devices WHERE userid IN (?)", unums)
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.QueryxContext(ctx, q, unums...)
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
	if err == nil {
		err = rows.Err()
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
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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

	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := t.TimeNow()
	userId := common.DecodeUidString(cred.User)

	// Enforce uniqueness: if credential is confirmed, "method:value" must be unique.
	// if credential is not yet confirmed, "userid:method:value" is unique.
	synth := cred.Method + ":" + cred.Value

	if !cred.Done {
		// Check if this credential is already validated.
		var done bool
		err = tx.Get(&done, "SELECT done FROM credentials WHERE synthetic=?", synth)
		if err == nil {
			// Assign err to ensure closing of a transaction.
			err = t.ErrDuplicate
			return false, err
		}
		if err != sql.ErrNoRows {
			return false, err
		}
		// We are going to insert new record.
		synth = cred.User + ":" + synth

		// Adding new unvalidated credential. Deactivate all unvalidated records of this user and method.
		_, err = tx.Exec("UPDATE credentials SET deletedat=? WHERE userid=? AND method=? AND done=FALSE",
			now, userId, cred.Method)
		if err != nil {
			return false, err
		}
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
	args := []any{store.DecodeUid(uid)}

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
	args = append([]any{t.TimeNow()}, args...)
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
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	res, err := a.db.ExecContext(
		ctx,
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.ExecContext(ctx, "UPDATE credentials SET updatedat=?,retries=retries+1 WHERE userid=? AND method=? AND done=false",
		t.TimeNow(), store.DecodeUid(uid), method)
	return err
}

// CredGetActive returns currently active unvalidated credential of the given user and method.
func (a *adapter) CredGetActive(uid t.Uid, method string) (*t.Credential, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var cred t.Credential
	err := a.db.GetContext(ctx, &cred, "SELECT createdat,updatedat,method,value,resp,done,retries "+
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
	args := []any{store.DecodeUid(uid)}
	if method != "" {
		query += " AND method=?"
		args = append(args, method)
	}
	if validatedOnly {
		query += " AND done=true"
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var credentials []t.Credential
	err := a.db.SelectContext(ctx, &credentials, query, args...)
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
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var user any
	if fd.User != "" {
		user = store.DecodeUid(t.ParseUid(fd.User))
	} else {
		user = 0
	}
	_, err := a.db.ExecContext(ctx,
		"INSERT INTO fileuploads(id,createdat,updatedat,userid,status,mimetype,size,etag,location) "+
			"VALUES(?,?,?,?,?,?,?,?,?)",
		store.DecodeUid(fd.Uid()), fd.CreatedAt, fd.UpdatedAt, user,
		fd.Status, fd.MimeType, fd.Size, fd.ETag, fd.Location)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise
func (a *adapter) FileFinishUpload(fd *t.FileDef, success bool, size int64) (*t.FileDef, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	now := t.TimeNow()
	if success {
		_, err = tx.ExecContext(ctx, "UPDATE fileuploads SET updatedat=?,status=?,size=?,etag=?,location=? WHERE id=?",
			now, t.UploadCompleted, size, fd.ETag, fd.Location, store.DecodeUid(fd.Uid()))
		if err != nil {
			return nil, err
		}

		fd.Status = t.UploadCompleted
		fd.Size = size
	} else {
		// Deleting the record: there is no value in keeping it in the DB.
		_, err = tx.ExecContext(ctx, "DELETE FROM fileuploads WHERE id=?", store.DecodeUid(fd.Uid()))
		if err != nil {
			return nil, err
		}

		fd.Status = t.UploadFailed
		fd.Size = 0
	}
	fd.UpdatedAt = now

	return fd, tx.Commit()
}

// FileGet fetches a record of a specific file
func (a *adapter) FileGet(fid string) (*t.FileDef, error) {
	id := t.ParseUid(fid)
	if id.IsZero() {
		return nil, t.ErrMalformed
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var fd t.FileDef
	err := a.db.GetContext(ctx, &fd, "SELECT id,createdat,updatedat,userid AS user,status,mimetype,size,IFNULL(etag,'') AS etag,location "+
		"FROM fileuploads WHERE id=?", store.DecodeUid(id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	fd.Id = common.EncodeUidString(fd.Id).String()
	fd.User = common.EncodeUidString(fd.User).String()

	return &fd, nil

}

// FileDeleteUnused deletes file upload records.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Garbage collecting entries which as either marked as deleted, or lack message references, or have no user assigned.
	query := "SELECT fu.id,fu.location FROM fileuploads AS fu LEFT JOIN filemsglinks AS fml ON fml.fileid=fu.id " +
		"WHERE fml.id IS NULL"
	var args []any
	if !olderThan.IsZero() {
		query += " AND fu.updatedat<?"
		args = append(args, olderThan)
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}

	var locations []string
	var ids []any
	for rows.Next() {
		var id int
		var loc string
		if err = rows.Scan(&id, &loc); err != nil {
			break
		}
		if loc != "" {
			locations = append(locations, loc)
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
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

// FileLinkAttachments connects given topic or message to the file record IDs from the list.
func (a *adapter) FileLinkAttachments(topic string, userId, msgId t.Uid, fids []string) error {
	if len(fids) == 0 || (topic == "" && msgId.IsZero() && userId.IsZero()) {
		return t.ErrMalformed
	}
	now := t.TimeNow()

	var args []any
	var linkId any
	var linkBy string
	if !msgId.IsZero() {
		linkBy = "msgid"
		linkId = int64(msgId)
	} else if topic != "" {
		linkBy = "topic"
		linkId = topic
		// Only one attachment per topic is permitted at this time.
		fids = fids[0:1]
	} else {
		linkBy = "userid"
		linkId = store.DecodeUid(userId)
		// Only one attachment per user is permitted at this time.
		fids = fids[0:1]
	}

	// Decoded ids
	var dids []any
	for _, fid := range fids {
		id := t.ParseUid(fid)
		if id.IsZero() {
			return t.ErrMalformed
		}
		dids = append(dids, store.DecodeUid(id))
	}

	for _, id := range dids {
		// createdat,fileid,[msgid|topic|userid]
		args = append(args, now, id, linkId)
	}

	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Unlink earlier uploads on the same topic or user allowing them to be garbage-collected.
	if msgId.IsZero() {
		sql := "DELETE FROM filemsglinks WHERE " + linkBy + "=?"
		_, err = tx.Exec(sql, linkId)
		if err != nil {
			return err
		}
	}

	sql := "INSERT INTO filemsglinks(createdat,fileid," + linkBy + ") VALUES (?,?,?)"
	_, err = tx.Exec(sql+strings.Repeat(",(?,?,?)", len(dids)-1), args...)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// PCacheGet reads a persistet cache entry.
func (a *adapter) PCacheGet(key string) (string, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	var value string
	if err := a.db.GetContext(ctx, &value, "SELECT `value` FROM kvmeta WHERE `key`=? LIMIT 1", key); err != nil {
		if err == sql.ErrNoRows {
			return "", t.ErrNotFound
		}
		return "", err
	}
	return value, nil
}

// PCacheUpsert creates or updates a persistent cache entry.
func (a *adapter) PCacheUpsert(key string, value string, failOnDuplicate bool) error {
	if strings.Contains(key, "%") {
		// Do not allow % in keys: it interferes with LIKE query.
		return t.ErrMalformed
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	var action string
	if failOnDuplicate {
		action = "INSERT"
	} else {
		action = "REPLACE"
	}

	_, err := a.db.ExecContext(ctx, action+" INTO kvmeta(`key`,createdat,`value`) VALUES(?,?,?)", key, t.TimeNow(), value)
	if isDupe(err) {
		return t.ErrDuplicate
	}
	return err
}

// PCacheDelete deletes one persistent cache entry.
func (a *adapter) PCacheDelete(key string) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	_, err := a.db.ExecContext(ctx, "DELETE FROM kvmeta WHERE `key`=?", key)
	return err
}

// PCacheExpire expires old entries with the given key prefix.
func (a *adapter) PCacheExpire(keyPrefix string, olderThan time.Time) error {
	if keyPrefix == "" {
		return t.ErrMalformed
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	_, err := a.db.ExecContext(ctx, "DELETE FROM kvmeta WHERE `key` LIKE ? AND createdat<?", keyPrefix+"%", olderThan)
	return err
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

func init() {
	store.RegisterAdapter(&adapter{})
}
