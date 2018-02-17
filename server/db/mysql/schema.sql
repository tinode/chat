DROP DATABASE IF EXISTS tinode;

CREATE DATABASE tinode 
	CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE tinode;


CREATE TABLE kvmeta(
	`key` CHAR(32), 
	`value` TEXT,
	PRIMARY KEY(`key`)
);

INSERT INTO kvmeta(`key`, `value`) VALUES("version", "100");

/*
type User struct {
	ObjHeader
	// Currently unused: Unconfirmed, Active, etc.
	State int

	// Default access to user for P2P topics (used as default modeGiven)
	Access DefaultAccess

	// Values for 'me' topic:

	// Last time when the user joined 'me' topic, by User Agent
	LastSeen time.Time
	// User agent provided when accessing the topic last time
	UserAgent string

	Public interface{}

	// Unique indexed tags (email, phone) for finding this user. Stored on the
	// 'users' as well as indexed in 'tagunique'
	Tags []string

	// Info on known devices, used for push notifications
	Devices map[string]*DeviceDef
}
*/

CREATE TABLE users(
	id 			BIGINT NOT NULL,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	state 		INT,
	access 		JSON,
	lastSeen 	DATETIME,
	userAgent 	VARCHAR(255),
	public 		JSON,
	
	PRIMARY KEY(id)
);

# Indexed tags
CREATE TABLE tags(
	id 		INT NOT NULL AUTO_INCREMENT,
	userid 	BIGINT NOT NULL,
	name 	CHAR(24) NOT NULL,
	tag 	VARCHAR(255) NOT NULL,
	cat 	INT NOT NULL, -- Category: 0 = user, 1 = topic
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);

CREATE INDEX tags_name_cat ON tags(name, cat);
CREATE INDEX tags_tag ON tags(tag);

# Indexed devices
CREATE TABLE devices(
	id 			INT NOT NULL AUTO_INCREMENT,
	userid 		BIGINT NOT NULL,
	hash 		CHAR(16) NOT NULL,
	deviceid 	TEXT NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id)
);

# Authentication records
CREATE TABLE auth(
	id 			INT NOT NULL AUTO_INCREMENT,
	`unique` 	VARCHAR(255) NOT NULL,
	userid 		BIGINT NOT NULL,
	authLvl 	INT NOT NULL,
	secret 		VARCHAR(255) NOT NULL,
	expires 	DATETIME,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX(`unique`)
);


# Topics
CREATE TABLE topics(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	name 		CHAR(24) NOT NULL,
	public 		JSON,
	
	PRIMARY KEY(id),
	UNIQUE INDEX(name)
);


# Subscriptions
CREATE TABLE subscriptions(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	userid 		BIGINT NOT NULL,
	topic 		CHAR(24) NOT NULL,
	
	PRIMARY KEY(id)	,
	FOREIGN KEY(userid) REFERENCES users(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	UNIQUE INDEX(topic, userid)
);

# Messages
CREATE TABLE messages(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	delid 		INT,
	seqid 		INT NOT NULL,
	topic 		VARCHAR(32) NOT NULL,
	`from` 		BIGINT NOT NULL,
	head 		JSON,
	content 	JSON,
	
	PRIMARY KEY(id),
	FOREIGN KEY(`from`) REFERENCES users(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	UNIQUE INDEX(topic, seqid)
);

# Create soft deletion table: topics name X userID x delId
CREATE TABLE softdel(
	id 			INT NOT NULL AUTO_INCREMENT,
	topic 		VARCHAR(32) NOT NULL,
	seqId 		INT NOT NULL,
	deletedFor 	BIGINT NOT NULL,
	delId 		INT NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	FOREIGN KEY(deletedFor) REFERENCES users(id),
	UNIQUE INDEX(topic, deletedFor, delId)
);

# Deletion log
CREATE TABLE dellog(
	id INT NOT NULL,
	PRIMARY KEY(id)
);
