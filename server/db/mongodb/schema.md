# MongoDB Database Schema

## Database `tinode`

### Table `users`
Stores user accounts

Fields:
* `_id` user id, primary key
* `createdat` timestamp when the user was created
* `updatedat` timestamp when user metadata was updated
* `access` user's default access level for peer-to-peer topics
    * `auth`, `anon` default permissions for authenticated and anonymous users
* `public` application-defined data
* `state` account state: normal (ok), suspended, soft-deleted
* `stateat` timestamp when the state was last updated or NULL
* `lastseen` timestamp when the user was last online
* `useragent` client User-Agent used when last online
* `tags` unique strings for user discovery
* `devices` client devices for push notifications
    * `deviceid` device registration ID
    * `platform` device platform string (iOS, Android, Web)
    * `lastseen` last logged in
    * `lang` device language, ISO code

Indexes:
 * `_id` primary key
 * `tags` multikey-index (indexed array)
 * `deletedat` index
 * `deviceids` multikey-index of push notification tokens

Sample:
```json
{
  "access": {
    "anon": 0 ,
    "auth": 47
  } ,
  "createdat": "2019-10-11T12:13:14.522Z" , 
  "state": 0,
  "stateat": null ,
  "devices": null ,
  "_id": "7yUCHniegrM" ,
  "lastseen": "2019-10-11T12:13:14.522Z" ,
  "public": {
    "fn": "Alice Johnson" ,
    "photo": {
      "data": Binary('/9j/4AAQSkZJRgAB...'),
      "type": "jpg"
    }
  } ,
  "state": 1 ,
  "tags": [
    "email:alice@example.com" ,
    "tel:17025550001"
  ] ,
  "updatedat": "2019-10-11T12:13:14.522Z",
  "useragent": "TinodeWeb/0.13 (MacIntel) tinodejs/0.13"
}
```

### Table `auth`
Stores authentication secrets

Fields:
* `_id` unique string which identifies this record, primary key; defined as "_authentication scheme_':'_some unique value per scheme_"
* `userid` ID of the user who owns the record
* `secret` shared secret, for instance bcrypt of password
* `authLvl` authentication level
* `expires` timestamp when the records expires


Indexes:
 * `_id` primary key
 * `userid` index

Sample:
```json
{
   "_id": "basic:alice" ,
   "authLvl": 20 ,
   "expires": "2019-10-11T12:13:14.522Z" ,
   "secret": Binary('/9j/RgAB...'),
   "userid": "7yUCHniegrM"
}
```

### Table `topics`
The table stores topics.

Fields:
 * `_id` name of the topic, primary key
 * `createdat` topic creation time
 * `updatedat` timestamp of the last change to topic metadata
 * `access` stores topic's default access permissions
    * `auth`, `anon` permissions for authenticated and anonymous users respectively
 * `owner` ID of the user who owns the topic
 * `public` application-defined data
 * `state` topic state: normal (ok), suspended, soft-deleted
 * `stateat` timestamp when the state was last updated or NULL
 * `seqid` sequential ID of the last message
 * `delid` topic-sequential ID of the deletion operation
 * `usebt` currently unused

Indexes:
* `_id` primary key
* `owner` index
* `tags` multikey index

Sample:
```json
{
 "access": {
  "anon": 64 ,
  "auth": 64
 } ,
 "delid": 0,
 "createdat": "2019-10-11T12:13:14.522Z",
 "lastmessageat": "2019-10-11T12:13:14.522Z" ,
 "id":  "p2pavVGHLCBbKrvJQIeeJ6Csw" ,
 "owner": "v2JyG4OLSoA" ,
 "public": {
   "fn":  "Travel, travel, travel" ,
   "photo": {
     "data": Binary('/9j/RgAB...') ,
     "type":  "jpg"
   }
 } ,
 "seqid": 14,
 "state": 0,
 "stateat": null,
 "updatedat": "2019-10-11T12:13:14.522Z" ,
 "usebt": false
}
```

### Table `subscriptions`
The table stores relationships between users and topics.

Fields:
 * `_id` used for object retrieval
 * `createdat` timestamp when the user was created
 * `updatedat` timestamp when user metadata was updated
 * `deletedat` currently unused
 * `readseqid` id of the message last read by the user
 * `recvseqid` id of the message last received by user device
 * `delid` topic-sequential ID of the soft-deletion operation
 * `topic` name of the topic subscribed to
 * `user` subscriber's user ID
 * `modewant` access mode that user wants when accessing the topic
 * `modegiven` access mode granted to user by the topic
 * `private` application-defined data, accessible by the user only

Indexes:
 * `_id` primary key composed as "_topic name_':'_user ID_"
 * `user` index
 * `topic` index

Sample:
```json
{
  "_id": "grpjajVKrHn0PU:v2JyG4OLSoA" ,
  "createdat": "2019-10-11T12:13:14.522Z" ,
  "updatedat": "2019-10-11T12:13:14.522Z" ,
  "deletedat": null ,
  "user": "v2JyG4OLSoA",
  "topic": "grpjajVKrHn0PU" ,
  "recvseqid": 0 ,
  "readseqid": 0 ,
  "modewant": 47 ,
  "modegiven": 47 ,
  "private": "Kirgudu" ,
  "state": 0 
}
```

### Table `messages`
The table stores `{data}` messages

Fields:
* `_id` currently unused, primary key
* `createdat` timestamp when the message was created
* `updatedat` initially equal to CreatedAt, for deleted messages equal to DeletedAt
* `deletedfor` array of user IDs which soft-deleted the message
    * `delid` topic-sequential ID of the soft-deletion operation
    * `user` ID of the user who soft-deleted the message
* `from` ID of the user who generated this message
* `topic` which received this message
* `seqid` messages ID - sequential number of the message in the topic
* `head` message headers
* `attachments` denormalized IDs of files attached to the message
* `content` application-defined message payload

Indexes:
 * `_id` primary key

Sample:
```json
{
  "_id":  "LLXKEe9W4Bs" ,
  "createdat": "2019-10-11T12:13:14.522Z" ,
  "updatedat": "2019-10-11T12:13:14.522Z",
  "deletedfor": [
    {
      "delid": 1 ,
      "user":  "wTI0jO9rEqY"
    }
  ] ,
  "seqid": 3 ,
  "topic":  "p2pJhbJnya8z5PBMjSM72sSpg",
  "from":  "wTI0jO9rEqY" ,
  "head": {
    "mime":  "text/x-drafty"
  } ,
  "content": {
    "fmt": [
      {
        "len": 6 ,
        "tp":  "ST"
      }
    ] ,
    "txt":  "Hello!"
  }
}
```

### Table `dellog`
The table stores records of message deletions

Fields:
* `_id` currently unused, primary key
* `createdat` timestamp when the record was created
* `updatedat` timestamp equal to CreatedAt
* `delid` topic-sequential ID of the deletion operation.
* `deletedfor` ID of the user for soft-deletions, blank string for hard-deletions
* `topic` affected topic
* `seqidranges` array of ranges of deleted message IDs (see `messages.seqid`)

Indexes:
 * `_id` primary key
* `topic_delid` compound index `["Topic", "DelId"]`

Sample:
```json
{
  "_id": "9LfrjW349Rc",
  "createdat": "2019-10-11T12:13:14.522Z",
  "updatedat": "2019-10-11T12:13:14.522Z",
  "topic":  "grpGx7fpjQwVC0",
  "delid": 18,
  "deletedfor": "xY-YHx09-WI",
  "seqidranges": [
    {
      "low": 20,
      "hi": 25
    }
  ]
}
```

### Table `credentials`
The tables stores user credentials used for validation.

* `_id` credential, primary key
* `createdat` timestamp when the record was created
* `updatedat` timestamp when the last validation attempt was performed (successful or not).
* `method` validation method
* `done` indicator if the credential is validated
* `resp` expected validation response
* `retries` number of failed attempts at validation
* `user` id of the user who owns this credential
* `value` value of the credential

Indexes:
* `_id` Primary key composed either as `user`:`method`:`value` for unconfirmed credentials or as `method`:`value` for confirmed.
* `user` Index

Sample:
```json
{
  "Id": "tel:+17025550001",
  "CreatedAt": "2019-10-11T12:13:14.522Z",
  "UpdatedAt": "2019-10-11T12:13:14.522Z",
  "Method":  "tel" ,
  "Done": true ,
  "Resp":  "123456" ,
  "Retries": 0 ,
  "User":  "k3srBRk9RYw" ,
  "Value":  "+17025550001"
}
```

### Table `fileuploads`
The table stores records of uploaded files. The files themselves are stored outside of the database.
* `_id` unique user-visible file name, primary key
* `createdat` timestamp when the record was created
* `updatedat` timestamp of when th upload has cmpleted or failed
* `user` id of the user who uploaded this file.
* `location` actual location of the file on the server.
* `mimetype` file content type as a [Mime](https://en.wikipedia.org/wiki/MIME) string.
* `size` size of the file in bytes. Could be 0 if upload has not completed yet.
* `usecount` count of messages referencing this file.
* `status` upload status: 0 pending, 1 completed, -1 failed.

Indexes:
 * `_id` file name, primary key
 * `user` index
 * `usecount` index

Sample:
```json
{
  "_id":  "sFmjlQ_kA6A" ,
  "createdat": "2019-10-11T12:13:14.522Z" ,
  "updatedat": "2019-10-11T12:13:14.522Z" ,
  "location":  "uploads/sFmjlQ_kA6A" ,
  "mimetype":  "image/jpeg" ,
  "size": 54961090 ,
  "usecount": 3,
  "status": 1 ,
  "user":  "7j-RR1V7O3Y"
}
```