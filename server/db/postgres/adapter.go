//go:build postgres
// +build postgres

// Package postgres is a database adapter for PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/jmoiron/sqlx"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/db/common"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// adapter holds MySQL connection data.
type adapter struct {
	db         *pgxpool.Pool
	poolConfig *pgxpool.Config
	dsn        string
	dbName     string
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
	defaultDSN      = "postgresql://postgres:postgres@localhost:5432/tinode?sslmode=disable&connect_timeout=10"
	defaultDatabase = "tinode"

	adpVersion  = 113
	adapterName = "postgres"

	defaultMaxResults = 1024
	// This is capped by the Session's send queue limit (128).
	defaultMaxMessageResults = 100

	// If DB request timeout is specified,
	// we allocate txTimeoutMultiplier times more time for transactions.
	txTimeoutMultiplier = 1.5
)

type configType struct {
	// DB connection settings:
	// Using fields
	User   string `json:"user,omitempty"`
	Passwd string `json:"passwd,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   string `json:"port,omitempty"`
	DBName string `json:"dbname,omitempty"`
	// Deprecated.
	DSN      string `json:"dsn,omitempty"`
	Database string `json:"database,omitempty"`

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
		return errors.New("postgres adapter is already connected")
	}

	if len(jsonconfig) < 2 {
		return errors.New("adapter postgres missing config")
	}

	var err error
	var config configType
	ctx := context.Background()
	if err = json.Unmarshal(jsonconfig, &config); err != nil {
		return errors.New("postgres adapter failed to parse config: " + err.Error())
	}

	if config.DSN != "" {
		a.dsn = config.DSN
		a.dbName = config.Database
	} else {
		dsn, err := setConnStr(config)
		if err != nil {
			return err
		}
		a.dsn = dsn
		a.dbName = config.DBName
	}

	if a.dsn == "" {
		a.dsn = defaultDSN
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

	a.poolConfig, err = pgxpool.ParseConfig(a.dsn)
	if err != nil {
		return errors.New("adapter postgres failed to parse config: " + err.Error())
	}

	// ConnectConfig creates a new Pool and immediately establishes one connection.
	a.db, err = pgxpool.ConnectConfig(ctx, a.poolConfig)
	if isMissingDb(err) {
		// Missing DB is OK if we are initializing the database.
		// Since tinode DB does not exist, connect without specifying the DB name.
		a.poolConfig.ConnConfig.Database = ""
		a.db, err = pgxpool.ConnectConfig(ctx, a.poolConfig)
	}
	if err != nil {
		return err
	}

	// Actually opening the network connection if one was not opened earlier.
	if a.poolConfig.LazyConnect {
		err = a.db.Ping(ctx)
	}

	if err == nil {
		if config.MaxOpenConns > 0 {
			a.poolConfig.MaxConns = int32(config.MaxOpenConns)
		}
		if config.MaxIdleConns > 0 {
			a.poolConfig.MinConns = int32(config.MaxIdleConns)
		}
		if config.ConnMaxLifetime > 0 {
			a.poolConfig.MaxConnLifetime = time.Duration(config.ConnMaxLifetime) * time.Second
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
	if a.db != nil {
		a.db.Close()
		a.db = nil
		a.version = -1
	}
	return nil
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
	var vers string
	err := a.db.QueryRow(ctx, "SELECT value FROM kvmeta WHERE key = $1", "version").Scan(&vers)

	if err != nil {
		if isMissingDb(err) || isMissingTable(err) || err == pgx.ErrNoRows {
			err = errors.New("Database not initialized")
		}
		return -1, err
	}

	a.version, _ = strconv.Atoi(vers)

	return a.version, nil
}

func (a *adapter) updateDbVersion(v int) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	a.version = -1
	if _, err := a.db.Exec(ctx, "UPDATE kvmeta SET value = $1 WHERE key = $2", strconv.Itoa(v), "version"); err != nil {
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
func (a *adapter) Stats() interface{} {
	if a.db == nil {
		return nil
	}
	return a.db.Stat()
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
	var tx pgx.Tx

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	// Can't use an existing connection because it's configured with a database name which may not exist.
	// Don't care if it does not close cleanly.
	if a.db != nil {
		a.db.Close()
	}

	// Create default database name
	a.poolConfig.ConnConfig.Database = "postgres"

	a.db, err = pgxpool.ConnectConfig(ctx, a.poolConfig)
	if err != nil {
		return err
	}

	if reset {
		if _, err = a.db.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s;", a.dbName)); err != nil {
			return err
		}
	}

	if _, err = a.db.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s WITH ENCODING utf8;", a.dbName)); err != nil {
		return err
	}

	a.poolConfig.ConnConfig.Database = a.dbName
	a.db, err = pgxpool.ConnectConfig(ctx, a.poolConfig)
	if err != nil {
		return err
	}

	if tx, err = a.db.Begin(ctx); err != nil {
		return err
	}

	defer func() {
		if err != nil {
			// FIXME: This is useless: MySQL auto-commits on every CREATE TABLE.
			// Maybe DROP DATABASE instead.
			tx.Rollback(ctx)
		}
	}()

	// Indexed users.
	if _, err := tx.Exec(ctx,
		`CREATE TABLE users(
			id        BIGINT NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			state     SMALLINT NOT NULL DEFAULT 0,
			stateat   TIMESTAMP(3),
			access    JSON,
			lastseen  TIMESTAMP,
			useragent VARCHAR(255) DEFAULT '',
			public    JSON,
			trusted   JSON,
			tags      JSON,
			PRIMARY KEY(id)
		);
		CREATE INDEX users_state_stateat ON users(state, stateat);
		CREATE INDEX users_lastseen_updatedat ON users(lastseen, updatedat);`); err != nil {
		return err
	}

	// Indexed user tags.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE usertags(
			id     SERIAL NOT NULL,
			userid BIGINT NOT NULL,
			tag    VARCHAR(96) NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id)
		);
		CREATE INDEX usertags_tag ON usertags(tag);
		CREATE UNIQUE INDEX usertags_userid_tag ON usertags(userid, tag);`); err != nil {
		return err
	}

	// Indexed devices. Normalized into a separate table.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE devices(
			id       SERIAL NOT NULL,
			userid   BIGINT NOT NULL,
			hash     CHAR(16) NOT NULL,
			deviceid TEXT NOT NULL,
			platform VARCHAR(32),
			lastseen TIMESTAMP NOT NULL,
			lang     VARCHAR(8),
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id)
		);
		CREATE UNIQUE INDEX devices_hash ON devices(hash);`); err != nil {
		return err
	}

	// Authentication records for the basic authentication scheme.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE auth(
			id      SERIAL NOT NULL,
			uname   VARCHAR(32) NOT NULL,
			userid  BIGINT NOT NULL,
			scheme  VARCHAR(16) NOT NULL,
			authlvl INT NOT NULL,
			secret  VARCHAR(255) NOT NULL,
			expires TIMESTAMP,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id)
		);
		CREATE UNIQUE INDEX auth_userid_scheme ON auth(userid, scheme);
		CREATE UNIQUE INDEX auth_uname ON auth(uname);`); err != nil {
		return err
	}

	// Topics
	if _, err = tx.Exec(ctx,
		`CREATE TABLE topics(
			id        SERIAL NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			state     SMALLINT NOT NULL DEFAULT 0,
			stateat   TIMESTAMP(3),
			touchedat TIMESTAMP(3),
			name      VARCHAR(25) NOT NULL,
			usebt     BOOLEAN DEFAULT FALSE,
			owner     BIGINT NOT NULL DEFAULT 0,
			access    JSON,
			seqid     INT NOT NULL DEFAULT 0,
			delid     INT DEFAULT 0,
			public    JSON,
			trusted   JSON,
			tags      JSON,
			PRIMARY KEY(id)
		);
		CREATE UNIQUE INDEX topics_name ON topics(name);
		CREATE INDEX topics_owner ON topics(owner);
		CREATE INDEX topics_state_stateat ON topics(state, stateat);`); err != nil {
		return err
	}

	// Create system topic 'sys'.
	if err = createSystemTopic(tx); err != nil {
		return err
	}

	// Indexed topic tags.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE topictags(
			id    SERIAL NOT NULL,
			topic VARCHAR(25) NOT NULL,
			tag   VARCHAR(96) NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name)
		);
		CREATE INDEX topictags_tag ON topictags(tag);
		CREATE UNIQUE INDEX topictags_userid_tag ON topictags(topic, tag);`); err != nil {
		return err
	}

	// Subscriptions
	if _, err = tx.Exec(ctx,
		`CREATE TABLE subscriptions(
			id        SERIAL NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			deletedat TIMESTAMP(3),
			userid    BIGINT NOT NULL,
			topic     VARCHAR(25) NOT NULL,
			delid     INT DEFAULT 0,
			recvseqid INT DEFAULT 0,
			readseqid INT DEFAULT 0,
			modewant  VARCHAR(8),
			modegiven VARCHAR(8),
			private   JSON,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id)
		);
		CREATE UNIQUE INDEX subscriptions_topic_userid ON subscriptions(topic, userid);
		CREATE INDEX subscriptions_topic ON subscriptions(topic);
		CREATE INDEX subscriptions_deletedat ON subscriptions(deletedat);`); err != nil {
		return err
	}

	// Messages
	if _, err = tx.Exec(ctx,
		`CREATE TABLE messages(
			id        SERIAL NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			deletedat TIMESTAMP(3),
			delid     INT DEFAULT 0,
			seqid     INT NOT NULL,
			topic     VARCHAR(25) NOT NULL,
			"from"    BIGINT NOT NULL,
			head      JSON,
			content   JSON,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name)
		);
		CREATE UNIQUE INDEX messages_topic_seqid ON messages(topic, seqid);`); err != nil {
		return err
	}

	// Deletion log
	if _, err = tx.Exec(ctx,
		`CREATE TABLE dellog(
			id         SERIAL NOT NULL,
			topic      VARCHAR(25) NOT NULL,
			deletedfor BIGINT NOT NULL DEFAULT 0,
			delid      INT NOT NULL,
			low        INT NOT NULL,
			hi         INT NOT NULL,
			PRIMARY KEY(id),
			FOREIGN KEY(topic) REFERENCES topics(name)
		);
		CREATE INDEX dellog_topic_delid_deletedfor ON dellog(topic,delid,deletedfor);
		CREATE INDEX dellog_topic_deletedfor_low_hi ON dellog(topic,deletedfor,low,hi);
		CREATE INDEX dellog_deletedfor ON dellog(deletedfor);`); err != nil {
		return err
	}

	// User credentials
	if _, err = tx.Exec(ctx,
		`CREATE TABLE credentials(
			id        SERIAL NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			deletedat TIMESTAMP(3),
			method    VARCHAR(16) NOT NULL,
			value     VARCHAR(128) NOT NULL,
			synthetic VARCHAR(192) NOT NULL,
			userid    BIGINT NOT NULL,
			resp      VARCHAR(255),
			done      BOOLEAN NOT NULL DEFAULT FALSE,
			retries   INT NOT NULL DEFAULT 0,
			PRIMARY KEY(id),
			FOREIGN KEY(userid) REFERENCES users(id)
		);
		CREATE UNIQUE INDEX credentials_uniqueness ON credentials(synthetic);`); err != nil {
		return err
	}

	// Records of uploaded files.
	// Don't add FOREIGN KEY on userid. It's not needed and it will break user deletion.
	// Using INDEX rather than FK on topic because it's either 'topics' or 'users' reference.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE fileuploads(
			id        BIGINT NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			updatedat TIMESTAMP(3) NOT NULL,
			userid    BIGINT,
			status    INT NOT NULL,
			mimetype  VARCHAR(255) NOT NULL,
			size      BIGINT NOT NULL,
			location  VARCHAR(2048) NOT NULL,
			PRIMARY KEY(id)
		);
		CREATE INDEX fileuploads_status ON fileuploads(status);`); err != nil {
		return err
	}

	// Links between uploaded files and the topics, users or messages they are attached to.
	if _, err = tx.Exec(ctx,
		`CREATE TABLE filemsglinks(
			id        SERIAL NOT NULL,
			createdat TIMESTAMP(3) NOT NULL,
			fileid    BIGINT NOT NULL,
			msgid     INT,
			topic     VARCHAR(25),
			userid    BIGINT,
			PRIMARY KEY(id),
			FOREIGN KEY(fileid) REFERENCES fileuploads(id) ON DELETE CASCADE,
			FOREIGN KEY(msgid) REFERENCES messages(id) ON DELETE CASCADE,
			FOREIGN KEY(topic) REFERENCES topics(name) ON DELETE CASCADE,
			FOREIGN KEY(userid) REFERENCES users(id) ON DELETE CASCADE
		);`); err != nil {
		return err
	}

	if _, err = tx.Exec(ctx,
		`CREATE TABLE kvmeta(
			"key"     VARCHAR(64) NOT NULL,
			createdat TIMESTAMP(3),
			"value"   TEXT,
			PRIMARY KEY("key")
		);
		CREATE INDEX kvmeta_createdat_key ON kvmeta(createdat, "key");`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO kvmeta("key", "value") VALUES($1, $2)`, "version", strconv.Itoa(adpVersion)); err != nil {
		return err
	}

	return tx.Commit(ctx)
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	if a.version == 112 {
		// Perform database upgrade from version 112 to version 113.

		// Index for deleting unvalidated accounts.
		if _, err := a.db.Exec(ctx, "CREATE INDEX users_lastseen_updatedat ON users(lastseen,updatedat)"); err != nil {
			return err
		}

		// Allow lnger kvmeta keys.
		if _, err := a.db.Exec(ctx, `ALTER TABLE kvmeta ALTER COLUMN "key" TYPE VARCHAR(64)`); err != nil {
			return err
		}

		if _, err := a.db.Exec(ctx, `ALTER TABLE kvmeta ALTER COLUMN "key" SET NOT NULL`); err != nil {
			return err
		}

		// Add timestamp to kvmeta.
		if _, err := a.db.Exec(ctx, `ALTER TABLE kvmeta ADD COLUMN createdat TIMESTAMP(3)`); err != nil {
			return err
		}

		// Add compound index on the new field and key (could be searched by key prefix).
		if _, err := a.db.Exec(ctx, `CREATE INDEX kvmeta_createdat_key ON kvmeta(createdat, "key")`); err != nil {
			return err
		}

		if err := bumpVersion(a, 113); err != nil {
			return err
		}
	}

	if a.version != adpVersion {
		return errors.New("Failed to perform database upgrade to version " + strconv.Itoa(adpVersion) +
			". DB is still at " + strconv.Itoa(a.version))
	}
	return nil
}

func createSystemTopic(tx pgx.Tx) error {
	now := t.TimeNow()
	query := `INSERT INTO topics(createdat,updatedat,state,touchedat,name,access,public)
				VALUES($1,$2,$3,$4,'sys','{"Auth": "N","Anon": "N"}','{"fn": "System"}')`
	_, err := tx.Exec(context.Background(), query, now, now, t.StateOK, now)
	return err
}

func addTags(ctx context.Context, tx pgx.Tx, table, keyName string, keyVal interface{}, tags []string, ignoreDups bool) error {
	if len(tags) == 0 {
		return nil
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s, tag) VALUES($1,$2)", table, keyName)

	for _, tag := range tags {
		if _, err := tx.Exec(ctx, sql, keyVal, tag); err != nil {
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

func removeTags(ctx context.Context, tx pgx.Tx, table, keyName string, keyVal interface{}, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	sql, args := expandQuery(fmt.Sprintf("DELETE FROM %s WHERE %s=? AND tag = ANY (?)", table, keyName), keyVal, tags)
	_, err := tx.Exec(ctx, sql, args)

	return err
}

// UserCreate creates a new user. Returns error and true if error is due to duplicate user name,
// false for any other error
func (a *adapter) UserCreate(user *t.User) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	decoded_uid := store.DecodeUid(user.Uid())
	if _, err = tx.Exec(ctx,
		"INSERT INTO users(id,createdat,updatedat,state,access,public,trusted,tags) VALUES($1,$2,$3,$4,$5,$6,$7,$8);",
		decoded_uid,
		user.CreatedAt,
		user.UpdatedAt,
		user.State,
		user.Access,
		toJSON(user.Public),
		toJSON(user.Trusted),
		user.Tags); err != nil {
		return err
	}

	// Save user's tags to a separate table to make user findable.
	if err = addTags(ctx, tx, "usertags", "userid", decoded_uid, user.Tags, false); err != nil {
		return err
	}

	return tx.Commit(ctx)
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

	if _, err := a.db.Exec(ctx, "INSERT INTO auth(uname,userid,scheme,authLvl,secret,expires) VALUES($1,$2,$3,$4,$5,$6)",
		unique, store.DecodeUid(uid), scheme, authLvl, secret, exp); err != nil {
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
	_, err := a.db.Exec(ctx, "DELETE FROM auth WHERE userid=$1 AND scheme=$2", store.DecodeUid(user), scheme)
	return err
}

// AuthDelAllRecords deletes all authentication records for the user.
func (a *adapter) AuthDelAllRecords(user t.Uid) (int, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	res, err := a.db.Exec(ctx, "DELETE FROM auth WHERE userid=$1", store.DecodeUid(user))
	if err != nil {
		return 0, err
	}
	count := res.RowsAffected()

	return int(count), nil
}

// Update user's authentication unique, secret, auth level.
func (a *adapter) AuthUpdRecord(uid t.Uid, scheme, unique string, authLvl auth.Level,
	secret []byte, expires time.Time) error {

	parapg := []string{"authLvl=?"}
	args := []interface{}{authLvl}
	if unique != "" {
		parapg = append(parapg, "uname=?")
		args = append(args, unique)
	}
	if len(secret) > 0 {
		parapg = append(parapg, "secret=?")
		args = append(args, secret)
	}
	if !expires.IsZero() {
		parapg = append(parapg, "expires=?")
		args = append(args, expires)
	}
	args = append(args, store.DecodeUid(uid), scheme)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	sql, args := expandQuery("UPDATE auth SET "+strings.Join(parapg, ",")+" WHERE userid=? AND scheme=?", args...)
	resp, err := a.db.Exec(ctx, sql, args...)
	if isDupe(err) {
		return t.ErrDuplicate
	}

	if count := resp.RowsAffected(); count <= 0 {
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
	if err := a.db.QueryRow(ctx, "SELECT uname,secret,expires,authlvl FROM auth WHERE userid=$1 AND scheme=$2",
		store.DecodeUid(uid), scheme).Scan(
		&record.Uname, &record.Secret, &record.Expires, &record.Authlvl); err != nil {
		if err == pgx.ErrNoRows {
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
	if err := a.db.QueryRow(ctx, "SELECT userid,secret,expires,authlvl FROM auth WHERE uname=$1", unique).Scan(
		&record.Userid, &record.Secret, &record.Expires, &record.Authlvl); err != nil {
		if err == pgx.ErrNoRows {
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
	var id int64

	row, err := a.db.Query(ctx, "SELECT * FROM users WHERE id=$1 AND state!=$2", store.DecodeUid(uid), t.StateDeleted)
	if err != nil {
		return nil, err
	}
	defer row.Close()

	if !row.Next() {
		// Nothing found: user does not exist or marked as soft-deleted
		return nil, nil
	}

	err = row.Scan(&id, &user.CreatedAt, &user.UpdatedAt, &user.State, &user.StateAt, &user.Access, &user.LastSeen, &user.UserAgent, &user.Public, &user.Trusted, &user.Tags)
	if err == nil {
		user.SetUid(uid)
		return &user, nil
	}

	return nil, err
}

func (a *adapter) UserGetAll(ids ...t.Uid) ([]t.User, error) {
	uids := make([]interface{}, len(ids))
	for i, id := range ids {
		uids[i] = store.DecodeUid(id)
	}

	users := []t.User{}
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.Query(ctx, "SELECT * FROM users WHERE id = ANY ($1) AND state!=$2", uids, t.StateDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var user t.User
		var id int64
		if err = rows.Scan(&id, &user.CreatedAt, &user.UpdatedAt, &user.State, &user.StateAt, &user.Access, &user.LastSeen, &user.UserAgent, &user.Public, &user.Trusted, &user.Tags); err != nil {
			users = nil
			break
		}

		if user.State == t.StateDeleted {
			continue
		}

		user.SetUid(store.EncodeUid(id))
		users = append(users, user)
	}
	if err == nil {
		err = rows.Err()
	}

	return users, err
}

// UserDelete deletes specified user: wipes completely (hard-delete) or marks as deleted.
// TODO: report when the user is not found.
func (a *adapter) UserDelete(uid t.Uid, hard bool) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	now := t.TimeNow()
	decoded_uid := store.DecodeUid(uid)

	if hard {
		// Delete user's devices
		// t.ErrNotFound = user has no devices.
		if err = deviceDelete(ctx, tx, uid, ""); err != nil && err != t.ErrNotFound {
			return err
		}

		// Delete user's subscriptions in all topics.
		if err = subsDelForUser(ctx, tx, uid, true); err != nil {
			return err
		}

		// Delete records of messages soft-deleted for the user.
		if _, err = tx.Exec(ctx, "DELETE FROM dellog WHERE deletedfor=$1", decoded_uid); err != nil {
			return err
		}

		// Can't delete user's messages in all topics because we cannot notify topics of such deletion.
		// Just leave the messages there marked as sent by "not found" user.

		// Delete topics where the user is the owner.

		// First delete all messages in those topics.
		if _, err = tx.Exec(ctx, "DELETE FROM dellog USING topics WHERE topics.name=dellog.topic AND topics.owner=$1",
			decoded_uid); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM messages USING topics WHERE topics.name=messages.topic AND topics.owner=$1",
			decoded_uid); err != nil {
			return err
		}

		// Delete all subscriptions.
		if _, err = tx.Exec(ctx, "DELETE FROM subscriptions USING topics WHERE topics.name=subscriptions.topic AND topics.owner=$1",
			decoded_uid); err != nil {
			return err
		}

		// Delete topic tags.
		if _, err = tx.Exec(ctx, "DELETE FROM topictags USING topics WHERE topics.name=topictags.topic AND topics.owner=$1",
			decoded_uid); err != nil {
			return err
		}

		// And finally delete the topics.
		if _, err = tx.Exec(ctx, "DELETE FROM topics WHERE owner=$1", decoded_uid); err != nil {
			return err
		}

		// Delete user's authentication records.
		if _, err = tx.Exec(ctx, "DELETE FROM auth WHERE userid=$1", decoded_uid); err != nil {
			return err
		}

		// Delete all credentials.
		if err = credDel(ctx, tx, uid, "", ""); err != nil && err != t.ErrNotFound {
			return err
		}

		if _, err = tx.Exec(ctx, "DELETE FROM usertags WHERE userid=$1", decoded_uid); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx, "DELETE FROM users WHERE id=$1", decoded_uid); err != nil {
			return err
		}
	} else {
		// Disable all user's subscriptions. That includes p2p subscriptions. No need to delete them.
		if err = subsDelForUser(ctx, tx, uid, false); err != nil {
			return err
		}

		// Disable all subscriptions to topics where the user is the owner.
		if _, err = tx.Exec(ctx, "UPDATE subscriptions SET updatedat=$1, deletedat=$2 "+
			"FROM topics WHERE subscriptions.topic=topics.name AND topics.owner=$3",
			now, now, decoded_uid); err != nil {
			return err
		}
		// Disable group topics where the user is the owner.
		if _, err = tx.Exec(ctx, "UPDATE topics SET updatedat=$1, touchedat=$2, state=$3, stateat=$4 WHERE owner=$5",
			now, now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}
		// Disable p2p topics with the user (p2p topic's owner is 0).
		if _, err = tx.Exec(ctx, "UPDATE topics SET updatedat=$1, touchedat=$2, state=$3, stateat=$4 "+
			"FROM subscriptions WHERE topics.name=subscriptions.topic "+
			"AND topics.owner=0 AND subscriptions.userid=$5",
			now, now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}

		// Disable the other user's subscription to a disabled p2p topic.
		if _, err = tx.Exec(ctx, "UPDATE subscriptions AS s_one SET updatedat=$1, deletedat=$2 "+
			"FROM subscriptions AS s_two WHERE s_one.topic=s_two.topic "+
			"AND s_two.userid=$3 AND s_two.topic LIKE 'p2p%'",
			now, now, decoded_uid); err != nil {
			return err
		}

		// Disable user.
		if _, err = tx.Exec(ctx, "UPDATE users SET updatedat=$1, state=$2, stateat=$3 WHERE id=$4",
			now, t.StateDeleted, now, decoded_uid); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// topicStateForUser is called by UserUpdate when the update contains state change.
func (a *adapter) topicStateForUser(ctx context.Context, tx pgx.Tx, decoded_uid int64, now time.Time, update interface{}) error {
	var err error

	state, ok := update.(t.ObjState)
	if !ok {
		return t.ErrMalformed
	}

	if now.IsZero() {
		now = t.TimeNow()
	}

	// Change state of all topics where the user is the owner.
	if _, err = tx.Exec(ctx, "UPDATE topics SET state=$1, stateat=$2 WHERE owner=$3 AND state!=$4",
		state, now, decoded_uid, t.StateDeleted); err != nil {
		return err
	}

	// Change state of p2p topics with the user (p2p topic's owner is 0)
	if _, err = tx.Exec(ctx, "UPDATE topics SET state=$1, stateat=$2 "+
		"FROM subscriptions WHERE topics.name=subscriptions.topic AND "+
		"topics.owner=0 AND subscriptions.userid=$3 AND topics.state!=$4",
		state, now, decoded_uid, t.StateDeleted); err != nil {
		return err
	}

	// Subscriptions don't need to be updated:
	// subscriptions of a disabled user are not disabled and still can be manipulated.

	return nil
}

// UserUpdate updates user object.
func (a *adapter) UserUpdate(uid t.Uid, update map[string]interface{}) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	cols, args := updateByMap(update)
	decoded_uid := store.DecodeUid(uid)
	args = append(args, decoded_uid)
	sql, args := expandQuery("UPDATE users SET "+strings.Join(cols, ",")+" WHERE id=?", args...)
	_, err = tx.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}

	if state, ok := update["State"]; ok {
		now, _ := update["StateAt"].(time.Time)
		err = a.topicStateForUser(ctx, tx, decoded_uid, now, state)
		if err != nil {
			return err
		}
	}

	// Tags are also stored in a separate table
	if tags := extractTags(update); tags != nil {
		// First delete all user tags
		_, err = tx.Exec(ctx, "DELETE FROM usertags WHERE userid=$1", decoded_uid)
		if err != nil {
			return err
		}
		// Now insert new tags
		err = addTags(ctx, tx, "usertags", "userid", decoded_uid, tags, false)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// UserUpdateTags adds or resets user's tags
func (a *adapter) UserUpdateTags(uid t.Uid, add, remove, reset []string) ([]string, error) {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	decoded_uid := store.DecodeUid(uid)

	if reset != nil {
		// Delete all tags first if resetting.
		_, err = tx.Exec(ctx, "DELETE FROM usertags WHERE userid=$1", decoded_uid)
		if err != nil {
			return nil, err
		}
		add = reset
		remove = nil
	}

	// Now insert new tags. Ignore duplicates if resetting.
	err = addTags(ctx, tx, "usertags", "userid", decoded_uid, add, reset == nil)
	if err != nil {
		return nil, err
	}

	// Delete tags.
	err = removeTags(ctx, tx, "usertags", "userid", decoded_uid, remove)
	if err != nil {
		return nil, err
	}

	var allTags []string
	rows, err := tx.Query(ctx, "SELECT tag FROM usertags WHERE userid=$1", decoded_uid)
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		var tag string
		rows.Scan(&tag)
		allTags = append(allTags, tag)
	}
	rows.Close()

	_, err = tx.Exec(ctx, "UPDATE users SET tags=$1 WHERE id=$2", t.StringSlice(allTags), decoded_uid)
	if err != nil {
		return nil, err
	}

	return allTags, tx.Commit(ctx)
}

// UserGetByCred returns user ID for the given validated credential.
func (a *adapter) UserGetByCred(method, value string) (t.Uid, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var decoded_uid int64
	err := a.db.QueryRow(ctx, "SELECT userid FROM credentials WHERE synthetic=$1", method+":"+value).Scan(&decoded_uid)
	if err == nil {
		return store.EncodeUid(decoded_uid), nil
	}

	if err == pgx.ErrNoRows {
		// Clear the error if user does not exist
		return t.ZeroUid, nil
	}
	return t.ZeroUid, err
}

// UserUnreadCount returns the total number of unread messages in all topics with
// the R permission. If read fails, the counts are still returned with the original
// user IDs but with the unread count undefined and non-nil error.
func (a *adapter) UserUnreadCount(ids ...t.Uid) (map[t.Uid]int, error) {
	uids := make([]interface{}, len(ids))
	counts := make(map[t.Uid]int, len(ids))
	for i, id := range ids {
		uids[i] = store.DecodeUid(id)
		// Ensure all original uids are always present.
		counts[id] = 0
	}

	query, uids := expandQuery("SELECT s.userid, SUM(t.seqid)-SUM(s.readseqid) AS unreadcount FROM topics AS t, subscriptions AS s "+
		"WHERE s.userid IN (?) AND t.name=s.topic AND s.deletedat IS NULL AND t.state!=? AND "+
		"POSITION('R' IN s.modewant)>0 AND POSITION('R' IN s.modegiven)>0 GROUP BY s.userid", uids, t.StateDeleted)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.Query(ctx, query, uids...)
	if err != nil {
		return counts, err
	}
	defer rows.Close()

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

	rows, err := a.db.Query(ctx,
		"SELECT u.id, COALESCE(SUM(CASE WHEN c.done THEN 1 ELSE 0 END), 0) AS total "+
			"FROM users u LEFT JOIN credentials c ON u.id = c.userid "+
			"WHERE u.lastseen IS NULL AND u.updatedat < $1 GROUP BY u.id, u.updatedat "+
			"HAVING COALESCE(SUM(CASE WHEN c.done THEN 1 ELSE 0 END), 0) = 0 ORDER BY u.updatedat ASC LIMIT $2",
		lastUpdatedBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	return uids, err
}

// *****************************

func (a *adapter) topicCreate(ctx context.Context, tx pgx.Tx, topic *t.Topic) error {
	_, err := tx.Exec(ctx, "INSERT INTO topics(createdat,updatedat,touchedat,state,name,usebt,owner,access,public,trusted,tags) "+
		"VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
		topic.CreatedAt, topic.UpdatedAt, topic.TouchedAt, topic.State, topic.Id, topic.UseBt,
		store.DecodeUid(t.ParseUid(topic.Owner)), topic.Access, toJSON(topic.Public), toJSON(topic.Trusted), topic.Tags)
	if err != nil {
		return err
	}

	// Save topic's tags to a separate table to make topic findable.
	return addTags(ctx, tx, "topictags", "topic", topic.Id, topic.Tags, false)
}

// TopicCreate saves topic object to database.
func (a *adapter) TopicCreate(topic *t.Topic) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	err = a.topicCreate(ctx, tx, topic)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// If undelete = true - update subscription on duplicate key, otherwise ignore the duplicate.
func createSubscription(ctx context.Context, tx pgx.Tx, sub *t.Subscription, undelete bool) error {

	isOwner := (sub.ModeGiven & sub.ModeWant).IsOwner()

	jpriv := toJSON(sub.Private)
	decoded_uid := store.DecodeUid(t.ParseUid(sub.User))
	_, err2 := tx.Exec(ctx, "SAVEPOINT createSub")
	if err2 != nil {
		log.Println("Error: Failed to create savepoint: ", err2.Error())
	}
	_, err := tx.Exec(ctx,
		"INSERT INTO subscriptions(createdat,updatedat,deletedat,userid,topic,modeWant,modeGiven,private) "+
			"VALUES($1,$2,NULL,$3,$4,$5,$6,$7)",
		sub.CreatedAt, sub.UpdatedAt, decoded_uid, sub.Topic, sub.ModeWant.String(), sub.ModeGiven.String(), jpriv)

	if err != nil && isDupe(err) {
		_, err2 = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT createSub")
		if err2 != nil {
			log.Println("Error: Failed to rollback savepoint: ", err2.Error())
		}
		if undelete {
			_, err = tx.Exec(ctx, "UPDATE subscriptions SET createdat=$1,updatedat=$2,deletedat=NULL,modeWant=$3,modeGiven=$4,"+
				"delid=0,recvseqid=0,readseqid=0 WHERE topic=$5 AND userid=$6",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeWant.String(), sub.ModeGiven.String(), sub.Topic, decoded_uid)
		} else {
			_, err = tx.Exec(ctx, "UPDATE subscriptions SET createdat=$1,updatedat=$2,deletedat=NULL,modeWant=$3,modeGiven=$4,"+
				"delid=0,recvseqid=0,readseqid=0,private=$5 WHERE topic=$6 AND userid=$7",
				sub.CreatedAt, sub.UpdatedAt, sub.ModeWant.String(), sub.ModeGiven.String(), jpriv,
				sub.Topic, decoded_uid)
		}
	} else {
		_, err2 = tx.Exec(ctx, "RELEASE SAVEPOINT createSub")
		if err2 != nil {
			log.Println("Error: Failed to release savepoint: ", err2.Error())
		}
	}
	if err == nil && isOwner {
		_, err = tx.Exec(ctx, "UPDATE topics SET owner=$1 WHERE name=$2", decoded_uid, sub.Topic)
	}
	return err
}

// TopicCreateP2P given two users creates a p2p topic
func (a *adapter) TopicCreateP2P(initiator, invited *t.Subscription) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	err = createSubscription(ctx, tx, initiator, false)
	if err != nil {
		return err
	}

	err = createSubscription(ctx, tx, invited, true)
	if err != nil {
		return err
	}

	topic := &t.Topic{ObjHeader: t.ObjHeader{Id: initiator.Topic}}
	topic.ObjHeader.MergeTimes(&initiator.ObjHeader)
	topic.TouchedAt = initiator.GetTouchedAt()
	err = a.topicCreate(ctx, tx, topic)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// TopicGet loads a single topic by name, if it exists. If the topic does not exist the call returns (nil, nil)
func (a *adapter) TopicGet(topic string) (*t.Topic, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	// Fetch topic by name
	var tt = new(t.Topic)
	var owner int64
	err := a.db.QueryRow(ctx,
		"SELECT createdat,updatedat,state,stateat,touchedat,name AS id,usebt,access,owner,seqid,delid,public,trusted,tags "+
			"FROM topics WHERE name=$1",
		topic).Scan(&tt.CreatedAt, &tt.UpdatedAt, &tt.State, &tt.StateAt, &tt.TouchedAt, &tt.Id,
		&tt.UseBt, &tt.Access, &owner, &tt.SeqId, &tt.DelId, &tt.Public, &tt.Trusted, &tt.Tags)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	tt.Owner = store.EncodeUid(owner).String()

	return tt, nil
}

// TopicsForUser loads user's contact list: p2p and grp topics, except for 'me' & 'fnd' subscriptions.
// Reads and denormalizes Public value.
func (a *adapter) TopicsForUser(uid t.Uid, keepDeleted bool, opts *t.QueryOpt) ([]t.Subscription, error) {
	// Fetch ALL user's subscriptions, even those which has not been modified recently.
	// We are going to use these subscriptions to fetch topics and users which may have been modified recently.
	q := `SELECT createdat,updatedat,deletedat,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE userid=?`
	args := []interface{}{store.DecodeUid(uid)}
	if !keepDeleted {
		// Filter out deleted rows.
		q += " AND deletedat IS NULL"
	}
	limit := 0
	ipg := time.Time{}
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
			ipg = *opts.IfModifiedSince
		}
	} else {
		limit = a.maxResults
	}

	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	q, args = expandQuery(q, args...)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, q, args...)

	if err != nil {
		rows.Close()
		return nil, err
	}

	// Fetch subscriptions. Two queries are needed: users table (p2p) and topics table (grp).
	// Prepare a list of separate subscriptions to users vs topics
	join := make(map[string]t.Subscription) // Keeping these to make a join with table for .private and .access
	topq := make([]interface{}, 0, 16)
	usrq := make([]interface{}, 0, 16)
	for rows.Next() {
		var sub t.Subscription
		var modeWant, modeGiven []byte
		if err = rows.Scan(&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt, &sub.Topic, &sub.DelId,
			&sub.RecvSeqId, &sub.ReadSeqId, &modeWant, &modeGiven, &sub.Private); err != nil {
			break
		}
		sub.ModeWant.Scan(modeWant)
		sub.ModeGiven.Scan(modeGiven)
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
		sub.Private = fromJSON(sub.Private)
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
		q = "SELECT createdat,updatedat,state,stateat,touchedat,name AS id,usebt,access,seqid,delid,public,trusted,tags " +
			"FROM topics WHERE name IN (?)"
		newargs := []interface{}{topq}

		if !keepDeleted {
			// Optionally skip deleted topics.
			q += " AND state!=?"
			newargs = append(newargs, t.StateDeleted)
		}

		if !ipg.IsZero() {
			// Use cache timestamp if provided: get newer entries only.
			q += " AND touchedat>?"
			newargs = append(newargs, ipg)

			if limit > 0 && limit < len(topq) {
				// No point in fetching more than the requested limit.
				q += " ORDER BY touchedat LIMIT ?"
				newargs = append(newargs, limit)
			}
		}
		q, newargs = expandQuery(q, newargs...)

		ctx2, cancel2 := a.getContext()
		if cancel2 != nil {
			defer cancel2()
		}
		rows, err = a.db.Query(ctx2, q, newargs...)
		if err != nil {
			rows.Close()
			return nil, err
		}

		var top t.Topic
		for rows.Next() {
			if err = rows.Scan(&top.CreatedAt, &top.UpdatedAt, &top.State, &top.StateAt, &top.TouchedAt, &top.Id, &top.UseBt,
				&top.Access, &top.SeqId, &top.DelId, &top.Public, &top.Trusted, &top.Tags); err != nil {
				break
			}

			sub := join[top.Id]
			// Check if sub.UpdatedAt needs to be adjusted to earlier or later time.
			sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, top.UpdatedAt)
			sub.SetState(top.State)
			sub.SetTouchedAt(top.TouchedAt)
			sub.SetSeqId(top.SeqId)
			if t.GetTopicCat(sub.Topic) == t.TopicCatGrp {
				sub.SetPublic(top.Public)
				sub.SetTrusted(top.Trusted)
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
		q = "SELECT id,createdat,updatedat,state,stateat,access,lastseen,useragent,public,trusted,tags " +
			"FROM users WHERE id IN (?)"
		newargs := []interface{}{usrq}

		if !keepDeleted {
			// Optionally skip deleted users.
			q += " AND state!=?"
			newargs = append(newargs, t.StateDeleted)
		}

		// Ignoring ipg: we need all users to get LastSeen and UserAgent.

		q, newargs = expandQuery(q, newargs...)

		ctx3, cancel3 := a.getContext()
		if cancel3 != nil {
			defer cancel3()
		}

		rows, err = a.db.Query(ctx3, q, newargs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			var usr2 t.User
			var id int64
			if err = rows.Scan(&id, &usr2.CreatedAt, &usr2.UpdatedAt, &usr2.State, &usr2.StateAt, &usr2.Access,
				&usr2.LastSeen, &usr2.UserAgent, &usr2.Public, &usr2.Trusted, &usr2.Tags); err != nil {
				break
			}

			usr2.Id = store.EncodeUid(id).String()
			joinOn := uid.P2PName(t.ParseUid(usr2.Id))
			if sub, ok := join[joinOn]; ok {
				sub.UpdatedAt = common.SelectLatestTime(sub.UpdatedAt, usr2.UpdatedAt)
				sub.SetState(usr2.State)
				sub.SetPublic(usr2.Public)
				sub.SetTrusted(usr2.Trusted)
				sub.SetDefaultAccess(usr2.Access.Auth, usr2.Access.Anon)
				sub.SetLastSeenAndUA(usr2.LastSeen, usr2.UserAgent)
				join[joinOn] = sub
			}
		}
		if err == nil {
			err = rows.Err()
		}

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
	q, args = expandQuery(q, args...)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Fetch subscriptions
	var sub t.Subscription
	var subs []t.Subscription
	var userId int64
	var modeWant, modeGiven []byte
	var lastSeen *time.Time = nil
	var userAgent string
	var public, trusted interface{}
	for rows.Next() {
		if err = rows.Scan(
			&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt,
			&userId, &sub.Topic, &sub.DelId, &sub.RecvSeqId,
			&sub.ReadSeqId, &modeWant, &modeGiven,
			&public, &trusted, &lastSeen, &userAgent, &sub.Private); err != nil {
			break
		}

		sub.User = store.EncodeUid(userId).String()
		sub.SetPublic(public)
		sub.SetTrusted(trusted)
		sub.SetLastSeenAndUA(lastSeen, userAgent)
		sub.ModeWant.Scan(modeWant)
		sub.ModeGiven.Scan(modeGiven)
		subs = append(subs, sub)
	}
	if err == nil {
		err = rows.Err()
	}

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
	rows, err := a.db.Query(ctx, sqlQuery, store.DecodeUid(uid))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	return names, err
}

// OwnTopics loads a slice of topic names where the user is the owner.
func (a *adapter) OwnTopics(uid t.Uid) ([]string, error) {
	return a.topicNamesForUser(uid, "SELECT name FROM topics WHERE owner=$1")
}

// ChannelsForUser loads a slice of topic names where the user is a channel reader and notifications (P) are enabled.
func (a *adapter) ChannelsForUser(uid t.Uid) ([]string, error) {
	return a.topicNamesForUser(uid,
		"SELECT topic FROM subscriptions WHERE userid=$1 AND topic LIKE 'chn%' "+
			"AND POSITION('P' IN modewant)>0 AND POSITION('P' IN modegiven)>0 AND deletedat IS NULL")
}

func (a *adapter) TopicShare(shares []*t.Subscription) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	for _, sub := range shares {
		err = createSubscription(ctx, tx, sub, true)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// TopicDelete deletes specified topic.
func (a *adapter) TopicDelete(topic string, isChan, hard bool) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// If the topic is a channel, must try to delete subscriptions under both grpXXX and chnXXX names.
	args := []interface{}{topic}
	if isChan {
		args = append(args, t.GrpToChn(topic))
	}

	if hard {
		// Delete subscriptions. If this is a channel, delete both group subscriptions and channel subscriptions.
		q, args := expandQuery("DELETE FROM subscriptions WHERE topic IN (?)", args)

		if _, err = tx.Exec(ctx, q, args...); err != nil {
			return err
		}

		if err = messageDeleteList(ctx, tx, topic, nil); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx, "DELETE FROM topictags WHERE topic=$1", topic); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx, "DELETE FROM topics WHERE name=$1", topic); err != nil {
			return err
		}
	} else {
		now := t.TimeNow()
		q, args := expandQuery("UPDATE subscriptions SET updatedat=?,deletedat=? WHERE topic IN (?)", now, now, args)

		if _, err = tx.Exec(ctx, q, args); err != nil {
			return err
		}

		if _, err = tx.Exec(ctx, "UPDATE topics SET updatedat=$1,touchedat=$2,state=$3,stateat=$4 WHERE name=$5",
			now, now, t.StateDeleted, now, topic); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *adapter) TopicUpdateOnMessage(topic string, msg *t.Message) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.Exec(ctx, "UPDATE topics SET seqid=$1,touchedat=$2 WHERE name=$3", msg.SeqId, msg.CreatedAt, topic)

	return err
}

func (a *adapter) TopicUpdate(topic string, update map[string]interface{}) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	if t, u := update["TouchedAt"], update["UpdatedAt"]; t == nil && u != nil {
		update["TouchedAt"] = u
	}
	cols, args := updateByMap(update)
	q, args := expandQuery("UPDATE topics SET "+strings.Join(cols, ",")+" WHERE name=?", args, topic)
	_, err = tx.Exec(ctx, q, args...)
	if err != nil {
		return err
	}

	// Tags are also stored in a separate table
	if tags := extractTags(update); tags != nil {
		// First delete all user tags
		_, err = tx.Exec(ctx, "DELETE FROM topictags WHERE topic=$1", topic)
		if err != nil {
			return err
		}
		// Now insert new tags
		err = addTags(ctx, tx, "topictags", "topic", topic, tags, false)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (a *adapter) TopicOwnerChange(topic string, newOwner t.Uid) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	_, err := a.db.Exec(ctx, "UPDATE topics SET owner=$1 WHERE name=$2", store.DecodeUid(newOwner), topic)
	return err
}

// Get a subscription of a user to a topic.
func (a *adapter) SubscriptionGet(topic string, user t.Uid, keepDeleted bool) (*t.Subscription, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var sub t.Subscription
	var userId int64
	var modeWant, modeGiven []byte
	err := a.db.QueryRow(ctx, `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven,private FROM subscriptions WHERE topic=$1 AND userid=$2`,
		topic, store.DecodeUid(user)).Scan(&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt, &userId,
		&sub.Topic, &sub.DelId, &sub.RecvSeqId, &sub.ReadSeqId, &modeWant, &modeGiven, &sub.Private)

	if err != nil {
		if err == pgx.ErrNoRows {
			// Nothing found - clear the error
			err = nil
		}
		return nil, err
	}

	if !keepDeleted && sub.DeletedAt != nil {
		return nil, nil
	}

	sub.User = store.EncodeUid(userId).String()
	sub.ModeWant.Scan(modeWant)
	sub.ModeGiven.Scan(modeGiven)

	return &sub, nil
}

// SubsForUser loads all user's subscriptions. Does NOT load Public or Private values and does
// not load deleted subscriptions.
func (a *adapter) SubsForUser(forUser t.Uid) ([]t.Subscription, error) {
	q := `SELECT createdat,updatedat,deletedat,userid AS user,topic,delid,recvseqid,
		readseqid,modewant,modegiven FROM subscriptions WHERE userid=$1 AND deletedat IS NULL`
	args := []interface{}{store.DecodeUid(forUser)}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []t.Subscription
	var sub t.Subscription
	var userId int64
	var modeWant, modeGiven []byte
	for rows.Next() {
		if err = rows.Scan(&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt, &userId, &sub.Topic, &sub.DelId,
			&sub.RecvSeqId, &sub.ReadSeqId, &modeWant, &modeGiven); err != nil {
			break
		}

		sub.User = store.EncodeUid(userId).String()
		sub.ModeWant.Scan(modeWant)
		sub.ModeGiven.Scan(modeGiven)
		subs = append(subs, sub)
	}
	if err == nil {
		err = rows.Err()
	}

	return subs, err
}

// SubsForTopic fetches all subsciptions for a topic. Does NOT load Public value.
// The difference between UsersForTopic vs SubsForTopic is that the former loads user.public+trusted,
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
	q, args = expandQuery(q, args...)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []t.Subscription
	var sub t.Subscription
	var userId int64
	var modeWant, modeGiven []byte
	for rows.Next() {
		if err = rows.Scan(&sub.CreatedAt, &sub.UpdatedAt, &sub.DeletedAt, &userId, &sub.Topic, &sub.DelId,
			&sub.RecvSeqId, &sub.ReadSeqId, &modeWant, &modeGiven, &sub.Private); err != nil {
			break
		}

		sub.User = store.EncodeUid(userId).String()
		sub.ModeWant.Scan(modeWant)
		sub.ModeGiven.Scan(modeGiven)
		subs = append(subs, sub)
	}
	if err == nil {
		err = rows.Err()
	}

	return subs, err
}

// SubsUpdate updates one or multiple subscriptions to a topic.
func (a *adapter) SubsUpdate(topic string, user t.Uid, update map[string]interface{}) error {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	cols, args := updateByMap(update)
	args = append(args, topic)
	q := "UPDATE subscriptions SET " + strings.Join(cols, ",") + " WHERE topic=?"
	if !user.IsZero() {
		// Update just one topic subscription
		args = append(args, store.DecodeUid(user))
		q += " AND userid=?"
	}
	q, args = expandQuery(q, args...)

	if _, err = tx.Exec(ctx, q, args...); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// SubsDelete marks subscription as deleted.
func (a *adapter) SubsDelete(topic string, user t.Uid) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	decoded_id := store.DecodeUid(user)
	now := t.TimeNow()
	res, err := tx.Exec(ctx,
		"UPDATE subscriptions SET updatedat=$1,deletedat=$2 WHERE topic=$3 AND userid=$4 AND deletedat IS NULL",
		now, now, topic, decoded_id)
	if err != nil {
		return err
	}

	affected := res.RowsAffected()
	if affected == 0 {
		// ensure tx.Rollback() above is ran
		err = t.ErrNotFound
		return err
	}

	// Remove records of messages soft-deleted by this user.
	_, err = tx.Exec(ctx, "DELETE FROM dellog WHERE topic=$1 AND deletedfor=$2", topic, decoded_id)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// subsDelForUser marks user's subscriptions as deleted.
func subsDelForUser(ctx context.Context, tx pgx.Tx, user t.Uid, hard bool) error {
	var err error
	if hard {
		_, err = tx.Exec(ctx, "DELETE FROM subscriptions WHERE userid=$1;", store.DecodeUid(user))
	} else {
		now := t.TimeNow()
		_, err = tx.Exec(ctx, "UPDATE subscriptions SET updatedat=$1,deletedat=$2 WHERE userid=$3 AND deletedat IS NULL;",
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

	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	if err = subsDelForUser(ctx, tx, user, hard); err != nil {
		return err
	}

	return tx.Commit(ctx)

}

// Returns a list of users who match given tags, such as "email:jdoe@example.com" or "tel:+18003287448".
// Searching the 'users.Tags' for the given tags using respective index.
func (a *adapter) FindUsers(user t.Uid, req [][]string, opt []string, activeOnly bool) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var args []interface{}
	stateConstraint := ""
	if activeOnly {
		args = append(args, t.StateOK)
		stateConstraint = "u.state=? AND "
	}
	allReq := t.FlattenDoubleSlice(req)
	allTags := append(allReq, opt...)
	for _, tag := range allTags {
		index[tag] = struct{}{}
	}
	args = append(args, allTags)

	query := "SELECT u.id,u.createdat,u.updatedat,u.access,u.public,u.trusted,u.tags,COUNT(*) AS matches " +
		"FROM users AS u LEFT JOIN usertags AS t ON t.userid=u.id " +
		"WHERE " + stateConstraint + "t.tag IN (?) GROUP BY u.id,u.createdat,u.updatedat"
	if len(allReq) > 0 {
		query += " HAVING"
		first := true
		for _, reqDisjunction := range req {
			if len(reqDisjunction) > 0 {
				if !first {
					query += " AND"
				} else {
					first = false
				}
				// At least one of the tags must be present.
				query += " COUNT(t.tag IN (?) OR NULL)>=1"
				args = append(args, reqDisjunction)
			}
		}
	}
	query, args = expandQuery(query+" ORDER BY matches DESC LIMIT ?", args, a.maxResults)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	// Get users matched by tags, sort by number of matches from high to low.
	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userId int64
	var public, trusted interface{}
	var access t.DefaultAccess
	var userTags t.StringSlice
	var ignored int
	var sub t.Subscription
	var subs []t.Subscription
	thisUser := store.DecodeUid(user)
	for rows.Next() {
		if err = rows.Scan(&userId, &sub.CreatedAt, &sub.UpdatedAt, &access,
			&public, &trusted, &userTags, &ignored); err != nil {
			subs = nil
			break
		}

		if userId == thisUser {
			// Skip the callee
			continue
		}
		sub.User = store.EncodeUid(userId).String()
		sub.SetPublic(public)
		sub.SetTrusted(trusted)
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
	if err == nil {
		err = rows.Err()
	}

	return subs, err

}

// Returns a list of topics with matching tags.
// Searching the 'topics.Tags' for the given tags using respective index.
func (a *adapter) FindTopics(req [][]string, opt []string, activeOnly bool) ([]t.Subscription, error) {
	index := make(map[string]struct{})
	var args []interface{}
	stateConstraint := ""
	if activeOnly {
		args = append(args, t.StateOK)
		stateConstraint = "t.state=? AND "
	}
	allReq := t.FlattenDoubleSlice(req)
	allTags := append(allReq, opt...)
	for _, tag := range allTags {
		index[tag] = struct{}{}
	}
	args = append(args, allTags)

	query := "SELECT t.id,t.name AS topic,t.createdat,t.updatedat,t.usebt,t.access,t.public,t.trusted,t.tags,COUNT(*) AS matches " +
		"FROM topics AS t LEFT JOIN topictags AS tt ON t.name=tt.topic " +
		"WHERE " + stateConstraint + "tt.tag IN (?) GROUP BY t.id,t.name,t.createdat,t.updatedat,t.usebt"
	if len(allReq) > 0 {
		query += " HAVING"
		first := true
		for _, reqDisjunction := range req {
			if len(reqDisjunction) > 0 {
				if !first {
					query += " AND"
				} else {
					first = false
				}
				// At least one of the tags must be present.
				query += " COUNT(tt.tag IN (?) OR NULL)>=1"
				args = append(args, reqDisjunction)
			}
		}
	}
	query, args = expandQuery(query+" ORDER BY matches DESC LIMIT ?", args, a.maxResults)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var access t.DefaultAccess
	var public, trusted interface{}
	var topicTags t.StringSlice
	var id int
	var ignored int
	var isChan int
	var sub t.Subscription
	var subs []t.Subscription
	for rows.Next() {
		if err = rows.Scan(&id, &sub.Topic, &sub.CreatedAt, &sub.UpdatedAt, &isChan, &access,
			&public, &trusted, &topicTags, &ignored); err != nil {
			subs = nil
			break
		}

		if isChan != 0 {
			sub.Topic = t.GrpToChn(sub.Topic)
		}
		sub.SetPublic(public)
		sub.SetTrusted(trusted)
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
	if err == nil {
		err = rows.Err()
	}

	if err != nil {
		return nil, err
	}
	return subs, nil

}

// Messages
func (a *adapter) MessageSave(msg *t.Message) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	// store assignes message ID, but we don't use it. Message IDs are not used anywhere.
	// Using a sequential ID provided by the database.
	var id int
	err := a.db.QueryRow(ctx,
		`INSERT INTO messages(createdAt,updatedAt,seqid,topic,"from",head,content) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		msg.CreatedAt, msg.UpdatedAt, msg.SeqId, msg.Topic,
		store.DecodeUid(t.ParseUid(msg.From)), msg.Head, toJSON(msg.Content)).Scan(&id)
	if err == nil {
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

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	rows, err := a.db.Query(
		ctx,
		`SELECT m.createdat,m.updatedat,m.deletedat,m.delid,m.seqid,m.topic,m."from",m.head,m.content`+
			" FROM messages AS m LEFT JOIN dellog AS d"+
			" ON d.topic=m.topic AND m.seqid BETWEEN d.low AND d.hi-1 AND d.deletedfor=$1"+
			" WHERE m.delid=0 AND m.topic=$2 AND m.seqid BETWEEN $3 AND $4 AND d.deletedfor IS NULL"+
			" ORDER BY m.seqid DESC LIMIT $5",
		unum, topic, lower, upper, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs := make([]t.Message, 0, limit)
	for rows.Next() {
		var msg t.Message
		var from int64
		if err = rows.Scan(&msg.CreatedAt, &msg.UpdatedAt, &msg.DeletedAt, &msg.DelId, &msg.SeqId,
			&msg.Topic, &from, &msg.Head, &msg.Content); err != nil {
			break
		}
		msg.From = store.EncodeUid(from).String()
		msgs = append(msgs, msg)
	}
	if err == nil {
		err = rows.Err()
	}

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
	rows, err := a.db.Query(ctx, "SELECT topic,deletedfor,delid,low,hi FROM dellog WHERE topic=$1 AND delid BETWEEN $2 AND $3"+
		" AND (deletedFor=0 OR deletedFor=$4) ORDER BY delid LIMIT $5",
		topic, lower, upper, store.DecodeUid(forUser), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
		if err = rows.Scan(&dellog.Topic, &dellog.Deletedfor, &dellog.Delid, &dellog.Low, &dellog.Hi); err != nil {
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

	if err == nil {
		if dmsg.DelId > 0 {
			dmsgs = append(dmsgs, dmsg)
		}
	}

	return dmsgs, err
}

func messageDeleteList(ctx context.Context, tx pgx.Tx, topic string, toDel *t.DelMessage) error {
	var err error
	if toDel == nil {
		// Whole topic is being deleted, thus also deleting all messages.
		_, err = tx.Exec(ctx, "DELETE FROM dellog WHERE topic=$1", topic)
		if err == nil {
			_, err = tx.Exec(ctx, "DELETE FROM messages WHERE topic=$1", topic)
		}
		// filemsglinks will be deleted because of ON DELETE CASCADE

	} else {
		// Only some messages are being deleted
		// Start with making log entries
		forUser := decodeUidString(toDel.DeletedFor)

		// Counter of deleted messages
		for _, rng := range toDel.SeqIdRanges {
			if rng.Hi == 0 {
				// Dellog must contain valid Low and *Hi*.
				rng.Hi = rng.Low + 1
			}
			if _, err = tx.Exec(ctx,
				"INSERT INTO dellog(topic,deletedfor,delid,low,hi) VALUES($1,$2,$3,$4,$5)",
				topic, forUser, toDel.DelId, rng.Low, rng.Hi); err != nil {
				break
			}
		}

		if err == nil && toDel.DeletedFor == "" {
			// Hard-deleting messages requires updates to the messages table
			where := "m.topic=? AND "
			args := []interface{}{topic}
			if len(toDel.SeqIdRanges) > 1 || toDel.SeqIdRanges[0].Hi == 0 {
				seqRange := []int{}
				for _, r := range toDel.SeqIdRanges {
					if r.Hi == 0 {
						seqRange = append(seqRange, r.Low)
					} else {
						for i := r.Low; i < r.Hi; i++ {
							seqRange = append(seqRange, i)
						}
					}
				}
				args = append(args, seqRange)
				where += "m.seqid IN (?)"

			} else {
				// Optimizing for a special case of single range low..hi.
				where += "m.seqid BETWEEN ? AND ?"
				// MySQL's BETWEEN is inclusive-inclusive thus decrement Hi by 1.
				args = append(args, toDel.SeqIdRanges[0].Low, toDel.SeqIdRanges[0].Hi-1)
			}
			where += " AND m.deletedAt IS NULL"
			query, newargs := expandQuery("DELETE FROM filemsglinks AS fml USING messages AS m WHERE m.id=fml.msgid AND "+
				where, args...)

			_, err = tx.Exec(ctx, query, newargs...)
			if err != nil {
				return err
			}

			query, newargs = expandQuery("UPDATE messages AS m SET deletedat=?,delid=?,head=NULL,content=NULL WHERE "+
				where, t.TimeNow(), toDel.DelId, args)

			_, err = tx.Exec(ctx, query, newargs...)
		}
	}

	return err
}

// MessageDeleteList deletes messages in the given topic with seqIds from the list
func (a *adapter) MessageDeleteList(topic string, toDel *t.DelMessage) (err error) {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	if err = messageDeleteList(ctx, tx, topic, toDel); err != nil {
		return err
	}

	return tx.Commit(ctx)
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
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Ensure uniqueness of the device ID: delete all records of the device ID
	_, err = tx.Exec(ctx, "DELETE FROM devices WHERE hash=$1", hash)
	if err != nil {
		return err
	}

	// Actually add/update DeviceId for the new user
	_, err = tx.Exec(ctx, "INSERT INTO devices(userid, hash, deviceId, platform, lastseen, lang) VALUES($1,$2,$3,$4,$5,$6)",
		store.DecodeUid(uid), hash, def.DeviceId, def.Platform, def.LastSeen, def.Lang)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (a *adapter) DeviceGetAll(uids ...t.Uid) (map[t.Uid][]t.DeviceDef, int, error) {
	var unupg []interface{}
	for _, uid := range uids {
		unupg = append(unupg, store.DecodeUid(uid))
	}

	query, unupg := expandQuery("SELECT userid,deviceid,platform,lastseen,lang FROM devices WHERE userid IN (?)", unupg)

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	rows, err := a.db.Query(ctx, query, unupg...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

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
		if err = rows.Scan(&device.Userid, &device.Deviceid, &device.Platform, &device.Lastseen, &device.Lang); err != nil {
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

	return result, count, err
}

func deviceDelete(ctx context.Context, tx pgx.Tx, uid t.Uid, deviceID string) error {
	var err error
	var res pgconn.CommandTag
	if deviceID == "" {
		res, err = tx.Exec(ctx, "DELETE FROM devices WHERE userid=$1", store.DecodeUid(uid))
	} else {
		res, err = tx.Exec(ctx, "DELETE FROM devices WHERE userid=$1 AND hash=$2", store.DecodeUid(uid), deviceHasher(deviceID))
	}

	if err == nil {
		if count := res.RowsAffected(); count == 0 {
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
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	err = deviceDelete(ctx, tx, uid, deviceID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
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
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
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
		err = tx.QueryRow(ctx, "SELECT done FROM credentials WHERE synthetic=$1", synth).Scan(&done)
		if err == nil {
			// Assign err to ensure closing of a transaction.
			err = t.ErrDuplicate
			return false, err
		}
		if err != pgx.ErrNoRows {
			return false, err
		}
		// We are going to insert new record.
		synth = cred.User + ":" + synth

		// Adding new unvalidated credential. Deactivate all unvalidated records of this user and method.
		_, err = tx.Exec(ctx, "UPDATE credentials SET deletedat=$1 WHERE userid=$2 AND method=$3 AND done=FALSE",
			now, userId, cred.Method)
		if err != nil {
			return false, err
		}
		// Assume that the record exists and try to update it: undelete, update timestamp and response value.
		res, err := tx.Exec(ctx, "UPDATE credentials SET updatedat=$1,deletedat=NULL,resp=$2,done=FALSE WHERE synthetic=$3",
			cred.UpdatedAt, cred.Resp, synth)
		if err != nil {
			return false, err
		}
		// If record was updated, then all is fine.
		if numrows := res.RowsAffected(); numrows > 0 {
			return false, tx.Commit(ctx)
		}
	} else {
		// Hard-deleting unconformed record if it exists.
		_, err = tx.Exec(ctx, "DELETE FROM credentials WHERE synthetic=$1", cred.User+":"+synth)
		if err != nil {
			return false, err
		}
	}

	_, err = tx.Exec(ctx, "INSERT INTO credentials(createdat,updatedat,method,value,synthetic,userid,resp,done) "+
		"VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
		cred.CreatedAt, cred.UpdatedAt, cred.Method, cred.Value, synth, userId, cred.Resp, cred.Done)
	if err != nil {
		if isDupe(err) {
			return true, t.ErrDuplicate
		}
		return true, err
	}
	return true, tx.Commit(ctx)
}

// credDel deletes given validation method or all methods of the given user.
// 1. If user is being deleted, hard-delete all records (method == "")
// 2. If one value is being deleted:
// 2.1 Delete it if it's valiated or if there were no attempts at validation
// (otherwise it could be used to circumvent the limit on validation attempts).
// 2.2 In that case mark it as soft-deleted.
func credDel(ctx context.Context, tx pgx.Tx, uid t.Uid, method, value string) error {
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
	where, _ := expandQuery(constraints, args...)

	var err error
	var res pgconn.CommandTag
	if method == "" {
		// Case 1
		res, err = tx.Exec(ctx, "DELETE FROM credentials"+where, args...)
		if err == nil {
			if count := res.RowsAffected(); count == 0 {
				err = t.ErrNotFound
			}
		}
		return err
	}

	// Case 2.1
	res, err = tx.Exec(ctx, "DELETE FROM credentials"+where+" AND (done=true OR retries=0)", args...)
	if err != nil {
		return err
	}
	if count := res.RowsAffected(); count > 0 {
		return nil
	}

	// Case 2.2
	query, args := expandQuery("UPDATE credentials SET deletedat=?"+constraints, t.TimeNow(), args)
	res, err = tx.Exec(ctx, query, args...)
	if err == nil {
		if count := res.RowsAffected(); count >= 0 {
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
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	err = credDel(ctx, tx, uid, method, value)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// CredConfirm marks given credential method as confirmed.
func (a *adapter) CredConfirm(uid t.Uid, method string) error {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	res, err := a.db.Exec(
		ctx,
		"UPDATE credentials SET updatedat=$1,done=true,synthetic=CONCAT(method,':',value) "+
			"WHERE userid=$2 AND method=$3 AND deletedat IS NULL AND done=FALSE",
		t.TimeNow(), store.DecodeUid(uid), method)
	if err != nil {
		if isDupe(err) {
			return t.ErrDuplicate
		}
		return err
	}
	if numrows := res.RowsAffected(); numrows < 1 {
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
	_, err := a.db.Exec(ctx, "UPDATE credentials SET updatedat=$1,retries=retries+1 WHERE userid=$2 AND method=$3 AND done=FALSE",
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

	err := a.db.QueryRow(ctx, "SELECT createdat,updatedat,method,value,resp,done,retries "+
		"FROM credentials WHERE userid=$1 AND deletedat IS NULL AND method=$2 AND done=FALSE",
		store.DecodeUid(uid), method).Scan(&cred.CreatedAt, &cred.UpdatedAt, &cred.Method, &cred.Value, &cred.Resp, &cred.Done, &cred.Retries)

	if err != nil {
		if err == pgx.ErrNoRows {
			err = nil
		}
		return nil, err
	}

	cred.User = uid.String()

	return &cred, nil
}

// CredGetAll returns credential records for the given user and method, all or validated only.
func (a *adapter) CredGetAll(uid t.Uid, method string, validatedOnly bool) ([]t.Credential, error) {
	query := "SELECT createdat,updatedat,method,value,resp,done,retries FROM credentials WHERE userid=$1 AND deletedat IS NULL"
	args := []interface{}{store.DecodeUid(uid)}
	if method != "" {
		query += " AND method=$2"
		args = append(args, method)
	}
	if validatedOnly {
		query += " AND done=TRUE"
	}

	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	var credentials []t.Credential
	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var cred t.Credential
		if err = rows.Scan(&cred.CreatedAt, &cred.UpdatedAt, &cred.Method, &cred.Value, &cred.Resp, &cred.Done, &cred.Retries); err != nil {
			credentials = nil
			break
		}

		credentials = append(credentials, cred)
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
	var user interface{}
	if fd.User != "" {
		user = store.DecodeUid(t.ParseUid(fd.User))
	}
	_, err := a.db.Exec(ctx,
		"INSERT INTO fileuploads(id,createdat,updatedat,userid,status,mimetype,size,location) "+
			"VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
		store.DecodeUid(fd.Uid()), fd.CreatedAt, fd.UpdatedAt, user,
		fd.Status, fd.MimeType, fd.Size, fd.Location)
	return err
}

// FileFinishUpload marks file upload as completed, successfully or otherwise
func (a *adapter) FileFinishUpload(fd *t.FileDef, success bool, size int64) (*t.FileDef, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	now := t.TimeNow()
	if success {
		_, err = tx.Exec(ctx, "UPDATE fileuploads SET updatedat=$1,status=$2,size=$3 WHERE id=$4",
			now, t.UploadCompleted, size, store.DecodeUid(fd.Uid()))
		if err != nil {
			return nil, err
		}

		fd.Status = t.UploadCompleted
		fd.Size = size
	} else {
		// Deleting the record: there is no value in keeping it in the DB.
		_, err = tx.Exec(ctx, "DELETE FROM fileuploads WHERE id=$1", store.DecodeUid(fd.Uid()))
		if err != nil {
			return nil, err
		}

		fd.Status = t.UploadFailed
		fd.Size = 0
	}
	fd.UpdatedAt = now

	return fd, tx.Commit(ctx)
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
	var ID int64
	var userId int64
	err := a.db.QueryRow(ctx, "SELECT id,createdat,updatedat,userid AS user,status,mimetype,size,location "+
		"FROM fileuploads WHERE id=$1", store.DecodeUid(id)).Scan(&ID, &fd.CreatedAt, &fd.UpdatedAt, &userId, &fd.Status, &fd.MimeType, &fd.Size, &fd.Location)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	fd.SetUid(store.EncodeUid(ID))
	fd.User = store.EncodeUid(userId).String()

	return &fd, nil

}

// FileDeleteUnused deletes file upload records.
func (a *adapter) FileDeleteUnused(olderThan time.Time, limit int) ([]string, error) {
	ctx, cancel := a.getContextForTx()
	if cancel != nil {
		defer cancel()
	}
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Garbage collecting entries which as either marked as deleted, or lack message references, or have no user assigned.
	query := "SELECT fu.id,fu.location FROM fileuploads AS fu LEFT JOIN filemsglinks AS fml ON fml.fileid=fu.id " +
		"WHERE fml.id IS NULL"
	var args []interface{}

	if !olderThan.IsZero() {
		query += " AND fu.updatedat<?"
		args = append(args, olderThan)
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	query, _ = expandQuery(query, args...)

	rows, err := tx.Query(ctx, query, args...)
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
		query, ids = expandQuery("DELETE FROM fileuploads WHERE id IN (?)", ids)
		_, err = tx.Exec(ctx, query, ids...)
		if err != nil {
			return nil, err
		}
	}

	return locations, tx.Commit(ctx)
}

// FileLinkAttachments connects given topic or message to the file record IDs from the list.
func (a *adapter) FileLinkAttachments(topic string, userId, msgId t.Uid, fids []string) error {
	if len(fids) == 0 || (topic == "" && msgId.IsZero() && userId.IsZero()) {
		return t.ErrMalformed
	}
	now := t.TimeNow()

	var args []interface{}
	var linkId interface{}
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
	var dids []interface{}
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
	tx, err := a.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		}
	}()

	// Unlink earlier uploads on the same topic or user allowing them to be garbage-collected.
	if msgId.IsZero() {
		sql := "DELETE FROM filemsglinks WHERE " + linkBy + "=$1"
		_, err = tx.Exec(ctx, sql, linkId)
		if err != nil {
			return err
		}
	}

	query, args := expandQuery("INSERT INTO filemsglinks(createdat,fileid,"+linkBy+") VALUES (?,?,?)"+
		strings.Repeat(",(?,?,?)", len(dids)-1), args...)

	_, err = tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// PCacheGet reads a persistet cache entry.
func (a *adapter) PCacheGet(key string) (string, error) {
	ctx, cancel := a.getContext()
	if cancel != nil {
		defer cancel()
	}

	var value string
	if err := a.db.QueryRow(ctx, `SELECT "value" FROM kvmeta WHERE "key"=$1 LIMIT 1`, key).Scan(&value); err != nil {
		if err == pgx.ErrNoRows {
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

	_, err := a.db.Exec(ctx, action+` INTO kvmeta("key",createdat,"value") VALUES($1,$2,$3)`, key, t.TimeNow(), value)
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

	_, err := a.db.Exec(ctx, `DELETE FROM kvmeta WHERE "key"=$1`, key)
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

	_, err := a.db.Exec(ctx, `DELETE FROM kvmeta WHERE "key" LIKE $1 AND createdat<$2`, keyPrefix+"%", olderThan)
	return err
}

// Helper functions

// Check if MySQL error is a Error Code: 1062. Duplicate entry ... for key ...
func isDupe(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505")
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 42P01")
}

func isMissingDb(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 3D000")
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
		if col == "public" || col == "trusted" || col == "private" {
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

	if val := update["Tags"]; val != nil {
		tags, _ = val.(t.StringSlice)
	}

	return []string(tags)
}

// Converting a structure with data to enter a connection string
func setConnStr(c configType) (string, error) {
	if c.User == "" || c.Passwd == "" || c.Host == "" || c.Port == "" || c.DBName == "" {
		return "", errors.New("adapter postgres invalid config value")
	}
	connStr := fmt.Sprintf("%s://%s:%s@%s:%s/%s?sslmode=disable&connect_timeout=%d",
		"postgres",
		c.User,
		c.Passwd,
		c.Host,
		c.Port,
		c.DBName,
		c.SqlTimeout)

	return connStr, nil
}

func expandQuery(query string, args ...interface{}) (string, []interface{}) {
	var expandedArgs []interface{}
	var expandedQuery string

	if len(args) != strings.Count(query, "?") {
		args = flatMap(args)
	}

	expandedQuery, expandedArgs, _ = sqlx.In(query, args...)

	placeholders := make([]string, len(expandedArgs))
	for i := range expandedArgs {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		expandedQuery = strings.Replace(expandedQuery, "?", placeholders[i], 1)
	}

	return expandedQuery, expandedArgs
}

func flatMap(slice []interface{}) []interface{} {
	var result []interface{}
	for _, v := range slice {
		switch reflect.TypeOf(v).Kind() {
		case reflect.Slice:
			s := reflect.ValueOf(v)
			for i := 0; i < s.Len(); i++ {
				result = append(result, s.Index(i).Interface())
			}
		default:
			result = append(result, v)
		}
	}
	return result
}

func init() {
	store.RegisterAdapter(&adapter{})
}

func (a *adapter) GetTopicsLastMsgWriter(subs []t.Subscription) []t.Subscription {
	return subs
}
