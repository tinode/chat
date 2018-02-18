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
	tags		JSON, -- Denormalized array of tags
	
	PRIMARY KEY(id)
);

# Indexed user tags.
CREATE TABLE usertags(
	id 		INT NOT NULL AUTO_INCREMENT,
	userid 	BIGINT NOT NULL,
	tag 	VARCHAR(255) NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	INDEX(tag)
);

# Indexed devices. Normalized into a separate table.
CREATE TABLE devices(
	id 			INT NOT NULL AUTO_INCREMENT,
	userid 		BIGINT NOT NULL,
	hash 		CHAR(16) NOT NULL,
	deviceid 	TEXT NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX(hash)
);

# Authentication records for the basic authentication scheme.
CREATE TABLE basicauth(
	id 			INT NOT NULL AUTO_INCREMENT,
	login	 	VARCHAR(255) NOT NULL,
	userid 		BIGINT NOT NULL,
	authLvl 	INT NOT NULL,
	secret 		VARCHAR(255) NOT NULL,
	expires 	DATETIME,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX(login)
);


# Topics
CREATE TABLE topics(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	name 		CHAR(25) NOT NULL,
	useBt 		INT,
	access 		JSON,
	seqid 		INT NOT NULL DEFAULT 0,
	delid 		INT,
	public 		JSON,
	tags		JSON, -- Denormalized array of tags
	
	PRIMARY KEY(id),
	UNIQUE INDEX(name)
);

# Indexed topic tags.
CREATE TABLE topictags(
	id 		INT NOT NULL AUTO_INCREMENT,
	topic 	CHAR(24) NOT NULL,
	tag 	VARCHAR(255) NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	INDEX(tag)
);

# Subscriptions
CREATE TABLE subscriptions(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	userid 		BIGINT NOT NULL,
	topic 		CHAR(25) NOT NULL,
	delId      INT,
	recvSeqId  INT,
	readSeqId  INT,
	modeWant	CHAR(8),
	modeGiven  	CHAR(8),
	private 	JSON,
	
	PRIMARY KEY(id)	,
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX(topic, userid),
	INDEX(topic)
);

# Messages
CREATE TABLE messages(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdAt 	DATETIME NOT NULL,
	updatedAt 	DATETIME NOT NULL,
	deletedAt 	DATETIME,
	delid 		INT,
	seqid 		INT NOT NULL,
	topic 		CHAR(25) NOT NULL,
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
