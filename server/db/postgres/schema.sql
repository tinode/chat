# THIS SCHEMA FILE IS FOR REFERENCE/DOCUMENTATION ONLY!
# DO NOT USE IT TO INITIALIZE THE DATABASE.
# Read installation instructions first.

# The following line will produce an intentional error.

'READ INSTALLATION INSTRUCTIONS!';

# The actual schema is below.

DROP DATABASE IF EXISTS tinode;

CREATE DATABASE tinode CHARACTER SET utf8;

USE tinode;


CREATE TABLE kvmeta(
	`key` CHAR(32), 
	`value` TEXT,
	PRIMARY KEY(`key`)
);

INSERT INTO kvmeta(`key`, `value`) VALUES("version", "112");

CREATE TABLE users(
	id 			BIGINT NOT NULL,
	createdat 	TIMESTAMP(3) NOT NULL,
	updatedat 	TIMESTAMP(3) NOT NULL,
	state 		SMALLINT NOT NULL DEFAULT 0,
	stateat 	TIMESTAMP(3),
	access 		JSON,
	lastseen 	TIMESTAMP,
	useragent 	VARCHAR(255) DEFAULT '',
	public 		JSON,
	tags		JSON, -- Denormalized array of tags
	
	PRIMARY KEY(id)
);
CREATE INDEX users_state_stateat ON users(state, stateat);

# Indexed user tags.
CREATE TABLE usertags(
	id 		SERIAL NOT NULL,
	userid 	BIGINT NOT NULL,
	tag 	VARCHAR(96) NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);
CREATE INDEX usertags_tag ON usertags(tag);
CREATE UNIQUE INDEX usertags_userid_tag ON usertags(userid, tag);

# Indexed devices. Normalized into a separate table.
CREATE TABLE devices(
	id 			SERIAL NOT NULL,
	userid 		BIGINT NOT NULL,
	hash 		CHAR(16) NOT NULL,
	deviceid 	TEXT NOT NULL,
	platform	VARCHAR(32),
	lastseen 	TIMESTAMP NOT NULL,
	lang 		VARCHAR(8),
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);
CREATE UNIQUE INDEX devices_hash ON devices(hash);

# Authentication records for the basic authentication scheme.
CREATE TABLE auth(
	id 		SERIAL NOT NULL,
	uname	VARCHAR(32) NOT NULL,
	userid 	BIGINT NOT NULL,
	scheme	VARCHAR(16) NOT NULL,
	authlvl	INT NOT NULL,
	secret 	VARCHAR(255) NOT NULL,
	expires TIMESTAMP,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);
CREATE UNIQUE INDEX auth_userid_scheme ON auth(userid, scheme);
CREATE UNIQUE INDEX auth_uname ON auth(uname);


# Topics
CREATE TABLE topics(
	id			SERIAL NOT NULL,
	createdat 	TIMESTAMP(3) NOT NULL,
	updatedat 	TIMESTAMP(3) NOT NULL,
	touchedat 	TIMESTAMP(3),
	state		SMALLINT NOT NULL DEFAULT 0,
	stateat		TIMESTAMP(3),
	name		CHAR(25) NOT NULL,
	usebt		SMALLINT DEFAULT 0,
	owner		BIGINT NOT NULL DEFAULT 0,
	access		JSON,
	seqid		INT NOT NULL DEFAULT 0,
	delid		INT DEFAULT 0,
	public		JSON,
	tags		JSON, -- Denormalized array of tags
	
	PRIMARY KEY(id)
);
CREATE UNIQUE INDEX topics_name ON topics(name);
CREATE INDEX topics_owner ON topics(owner);
CREATE INDEX topics_state_stateat ON topics(state, stateat);

# Indexed topic tags.
CREATE TABLE topictags(
	id 		SERIAL NOT NULL,
	topic 	CHAR(25) NOT NULL,
	tag 	VARCHAR(96) NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name)
);
CREATE INDEX topictags_tag ON topictags(tag);
CREATE UNIQUE INDEX topictags_userid_tag ON topictags(topic, tag);

# Subscriptions
CREATE TABLE subscriptions(
	id			SERIAL NOT NULL,
	createdat	TIMESTAMP(3) NOT NULL,
	updatedat	TIMESTAMP(3) NOT NULL,
	deletedat	TIMESTAMP(3),
	userid		BIGINT NOT NULL,
	topic		CHAR(25) NOT NULL,
	delid		INT DEFAULT 0,
	recvseqid	INT DEFAULT 0,
	readseqid	INT DEFAULT 0,
	modewant	CHAR(8),
	modegiven	CHAR(8),
	private		JSON,
	
	PRIMARY KEY(id)	,
	FOREIGN KEY(userid) REFERENCES users(id)
);
CREATE UNIQUE INDEX subscriptions_topic_userid ON subscriptions(topic, userid);
CREATE INDEX subscriptions_topic ON subscriptions(topic);
CREATE INDEX subscriptions_deletedat ON subscriptions(deletedat);

# Messages
CREATE TABLE messages(
	id 			SERIAL NOT NULL,
	createdat 	TIMESTAMP(3) NOT NULL,
	updatedat 	TIMESTAMP(3) NOT NULL,
	deletedat 	TIMESTAMP(3),
	delid 		INT DEFAULT 0,
	seqid 		INT NOT NULL,
	topic 		CHAR(25) NOT NULL,
	`from` 		BIGINT NOT NULL,
	head 		JSON,
	content 	JSON,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name)
);
CREATE UNIQUE INDEX messages_topic_seqid ON messages(topic, seqid);

# Deletion log
CREATE TABLE dellog(
	id			SERIAL NOT NULL,
	topic		CHAR(25) NOT NULL,
	deletedfor	BIGINT NOT NULL DEFAULT 0,
	delid		INT NOT NULL,
	low			INT NOT NULL,
	hi			INT NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name)
);
CREATE INDEX dellog_topic_delid_deletedfor ON dellog(topic,delid,deletedfor);
CREATE INDEX dellog_topic_deletedfor_low_hi ON dellog(topic,deletedfor,low,hi);
CREATE INDEX dellog_deletedfor ON dellog(deletedfor);

# User credentials
CREATE TABLE credentials(
	id			SERIAL NOT NULL,
	createdat	TIMESTAMP(3) NOT NULL,
	updatedat	TIMESTAMP(3) NOT NULL,
	deletedat	TIMESTAMP(3),
	method 		VARCHAR(16) NOT NULL,
	value		VARCHAR(128) NOT NULL,
	synthetic	VARCHAR(192) NOT NULL,
	userid 		BIGINT NOT NULL,
	resp		VARCHAR(255) NOT NULL,
	done		SMALLINT NOT NULL DEFAULT 0,
	retries		INT NOT NULL DEFAULT 0,
		
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);
CREATE UNIQUE INDEX credentials_uniqueness ON credentials(synthetic);

# Records of uploaded files. Files themselves are stored elsewhere.
CREATE TABLE fileuploads(
	id			BIGINT NOT NULL,
	createdat	TIMESTAMP(3) NOT NULL,
	updatedat	TIMESTAMP(3) NOT NULL,
	userid		BIGINT,
	status		INT NOT NULL,
	mimetype	VARCHAR(255) NOT NULL,
	size		BIGINT NOT NULL,
	location	VARCHAR(2048) NOT NULL,
	
	PRIMARY KEY(id)
);
CREATE INDEX fileuploads_status ON fileuploads(status);

# Links between uploaded files and messages or topics.
CREATE TABLE filepgglinks(
	id			SERIAL NOT NULL,
	createdat	TIMESTAMP(3) NOT NULL,
	fileid		BIGINT NOT NULL,
	pggid		INT,
	topic		CHAR(25),
	userid		BIGINT,

	PRIMARY KEY(id),
	FOREIGN KEY(fileid) REFERENCES fileuploads(id) ON DELETE CASCADE,
	FOREIGN KEY(pggid) REFERENCES messages(id) ON DELETE CASCADE,
	FOREIGN KEY(topicid) REFERENCES topics(id) ON DELETE CASCADE,
	FOREIGN KEY(userid) REFERENCES users(id) ON DELETE CASCADE
);