# RethinkDB Database Schema

## Database `tinode`

### Table `users`
Stores user accounts

Fields:
* `Id` user id, primary key
* `CreatedAt` timestamp when the user was created
* `UpdatedAt` timestamp when user metadata was updated
* `State` account state: normal (ok), suspended, soft-deleted
* `StateAt` timestamp when the state was last updated or NULL
* `Access` user's default access level for peer-to-peer topics
 * `Auth`, `Anon` default permissions for authenticated and anonymous users
* `Public` application-defined data
* `State` state of the user: normal, disabled, deleted
* `StateAt` timestamp when the state was last updated or NULL
* `LastSeen` timestamp when the user was last online
* `UserAgent` client User-Agent used when last online
* `Tags` unique strings for user discovery
* `Devices` client devices for push notifications
 * `DeviceId` device registration ID
 * `Platform` device platform string (iOS, Android, Web)
 * `LastSeen` last logged in
 * `Lang` device language, ISO code

Indexes:
 * `Id` primary key
 * `Tags` multi-index (indexed array)
 * `DeletedAt` index
 * `DeviceIds` multi-index of push notification tokens

Sample:
```js
{
  "Access": {
    "Anon": 0 ,
    "Auth": 47
  } ,
  "CreatedAt": Mon Jul 24 2017 11:16:38 GMT+00:00 ,
  "State": 0,
  "StateAt": null ,
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

### Table `topics`
The table stores topics.

Fields:
 * `Id` name of the topic, primary key
 * `CreatedAt` topic creation time
 * `UpdatedAt` timestamp of the last change to topic metadata
 * `State` topic state: normal (ok), suspended, soft-deleted
 * `StateAt` timestamp when the state was last updated or NULL
 * `Access` stores topic's default access permissions
  * `Auth`, `Anon` permissions for authenticated and anonymous users respectively
 * `Owner` ID of the user who owns the topic
 * `Public` application-defined data
 * `State` state of the topic: normal, disabled, deleted
 * `SeqId` sequential ID of the last message
 * `DelId` topic-sequential ID of the deletion operation
 * `UseBt` indicator that channel functionality is enabled in the topic

Indexes:
* `Id` primary key
* `Owner` index

Sample:
```js
{
 "Access": {
  "Anon": 64 ,
  "Auth": 64
 } ,
 "DelId": 0,
 "CreatedAt": Thu Oct 15 2015 04:06:51 GMT+00:00 ,
 "State": 0 ,
 "StateAt": null ,
 "LastMessageAt": Sat Oct 17 2015 13:51:56 GMT+00:00 ,
 "Id":  "p2pavVGHLCBbKrvJQIeeJ6Csw" ,
 "Owner": "v2JyG4OLSoA" ,
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
 * `CreatedAt` timestamp when the subscription was created
 * `UpdatedAt` timestamp when the subscription was updated
 * `DeletedAt` timestamp when the subscription was deleted
 * `ReadSeqId` id of the message last read by the user
 * `RecvSeqId` id of the message last received by any user device
 * `DelId` topic-sequential ID of the soft-deletion operation
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
* `DeletedFor` array of user IDs which soft-deleted the message
 * `DelId` topic-sequential ID of the soft-deletion operation
 * `User` ID of the user who soft-deleted the message
* `From` ID of the user who generated this message
* `Topic` which received this message
* `SeqId` messages ID - sequential number of the message in the topic
* `Head` message headers
* `Attachments` denormalized IDs of files attached to the message
* `Content` application-defined message payload

Indexes:
 * `Id` primary key
 * `Topic_SeqId` compound index `["Topic", "SeqId"]`
 * `Topic_DelId` compound index `["Topic", "DelId"]`
 * `Topic_DeletedFor` compound multi-index `["Topic", "DeletedFor"("User"), "DeletedFor"("DelId")]`

Sample:
```js
{
  "Content": {
    "fmt": [
      {
        "len": 6 ,
        "tp":  "ST"
      }
    ] ,
    "txt":  "Hello!"
  } ,
  "CreatedAt": Sun Dec 24 2017 05:16:23 GMT+00:00 ,
  "From":  "wTI0jO9rEqY" ,
  "Head": {
    "mime":  "text/x-drafty"
  } ,
  "DeletedFor": [
    {
      "DelId": 1 ,
      "User":  "wTI0jO9rEqY"
    }
  ] ,
  "Id":  "LLXKEe9W4Bs" ,
  "SeqId": 3 ,
  "Topic":  "p2pJhbJnya8z5PBMjSM72sSpg" ,
  "UpdatedAt": Sun Dec 24 2017 05:16:23 GMT+00:00
}
```

### Table `dellog`
The table stores records of message deletions

Fields:
* `Id` currently unused, primary key
* `CreatedAt` timestamp when the record was created
* `UpdatedAt` timestamp equal to CreatedAt
* `DelId` topic-sequential ID of the deletion operation.
* `DeletedFor` ID of the user for soft-deletions, blank string for hard-deletions
* `Topic` affected topic
* `SeqIdRanges` array of ranges of deleted message IDs (see `messages.SeqId`)

Indexes:
 * `Id` primary key
* `Topic_DelId` compound index `["Topic", "DelId"]`

Sample:
```js
{
  "Id":  "9LfrjW349Rc",
  "CreatedAt": Tue Dec 05 2017 01:51:38 GMT+00:00,
  "DelId": 18,
  "DeletedFor": "xY-YHx09-WI" ,

  "SeqIdRanges": [
    {
      "Low": 20,
      "Hi": 25,
    }
  ] ,
  "Topic":  "grpGx7fpjQwVC0" ,
  "UpdatedAt": Tue Dec 05 2017 01:51:38 GMT+00:00
}
```

### Table `credentials`
The tables stores user credentials used for validation.

* `Id` credential, primary key
* `CreatedAt` timestamp when the record was created
* `UpdatedAt` timestamp when the last validation attempt was performed (successful or not).
* `Method` validation method
* `Done` indicator if the credential is validated
* `Resp` expected validation response
* `Retries` number of failed attempts at validation
* `User` id of the user who owns this credential
* `Value` value of the credential
* `Closed` unvalidated credential is no longer being validated. Only one credential is not Closed for each user/method.

Indexes:
* `Id` Primary key composed either as `User`:`Method`:`Value` for unconfirmed credentials or as `Method`:`Value` for confirmed.
* `User` Index

Sample:
```js
{
  "Id": "tel:+17025550001",
  "CreatedAt": Sun Jun 10 2018 16:37:27 GMT+00:00 ,
  "UpdatedAt": Sun Jun 10 2018 16:37:28 GMT+00:00 ,
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
* `Id` unique user-visible file name, primary key
* `CreatedAt` timestamp when the record was created
* `UpdatedAt` timestamp of when th upload has cmpleted or failed
* `User` id of the user who uploaded this file.
* `Location` actual location of the file on the server.
* `MimeType` file content type as a [Mime](https://en.wikipedia.org/wiki/MIME) string.
* `Size` size of the file in bytes. Could be 0 if upload has not completed yet.
* `UseCount` count of messages referencing this file.
* `Status` upload status: 0 pending, 1 completed, -1 failed.

Indexes:
 * `Id` file name, primary key
 * `User` index
 * `UseCount` index

Sample:
```js
{
  "CreatedAt": Sun Jun 10 2018 16:38:45 GMT+00:00 ,
  "Id":  "sFmjlQ_kA6A" ,
  "Location":  "uploads/sFmjlQ_kA6A" ,
  "MimeType":  "image/jpeg" ,
  "Size": 54961090 ,
  "UseCount": 3,
  "Status": 1 ,
  "UpdatedAt": Sun Jun 10 2018 16:38:45 GMT+00:00 ,
  "User":  "7j-RR1V7O3Y"
}
```
