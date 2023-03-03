# THIS SCHEMA FILE IS FOR REFERENCE/DOCUMENTATION ONLY!
# DO NOT USE IT TO INITIALIZE THE DATABASE.
# Read installation instructions first.

# The following line will produce an intentional error.

'READ INSTALLATION INSTRUCTIONS!';

# The actual schema is below.

DROP DATABASE IF EXISTS tinode;

CREATE DATABASE tinode CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE tinode;


CREATE TABLE kvmeta(
	`key` VARCHAR(64),
	createdat DATETIME(3),
	`value` TEXT,
	PRIMARY KEY(`key`),
	INDEX kvmeta_createdat_key(createdat, `key`)
);

INSERT INTO kvmeta(`key`, `value`) VALUES("version", "100");

CREATE TABLE users(
	id 			BIGINT NOT NULL,
	createdat 	DATETIME(3) NOT NULL,
	updatedat 	DATETIME(3) NOT NULL,
	state 		SMALLINT NOT NULL DEFAULT 0,
	stateat 	DATETIME(3),
	access 		JSON,
	lastseen 	DATETIME,
	useragent 	VARCHAR(255) DEFAULT '',
	public 		JSON,
	tags		JSON, -- Denormalized array of tags

	PRIMARY KEY(id),
	INDEX users_state_stateat(state, stateat),
	INDEX users_lastseen_updatedat(lastseen, updatedat)
);

# Indexed user tags.
CREATE TABLE usertags(
	id 		INT NOT NULL AUTO_INCREMENT,
	userid 	BIGINT NOT NULL,
	tag 	VARCHAR(96) NOT NULL,

	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	INDEX usertags_tag(tag),
	UNIQUE INDEX usertags_userid_tag(userid, tag)
);

# Indexed devices. Normalized into a separate table.
CREATE TABLE devices(
	id 			INT NOT NULL AUTO_INCREMENT,
	userid 		BIGINT NOT NULL,
	hash 		CHAR(16) NOT NULL,
	deviceid 	TEXT NOT NULL,
	platform	VARCHAR(32),
	lastseen 	DATETIME NOT NULL,
	lang 		VARCHAR(8),

	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX devices_hash(hash)
);

# Authentication records for the basic authentication scheme.
CREATE TABLE auth(
	id 		INT NOT NULL AUTO_INCREMENT,
	uname	VARCHAR(32) NOT NULL,
	userid 	BIGINT NOT NULL,
	scheme	VARCHAR(16) NOT NULL,
	authlvl	SMALLINT NOT NULL,
	secret 	VARCHAR(255) NOT NULL,
	expires DATETIME,

	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX auth_userid_scheme(userid, scheme),
	UNIQUE INDEX auth_uname (uname)
);


# Topics
CREATE TABLE topics(
	id			INT NOT NULL AUTO_INCREMENT,
	createdat 	DATETIME(3) NOT NULL,
	updatedat 	DATETIME(3) NOT NULL,
	touchedat 	DATETIME(3),
	state		SMALLINT NOT NULL DEFAULT 0,
	stateat		DATETIME(3),
	name		CHAR(25) NOT NULL,
	usebt		TINYINT DEFAULT 0,
	owner		BIGINT NOT NULL DEFAULT 0,
	access		JSON,
	seqid		INT NOT NULL DEFAULT 0,
	delid		INT DEFAULT 0,
	public		JSON,
	tags		JSON, -- Denormalized array of tags

	PRIMARY KEY(id),
	UNIQUE INDEX topics_name (name),
	INDEX topics_owner(owner),
	INDEX topics_state_stateat(state, stateat)
);

# Indexed topic tags.
CREATE TABLE topictags(
	id 		INT NOT NULL AUTO_INCREMENT,
	topic 	CHAR(25) NOT NULL,
	tag 	VARCHAR(96) NOT NULL,

	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	INDEX topictags_tag (tag),
	UNIQUE INDEX topictags_userid_tag(topic, tag)
);

# Subscriptions
CREATE TABLE subscriptions(
	id			INT NOT NULL AUTO_INCREMENT,
	createdat	DATETIME(3) NOT NULL,
	updatedat	DATETIME(3) NOT NULL,
	deletedat	DATETIME(3),
	userid		BIGINT NOT NULL,
	topic		CHAR(25) NOT NULL,
	delid		INT DEFAULT 0,
	recvseqid	INT DEFAULT 0,
	readseqid	INT DEFAULT 0,
	modewant	CHAR(8),
	modegiven	CHAR(8),
	private		JSON,

	PRIMARY KEY(id)	,
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX subscriptions_topic_userid(topic, userid),
	INDEX subscriptions_topic(topic),
	INDEX subscriptions_deletedat(deletedat)
);

# Messages
CREATE TABLE messages(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdat 	DATETIME(3) NOT NULL,
	updatedat 	DATETIME(3) NOT NULL,
	deletedat 	DATETIME(3),
	delid 		INT DEFAULT 0,
	seqid 		INT NOT NULL,
	topic 		CHAR(25) NOT NULL,
	`from` 		BIGINT NOT NULL,
	head 		JSON,
	content 	JSON,

	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	UNIQUE INDEX messages_topic_seqid (topic, seqid)
);

# Deletion log
CREATE TABLE dellog(
	id			INT NOT NULL AUTO_INCREMENT,
	topic		CHAR(25) NOT NULL,
	deletedfor	BIGINT NOT NULL DEFAULT 0,
	delid		INT NOT NULL,
	low			INT NOT NULL,
	hi			INT NOT NULL,

	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	# For getting the list of deleted message ranges
	INDEX dellog_topic_delid_deletedfor(topic,delid,deletedfor),
	# Used when getting not-yet-deleted messages(messages LEFT JOIN dellog)
	INDEX dellog_topic_deletedfor_low_hi(topic,deletedfor,low,hi),
	# Used when deleting a user
	INDEX dellog_deletedfor(deletedfor)
);

# User credentials
CREATE TABLE credentials(
	id			INT NOT NULL AUTO_INCREMENT,
	createdat	DATETIME(3) NOT NULL,
	updatedat	DATETIME(3) NOT NULL,
	deletedat	DATETIME(3),
	method 		VARCHAR(16) NOT NULL,
	value		VARCHAR(128) NOT NULL,
	synthetic	VARCHAR(192) NOT NULL,
	userid 		BIGINT NOT NULL,
	resp		VARCHAR(255) NOT NULL,
	done		TINYINT NOT NULL DEFAULT 0,
	retries		INT NOT NULL DEFAULT 0,

	PRIMARY KEY(id),
	UNIQUE credentials_uniqueness(synthetic),
	FOREIGN KEY(userid) REFERENCES users(id),
);

# Records of uploaded files. Files themselves are stored elsewhere.
CREATE TABLE fileuploads(
	id			BIGINT NOT NULL,
	createdat	DATETIME(3) NOT NULL,
	updatedat	DATETIME(3) NOT NULL,
	userid		BIGINT,
	status		INT NOT NULL,
	mimetype	VARCHAR(255) NOT NULL,
	size		BIGINT NOT NULL,
	location	VARCHAR(2048) NOT NULL,

	PRIMARY KEY(id),
	INDEX fileuploads_status(status)
);

# Links between uploaded files and messages or topics.
CREATE TABLE filemsglinks(
	id			INT NOT NULL AUTO_INCREMENT,
	createdat	DATETIME(3) NOT NULL,
	fileid		BIGINT NOT NULL,
	msgid		INT,
	topic		CHAR(25),
	userid		BIGINT,

	PRIMARY KEY(id),
	FOREIGN KEY(fileid) REFERENCES fileuploads(id) ON DELETE CASCADE,
	FOREIGN KEY(msgid) REFERENCES messages(id) ON DELETE CASCADE,
	FOREIGN KEY(topicid) REFERENCES topics(id) ON DELETE CASCADE,
	FOREIGN KEY(userid) REFERENCES users(id) ON DELETE CASCADE
);
