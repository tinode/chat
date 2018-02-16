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

CREATE TABLE users(
	id BIGINT NOT NULL,
	
	PRIMARY KEY(id)
);

# Indexed tags
CREATE TABLE tags(
	id INT NOT NULL AUTO_INCREMENT,
	name CHAR(24) NOT NULL,
	tag VARCHAR(255) NOT NULL,
	# Category: 0 = user, 1 = topic
	cat INT NOT NULL,
	PRIMARY KEY(id)
);

CREATE INDEX tags_name_cat ON tags(name, cat);
CREATE INDEX tags_tag ON tags(tag);

# Indexed devices
CREATE TABLE devices(
	id INT NOT NULL AUTO_INCREMENT,
	userID BIGINT NOT NULL,
	deviceID TEXT NOT NULL,
	PRIMARY KEY(id)
);

CREATE INDEX devices_userid ON devices(userID);

# Authentication records
CREATE TABLE auth(
	id INT NOT NULL AUTO_INCREMENT,
	`unique` VARCHAR(255) NOT NULL,
	userID BIGINT NOT NULL,
	authLvl INT NOT NULL,
	secret VARCHAR(255) NOT NULL,
	expires DATETIME,
	PRIMARY KEY(id)
);

CREATE INDEX auth_userid ON auth(userID);
CREATE UNIQUE INDEX auth_unique ON auth(`unique`);

# Subscriptions
CREATE TABLE subscriptions(
	id INT NOT NULL AUTO_INCREMENT,
	createdAt DATETIME NOT NULL,
	updatedAt DATETIME NOT NULL,
	deletedAt DATETIME,
	userID BIGINT NOT NULL,
	topic CHAR(24) NOT NULL,
	PRIMARY KEY(id)	
);

CREATE INDEX subscriptions_userid ON subscriptions(userid);
CREATE INDEX subscriptions_topic ON subscriptions(topic);

# Topics
CREATE TABLE topics(
	id INT NOT NULL AUTO_INCREMENT,
	createdAt DATETIME NOT NULL,
	updatedAt DATETIME NOT NULL,
	deletedAt DATETIME,
	name CHAR(24) NOT NULL,
	public JSON,
	PRIMARY KEY(id)
);

CREATE UNIQUE INDEX topics_name ON topics(name);

# Messages
CREATE TABLE messages(
	id INT NOT NULL AUTO_INCREMENT,
	createdAt DATETIME NOT NULL,
	updatedAt DATETIME NOT NULL,
	deletedAt DATETIME,
	delID INT,
	seqID INT NOT NULL,
	topic VARCHAR(32) NOT NULL,
	`from` BIGINT NOT NULL,
	head JSON,
	content JSON,
	PRIMARY KEY(id)
);

CREATE UNIQUE INDEX messages_topic_seqid ON messages(topic, seqID);

# Create soft deletion table: topics name X useeID x delId


# Deletion log
CREATE TABLE dellog(
	id INT NOT NULL,
	PRIMARY KEY(id)
);

/*
	// Compound index of hard-deleted messages
	if _, err := rdb.DB("tinode").Table("messages").IndexCreateFunc("Topic_DelId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("DelId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}
	// Compound multi-index of soft-deleted messages: each message gets multiple compound index entries like
	// [[Topic, User1, DelId1], [Topic, User2, DelId2],...]
	if _, err := rdb.DB("tinode").Table("messages").IndexCreateFunc("Topic_DeletedFor",
		func(row rdb.Term) interface{} {
			return row.Field("DeletedFor").Map(func(df rdb.Term) interface{} {
				return []interface{}{row.Field("Topic"), df.Field("User"), df.Field("DelId")}
			})
		}, rdb.IndexCreateOpts{Multi: true}).RunWrite(a.conn); err != nil {
		return err
	}
	// Log of deleted messages
	if _, err := rdb.DB("tinode").TableCreate("dellog", rdb.TableCreateOpts{PrimaryKey: "Id"}).RunWrite(a.conn); err != nil {
		return err
	}
	if _, err := rdb.DB("tinode").Table("dellog").IndexCreateFunc("Topic_DelId",
		func(row rdb.Term) interface{} {
			return []interface{}{row.Field("Topic"), row.Field("DelId")}
		}).RunWrite(a.conn); err != nil {
		return err
	}

*/