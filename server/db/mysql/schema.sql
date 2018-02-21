DROP DATABASE IF EXISTS tinode;

CREATE DATABASE tinode CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE tinode;


CREATE TABLE kvmeta(
	`key` CHAR(32), 
	`value` TEXT,
	PRIMARY KEY(`key`)
);

INSERT INTO kvmeta(`key`, `value`) VALUES("version", "100");

CREATE TABLE users(
	id 			BIGINT NOT NULL,
	createdat 	DATETIME NOT NULL,
	updatedat 	DATETIME NOT NULL,
	deletedat 	DATETIME,
	state 		INT DEFAULT 0,
	access 		JSON,
	lastseen 	DATETIME,
	useragent 	VARCHAR(255) DEFAULT '',
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
	INDEX usertags_tag (tag)
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
	UNIQUE INDEX devices_hash (hash)
);

# Authentication records for the basic authentication scheme.
CREATE TABLE basicauth(
	id 			INT NOT NULL AUTO_INCREMENT,
	login	 	VARCHAR(255) NOT NULL,
	userid 		BIGINT NOT NULL,
	authlvl 	INT NOT NULL,
	secret 		VARCHAR(255) NOT NULL,
	expires 	DATETIME,
	
	PRIMARY KEY(id),
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX basicauth_login (login)
);


# Topics
CREATE TABLE topics(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdat 	DATETIME NOT NULL,
	updatedat 	DATETIME NOT NULL,
	deletedat 	DATETIME,
	name 		CHAR(25) NOT NULL,
	usebt 		INT DEFAULT 0,
	access 		JSON,
	seqid 		INT NOT NULL DEFAULT 0,
	delid 		INT DEFAULT 0,
	public 		JSON,
	tags		JSON, -- Denormalized array of tags
	
	PRIMARY KEY(id),
	UNIQUE INDEX topics_name (name)
);

# Indexed topic tags.
CREATE TABLE topictags(
	id 		INT NOT NULL AUTO_INCREMENT,
	topic 	CHAR(25) NOT NULL,
	tag 	VARCHAR(255) NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	INDEX topictags_tag (tag)
);

# Subscriptions
CREATE TABLE subscriptions(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdat 	DATETIME NOT NULL,
	updatedat 	DATETIME NOT NULL,
	deletedat 	DATETIME,
	userid 		BIGINT NOT NULL,
	topic 		CHAR(25) NOT NULL,
	delid      INT DEFAULT 0,
	recvseqid  INT DEFAULT 0,
	readseqid  INT DEFAULT 0,
	modewant	CHAR(8),
	modegiven  	CHAR(8),
	private 	JSON,
	
	PRIMARY KEY(id)	,
	FOREIGN KEY(userid) REFERENCES users(id),
	UNIQUE INDEX subscriptions_topic_userid (topic, userid),
	INDEX subscriptions_topic (topic)
);

# Messages
CREATE TABLE messages(
	id 			INT NOT NULL AUTO_INCREMENT,
	createdat 	DATETIME NOT NULL,
	updatedat 	DATETIME NOT NULL,
	deletedat 	DATETIME,
	delid 		INT DEFAULT 0,
	seqid 		INT NOT NULL,
	topic 		CHAR(25) NOT NULL,
	`from` 		BIGINT NOT NULL,
	head 		JSON,
	content 	JSON,
	
	PRIMARY KEY(id),
	FOREIGN KEY(`from`) REFERENCES users(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	UNIQUE INDEX messages_topic_seqid (topic, seqid)
);

# Create soft deletion table: topics name X userID x delId
CREATE TABLE softdel(
	id 			INT NOT NULL AUTO_INCREMENT,
	topic 		VARCHAR(25) NOT NULL,
	seqid 		INT NOT NULL,
	deletedfor 	BIGINT NOT NULL,
	delid 		INT NOT NULL,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	FOREIGN KEY(deletedfor) REFERENCES users(id),
	UNIQUE INDEX softdel_topic_deletedfor_seqid (topic,deletedfor,seqid),
	INDEX softdel_topic_delid_deletedfor (topic,delid,deletedfor)
);

# Deletion log
CREATE TABLE dellog(
	id INT NOT NULL AUTO_INCREMENT,
	topic 		VARCHAR(25) NOT NULL,
	deletedfor 	BIGINT NOT NULL DEFAULT 0,
	delid 		INT NOT NULL,
	low			INT NOT NULL,
	hi			INT NOT NULL DEFAULT 0,
	
	PRIMARY KEY(id),
	FOREIGN KEY(topic) REFERENCES topics(name),
	UNIQUE INDEX dellog_topic_delid (topic,delid),
	INDEX dellog_deletedfor (deletedfor)
);
