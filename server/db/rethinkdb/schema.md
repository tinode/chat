# RethinkDB Database Schema

## Database `tinode`

### Table `users`
Stores user accounts

Fields:
* `Id` user id, primary key
* `CreatedAt` timestamp when the user was created
* `UpdatedAt` timestamp when user metadata was updated
* `DeletedAt` currently unused
* `Access` user's default access level for peer-to-peer topics
 * `Auth`, `Anon` default permissions for authenticated and anonymous users
* `Public` application-defined data
* `State` currently unused
* `LastSeen` timestamp when the user was last online
* `UserAgent` client User-Agent used when last online
* `SeqId` last message id on user's 'me' topics
* `ClearId` maximum message id that's been cleared (deleted)
* `Tags` unique strings for user discovery
* `Devices` client devices for push notifications
 * `DeviceId` device registration ID
 * `Platform` device platform (iOS, Android, Web)
 * `LastSeen` last logged in
 * `Lang` device language, ISO code

Indexes:
 * `Id` primary key
 * `Tags` multi-index (indexed array)

Sample:
```js
{
  "Access": {
    "Anon": 0 ,
    "Auth": 47
  } ,
  "ClearId": 0 ,
  "CreatedAt": Mon Jul 24 2017 11:16:38 GMT+00:00 ,
  "DeletedAt": null ,
  "Devices": null ,
  "Id": "7yUCHniegrM" ,
  "LastSeen": Mon Jan 01 1 00:00:00 GMT+00:00 ,
  "Public": {
    "fn": "Alice Johnson" ,
    "photo": {
      "data": <binary, 6.5KB, "ff d8 ff e0 00 10..."> ,
      "type": "jpg"
    }
  } ,
  "SeqId": 0 ,
  "State": 1 ,
  "Tags": [
    "email:alice@example.com" ,
    "tel:17025550001"
  ] ,
  "UpdatedAt": Mon Jul 24 2017 11:16:38 GMT+00:00 ,
  "UserAgent": "TinodeWeb/0.13 (MacIntel) tinodejs/0.13"
}
```

### Table `auth`
Stores authentication secrets

Fields:
* `userid` ID of the user who owns the record
* `unique` unique string which identifies this record, primary key; defined as "_authentication scheme_':'_some unique value per scheme_"
* `secret` shared secret, for instance bcrypt of password
* `authLvl` authentication level
* `expires` timestamp when the records expires


Indexes:
 * `unique` primary key
 * `userid` index

Sample:
```js
{
   "authLvl": 20 ,
   "expires": Mon Jan 01 1 00:00:00 GMT+00:00 ,
   "secret": <binary, 60 bytes, "24 32 61 24 31 30..."> ,
   "unique": "basic:alice" ,
   "userid": "7yUCHniegrM"
}
 ```

### Table `tagunique`
Indexed user tags, mostly to ensure tag uniqueness

Fields:
`Id` unique tag, primary keys
`Source` ID of the user who owns the tag

Indexes:

Sample:
```js
{
  "Id": "email:alice@example.com" ,
  "Source": "7yUCHniegrM"
}
```

### Table `topics`
The table stores topics.

Fields:
 * `Id` name of the topic, primary key
 * `CreatedAt` topic creation time
 * `UpdatedAt` timestamp of the last change to topic metadata
 * `DeletedAt` currently unused
 * `Access` stores topic's default access permissions
  * `Auth`, `Anon` permissions for authenticated and anonymous users respectively
 * `Public` application-defined data
 * `State` currently unused
 * `SeqId` id of the last message
 * `ClearId` id of the message last cleared (deleted)
 * `UseBt` currently unused

Indexes:
* `Id` primary key

Sample:
```js
{
 "Access": {
  "Anon": 64 ,
  "Auth": 64
 } ,
 "ClearId": 0,
 "CreatedAt": Thu Oct 15 2015 04:06:51 GMT+00:00 ,
 "DeletedAt": null ,
 "LastMessageAt": Sat Oct 17 2015 13:51:56 GMT+00:00 ,
 "Id":  "p2pavVGHLCBbKrvJQIeeJ6Csw" ,
 "Public": {
   "fn":  "Travel, travel, travel" ,
   "photo": {
     "data": <binary, 6.2KB, "ff d8 ff e0 00 10..."> ,
     "type":  "jpg"
   }
 } ,
 "SeqId": 14,
 "State": 0 ,
 "UpdatedAt": Thu Oct 15 2015 04:06:51 GMT+00:00 ,
 "UseBt": false
}
```

### Table `subscriptions`
The table stores relationships between users and topics.

Fields:
 * `Id` used for object retrieval
 * `CreatedAt` timestamp when the user was created
 * `UpdatedAt` timestamp when user metadata was updated
 * `DeletedAt` currently unused
 * `ReadSeqId` id of the message last read by the user
 * `RecvSeqId` id of the message last received by user device
 * `ClearedId` user soft-deleted messages with id lower or equal to this id
 * `Topic` name of the topic subscribed to
 * `User` subscriber's user ID
 * `ModeWant` access mode that user wants when accessing the topic
 * `ModeGiven` access mode granted to user by the topic
 * `Private` application-defined data, accessible by the user only

Indexes:
 * `Id` primary key composed as "_topic name_':'_user ID_"
 * `User` index
 * `Topic` index

Sample:
```js
{
  "ClearId": 0 ,
  "CreatedAt": Tue Jul 25 2017 15:34:39 GMT+00:00 ,
  "DeletedAt": null ,
  "Id": "grpjajVKrHn0PU:v2JyG4OLSoA" ,
  "ModeGiven": 47 ,
  "ModeWant": 47 ,
  "Private": "Kirgudu" ,
  "ReadSeqId": 0 ,
  "RecvSeqId": 0 ,
  "State": 0 ,
  "Topic": "grpjajVKrHn0PU" ,
  "UpdatedAt": Tue Jul 25 2017 15:34:39 GMT+00:00 ,
  "User": "v2JyG4OLSoA"
}
```

### Table `messages`

The table stores `{data}` messages

Fields:
* `Id` currently unused, primary key
* `CreatedAt` timestamp when the message was created
* `UpdatedAt` initially equal to CreatedAt, for deleted messages equal to DeletedAt
* `DeletedAt` timestamp when the message was deleted for all users
* `DeletedFor` IDs of the users who have soft-deleted the message
* `From` ID of the user who generated this message
* `Topic` which received this message
* `SeqId` id of the message
* `Head` message headers
* `Content` application-defined message payload

Indexes:
 * `Id` primary key
 * `Topic_SeqId` compound index `["Topic", "SeqId"]`

Sample:
```js
{
  "Content":  "Signs point to yes" ,
  "CreatedAt": Thu Jul 27 2017 14:49:44 GMT+00:00 ,
  "DeletedAt": null ,
  "DeletedFor": [ ],
  "From":  "7yUCHniegrM" ,
  "Head": null ,
  "Id":  "5b6vNnjmpFM" ,
  "SeqId": 7 ,
  "Topic":  "p2pTDqwKVw-jLDvJQIeeJ6Csw" ,
  "UpdatedAt": Thu Jul 27 2017 14:49:44 GMT+00:00
}
```
