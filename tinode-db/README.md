# Utility to Create "tinode" DB in a local RethinkDB Cluster

Compile then run from the command line.

Parameters:
 - `--reset`: delete `tinode` database if it exists, then re-create it in blank state; by default the utility will fail if the database already exists
 - `--data=[FILENAME]`: fill `tinode` database with sample data; if `FILENAME` is missing, attempt to load data from `./data.json`
 - `--db=RETHINKDB_CONNECTION_URL`, ex
 `rethinkdb://localhost:28015/tinode?authKey=&discover=false&maxIdle=&maxOpen=&timeout=&workerId=1&uidkey=base_64_encoded_string==` where
  - `tinode` is database name
  - `workerId` is the host id for snowflake for generating object ids
  - `uidkey` is XTEA encryption key to (weakly) encrypt snowflake-generated ids so they don't apper to be sequential; you want to keep it private in production
  - for `authKey`, `discover`, `timeout`, see [RethinkDB documentation](http://rethinkdb.com/api/javascript/connect/)
  - for `maxIdle`, `maxOpen` see [Gorethink documentation](https://github.com/dancannon/gorethink#connection-pool)

The default `data.json` file creates five users with user names `alice`, `bob`, `carol`, `dave`, `frank`. Passwords are the same as user name with 123 appended, e.g. user `alice` has password `alice123`. It will also create three group topics, and multiple peer to peer topics. All topics will be filled with random messages.

## DB schema:

### Database `tinode`

#### Table `users`
`users` stores user accounts

Fields:
* `Id` user id, primary key
* `CreatedAt` timestamp when the user was created
* `UpdatedAt` timestamp when user metadata was updated
* `DeletedAt` currently unused
* `Access` user's default access level for peer to peer topics
 * `Auth`, `Anon` permissions for authenticated and anonymous
* `Username` username part of "basic" authentication
* `Passhash` bcrypt password part of "basic" authentication
* `Public` application-defined data
* `State` currently unused

Indexes:
 * `Id` primary key
 * `Username` index

Sample:
```js
{
 "Access": {
  "Anon": 0 ,
  "Auth": 7
 } ,
 "CreatedAt": Wed Oct 14 2015 08:06:50 GMT+00:00 ,
 "DeletedAt": null ,
 "Id":  "_hyPZc_c0CA" ,
 "Passhash": <binary, 60 bytes, "24 32 61 24 31 30..."> ,
 "Public":  "Bob Smith" ,
 "State": 1 ,
 "UpdatedAt": Wed Oct 14 2015 08:06:50 GMT+00:00 ,
 "Username":  "bob"
}
```

#### Table `topics`
The table stores topics.

Fields:
 * `Id` unused
 * `CreatedAt` topic creation time
 * `UpdatedAt` timestamp of the last change to topic metadata
 * `DeletedAt` currently unused
 * `Access` stores topic's default access permissions
  * `Auth`, `Anon` permissions for authenticated and anonymous users respectively
 * `LastMessageAt` timestamp of the last message sent through the topic
 * `Name` topic name
 * `Public` application-defined data
 * `State` currently unused
 * `UseBt` currently unused

Indexes:
* `Id` primary key
* `Name` index

Sample:
```js
{
 "Access": {
  "Anon": 64 ,
  "Auth": 64
 } ,
 "CreatedAt": Thu Oct 15 2015 04:06:51 GMT+00:00 ,
 "DeletedAt": null ,
 "Id":  "9lKtRlK22z8" ,
 "LastMessageAt": Sat Oct 17 2015 13:51:56 GMT+00:00 ,
 "Name":  "p2pPf5tU9aEakY_KhNzrN7avA" ,
 "Public": null ,
 "State": 0 ,
 "UpdatedAt": Thu Oct 15 2015 04:06:51 GMT+00:00 ,
 "UseBt": false
}
```

#### Table `subscriptions`
The table stores relashinships between users and topics.

Fields:
 * `Id` used for object retrieval
 * `CreatedAt` timestamp when the user was created
 * `UpdatedAt` timestamp when user metadata was updated
 * `DeletedAt` currently unused
 * `ClearedAt` user soft-deleted messages older than this timestamp
 * `Topic` name of the topic subscribed to
 * `User` subscriber's user ID
 * `LastMessageAt` timestamp of the last message sent through the topic which given user was able to receive
 * `LastSeen` when the user last accessed the topic from the given client
  * `tag` client ID
  * `when` timestamp of the last access
 * `ModeWant` access mode that user wants when accessing the topic
 * `ModeGiven` access mode granted to user by the topic
 * `Private` application-defined data, accessible by the user only

Indexes:
 * `Id` primary key composed as "_topic name_ ':' _user ID_"
 * `User_UpdatedAt` compound index `["User", "UpdatedAt"]`
 * `Topic_UpdatedAt` compound index `["Topic", "UpdatedAt"]`

Sample:
```js
{
 "ClearedAt": null ,
 "CreatedAt": Thu Oct 15 2015 10:24:51 GMT+00:00 ,
 "DeletedAt": null ,
 "Id":  "grp27bT-X_W8__6:px8jR3_EtDk" ,
 "LastMessageAt": null ,
 "LastSeen": null , // NEEDS BETTER SAMPLE
 "ModeGiven": 7 ,
 "ModeWant": 7 ,
 "Private":  "Kirgudu" ,
 "Topic":  "grp27bT-X_W8__6" ,
 "UpdatedAt": Thu Oct 15 2015 10:24:51 GMT+00:00 ,
 "User":  "px8jR3_EtDk"
}
```
#### Table `messages`

The table stores `{data}` messages

Fields:
* `Id` currently unused
* `CreatedAt` timestamp when the message was created
* `UpdatedAt` unused, created for consistency
* `DeletedAt` currently unused
* `From` ID of the user who generated this message
* `Topic` which routed this message
* `Content` application-defined message payload

Indexes:
 * `Id` primary key
 * `Topic_CreatedAt` compound index `["Topic", "CreatedAt"]`

Sample:
```js
{
 "Content":  "Cogito cogito ergo cogito sum." ,
 "CreatedAt": Mon Oct 19 2015 12:20:01 GMT+00:00 ,
 "DeletedAt": null ,
 "From":  "BVbQIzu7hag" ,
 "Id":  "Zn1rC3UuhjE" ,
 "Topic":  "grpJ-l4wO0Fwvy3" ,
 "UpdatedAt": Mon Oct 19 2015 12:20:01 GMT+00:00
}
```

#### Table `_uniques`

The table is used to ensure uniqueness of user names since RethinkDB does not support unique secondary indexes. The unique values are stored as primary key entries in the form "_table name_ '!' _field name_ '!' _value_"

 * `id` primary key

Sample:
```js
{
"id":  "users!username!bob"
}
```
