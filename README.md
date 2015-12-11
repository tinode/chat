# Tinode Instant Messaging Server

Instant messaging server. Backend in pure [Go](http://golang.org) ([Affero GPL 3.0](http://www.gnu.org/licenses/agpl-3.0.en.html)), client-side binding in Java for Android and Javascript ([Apache 2.0](http://www.apache.org/licenses/LICENSE-2.0)), persistent storage [RethinkDB](http://rethinkdb.com/), JSON over websocket (long polling is also available). No UI components other than demo apps. Tinode is meant as a replacement for XMPP.

Version 0.5. This is alpha-quality software. Bugs should be expected. Follow [instructions](INSTALL.md) to install and run.

A demo is (usually) available at [http://api.tinode.co/x/samples/chatdemo.html](http://api.tinode.co/x/samples/chatdemo.html). Login as one of `alice`, `bob`, `carol`, `dave`, `frank`. Password is `<login>123`, e.g. login for `alice` is `alice123`.


## Why?

[XMPP](http://xmpp.org/) is a mature specification with support for a very broad spectrum of use cases developed long before mobile became important. As a result most (all?) known XMPP servers are difficult to adapt for the most common use case of a few people messaging each other from mobile devices. Tinode is an attempt to build a modern replacement for XMPP/Jabber focused on a narrow use case of instant messaging between humans with emphasis on mobile communication.

## Features

### Supported

* One on one messaging
* Group chats:
 * Groups (topics) with up to 32 members where every member's access permissions are managed individually
 * Groups with unlimited number of members with bearer token access control
* Topic access control with separate permissions for various actions (reading, writing, sharing, etc)
* Server-generated presence notifications for people and topics
* Persistent message store
* Android Java bindings (dependencies: [jackson](https://github.com/FasterXML/jackson), [android-websockets](https://github.com/codebutler/android-websockets))
* Javascript bindings with no dependencies
* Websocket & long polling transport
* JSON wire protocol
* Server-generated message delivery status
* Basic support for client-side message caching
* Blocking users on the server

### Planned

* iOS client bindings
* Mobile push notification hooks
* Clustering
* Federation
* Multitenancy
* Different levels of message persistence (from strict persistence to store until delivered to purely ephemeral messaging)
* Support for binary wire protocol
* User search/discovery
* Anonymous clients
* Support for other SQL and NoSQL backends
* Pluggable authentication

## How it works?

Tinode is an IM router and a store. Conceptually it loosely follows a publish-subscribe model.

Server connects sessions, users, and topics. Session is a network connection between a client application and the server. User represents a human being who connects to the server with a session. Topic is a named communication channel which routes content between sessions.

Users and topics are assigned unique IDs. User ID is a string with 'usr' prefix followed by base64-URL-encoded pseudo-random 64-bit number, e.g. `usr2il9suCbuko`. Topic IDs are described below.

Clients such as mobile or web applications create sessions by connecting to the server over a websocket or through long polling. Client authentication is optional (*anonymous clients are technically supported but may not fully work as expected yet*). Client authenticates the session by sending a `{login}` packet. Only basic authentication with user name and password is currently supported. Multiple simultaneous sessions may be established by the same user. Logging out is not supported.

Once the session is established, the user can start interacting with other users through topics. The following
topic types are available:

* `me` is a topic for managing one's profile, receiving invites and requests for approval; 'me' topic exists for every user.
* Peer to peer topic is a communication channel strictly between two users. It's named as a 'p2p' prefix followed by a base64-URL-encoded numeric part of user IDs concatenated in ascending order, e.g. `p2p2il9suCbukqm4P2KFOv9-w`. Peer to peer topics must be explicitly created.
* Group topic is a channel for multi-user communication. It's named as 'grp' followed by 12 pseudo-random characters, i.e. `grp1XUtEhjv6HND`. Group topics must be explicitly created.

Session joins a topic by sending a `{sub}` packet. Packet `{sub}` serves three functions: creating a new topic, subscribing user to a topic, and attaching session to a topic. See {sub} section below for details.

Once the session has joined the topic, the user may start generating content by sending `{pub}` packets. The content is delivered to other attached sessions as `{data}` packets.

The user may query or update topic metadata by sending `{get}` and `{set}` packets.

Changes to topic metadata, such as changes in topic description, or when other users join or leave the topic, is reported to live sessions with `{pres}` (presence) packet.

When user's `me` topic comes online (i.e. an authenticated session attaches to `me` topic), a `{pres}` packet is sent to `me` topics of all other users, who have peer to peer subscriptions with the first user.

## General considerations

Timestamps are always represented as [RFC 3339](http://tools.ietf.org/html/rfc3339)-formatted string with precision to milliseconds and timezone always set to UTC, e.g. `"2015-10-06T18:07:29.841Z"`.

Whenever base64 encoding is mentioned, it means base64 URL encoding with padding characters stripped, see [RFC 4648](http://tools.ietf.org/html/rfc4648).

Server-issued message IDs are base-10 sequential numbers starting at 1. They guaranteed to be unique per topic. Client-assigned message IDs are strings generated by the client. Client should make them unique at least per session. The client-assigned IDs  are not interpreted by the server, they are returned to the client as is.

## Connecting to the server

Client establishes a connection to the server over HTTP. Server offers two end points:
 * `/v0/channels` for websocket connections
 * `/v0/v0/channels/lp` for long polling

`v0` denotes API version (currently zero). Every HTTP request must include API key in the request. It may be included in the URL as `...?apikey=<YOUR_API_KEY>`, in the request body, or in an HTTP header `X-Tinode-APIKey`.

Once the connection is opened, the server sends a `{ctrl}` message. The `params` field of response contains server's protocol version:
`"params":{"ver":"0.5"}`. `params` may include other values.

### Websocket

Messages are sent in text frames, one message per frame. Binary frames are reserved for future use. Server allows connections with any value in the `Origin` header.

### Long polling

Long polling works over `HTTP POST` (preferred) or `GET`. In response to client's very first request server sends a `{ctrl}` message containing `sid` (session ID) in `params`. Long polling client must include `sid` in every subsequent request either in URL or in request body.

Server allows connections from all origins, i.e. `Access-Control-Allow-Origin: *`

## Messages

A message is a logically associated set of data. Messages are passed as JSON-formatted text.

All client to server messages may have an optional `id` field. It's set by the client as means to receive an aknowledgement from the server that the message was received and processed. The `id` is expected to be a session-unique string but it can be any string. The server does not attempt to interpret it other than to check JSON validity. It's returned unchanged by the server when it replies to client messages.

For brevity the notation below omits double quotes around field names as well as outer curly brackets.

For messages that update application-defined data, such as `{set}` `private` or `public` fields, in case server-side
data needs to be cleared, use a string with a single Unicode DEL character "&#x2421;" `"\u2421"`.

### Client to server messages

#### `{acc}`

Message `{acc}` is used for creating users or updating authentication credentials. To create a new user set
`acc.user` to string "new". Either authenticated or anonymous session can send an `{acc}` message to create a new user.
To update credentials leave `acc.user` unset.

```js
acc: {
  id: "1a2b3", // string, client-provided message id, optional
  user: "new", // string, "new" to create a new user, default: current user, optional
  auth: [   // array of authentication schemes to add, update or delete
    {
      scheme: "basic", // requested authentication scheme for this account, required;
                       // only "basic" (default) is currently supported. The current
                       // basic scheme does not allow changes to username.
      secret: "username:password" // string, secret for the chosen authentication
                      // scheme; to delete a scheme use string with a single DEL
                      // Unicode character "\u2421"; required
    }
  ],
  init: {  // object, user initialization data closely matching that of table
           // initialization; optional
    defacs: {
      auth: "RWS", // string, default access mode for peer to peer conversations
                   // between this user and other authenticated users
      anon: "X"  // string, default access mode for peer to peer conversations
                 // between this user and anonymous (un-authenticated) users
    }, // Default access mode for user's peer to peer topics
    public: { ... }, // application-defined payload to describe user,
                // available to everyone
    private: { ... } // private application-defined payload available only to user
                // through 'me' topic
  }
}
```

Server responds with a `{ctrl}` message with `ctrl.params` containing details of the new user. If `init.acs` is missing,
server will assign server-default access values.

#### `{login}`

Login is used to authenticate the current session.

```js
login: {
  id: "1a2b3",     // string, client-provided message id, optional
  scheme: "basic", // string, authentication scheme, optional; only "basic" (default)
                   // is currently supported
  secret: "username:password", // string, secret for the chosen authentication
                               //  scheme, required
  ua: "JS/1.0 (Windows 10)" // string, user agent identifying client software,
                   // optional
}
```
Basic authentication scheme expects `secret` to be a string composed of a user name followed by a colon `:` followed by a plan text password.

Basic is the only currently supported authentication scheme. _Authentication scheme `none` is planned to support anonymous users in the future._

Server responds to a `{login}` packet with a `{ctrl}` message. The `params` of {ctrl} message contains id of the logged in user as `params:{uid:"..."}`.

The user agent `ua` is expected to follow [RFC 7231 section 5.5.3](http://tools.ietf.org/html/rfc7231#section-5.5.3) recommendation.

#### `{sub}`

The `{sub}` packet serves three functions:
 * creating a topic
 * subscribing user to a topic
 * attaching session to a topic

User creates a new group topic by sending `{sub}` packet with the `topic` field set to `new`. Server will create a topic and respond back to session with the name of the newly created topic.

User creates a new peer to peer topic by sending `{sub}` packet with `topic` set to peer's user ID.

The user is always subscribed to and the sessions is attached to the newly created topic.

If the user had no relationship with the topic, sending `{sub}` packet creates it. Subscribing means to establish a relationship between session's user and the topic when no relationship existed in the past.

Joining (attaching to) a topic means for the session to start consuming content from the topic. Server automatically differentiates between subscribing and joining/attaching based on context: if the user had no prior relationship with the topic, the server subscribes the user then attaches the current session to the topic. If relationship existed, the server only attaches the session to the topic. When subscribing, the server checks user's access permissions against topic's access control list. It may grant immediate access, deny access, may generate a request for approval from topic managers.

Server replies to the `{sub}` with a `{ctrl}`.

The `{sub}` message may include a `get` and `browse` fields which mirror `what` and `browse` fields of a {get} message. If included, server will treat them as a subsequent `{get}` message on the same topic. In that case the reply may also include `{meta}` and `{data}` messages.


```js
sub: {
  id: "1a2b3",  // string, client-provided message id, optional
  topic: "me",   // topic to be subscribed or attached to

  // Object with topic initialization data, new topics & new
  // subscriptions only, mirrors {set info}
  init: {
    defacs: {
      auth: "RWS", // string, default access for new authenticated subscribers
      anon: "X"    // string, default access for new anonymous (un-authenticated)
                   // subscribers
    }, // Default access mode for the new topic
    public: { ... }, // application-defined payload to describe topic
    private: { ... } // per-user private application-defined content
  }, // object, optional

  // Subscription parameters, mirrors {set sub}; sub.user must
  // not be provided
  sub: {
    mode: "RWS", // string, requested access mode, optional;
                 // default: server-defined
    info: { ... }  // application-defined payload to pass to the topic manager
  }, // object, optional

  // Metadata to request from the topic; space-separated list, valid strings
  // are "info", "sub", "data"; default: request nothing; unknown strings are
  // ignored; see {get  what} for details
  get: "info sub data", // string, optional

  // Optional parameters for get: "data", see {get what="data"} for details
  browse: {
    since: 123, // integer, load messages with server-issued IDs greater or equal
				 // to this (inclusive/closed), optional
    before: 321, // integer, load messages with server-issed sequential IDs less
				  // than this (exclusive/open), optional
    limit: 20, // integer, limit the number of returned objects,
               // default: 32, optional
  } // object, optional
}
```

#### `{leave}`

This is a counterpart to `{sub}` message. It also serves two functions:
* leaving the topic without unsubscribing (`unsub=false`)
* unsubscribing (`unsub=true`)

Server responds to `{leave}` with a `{ctrl}` packet. Leaving without unsubscribing affects just the current session. Leaving with unsubscribing will affect all user's sessions.

```js
leave: {
  id: "1a2b3",  // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND",   // string, topic to leave, unsubscribe, or
                              // delete, required
  unsub: true // boolean, leave and unsubscribe, optional, default: false
```

#### `{pub}`

The message is used to distribute content to topic subscribers.

```js
pub: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic to publish to, required
  noecho: true, // boolean, suppress echo (see below), optional
  content: { ... }  // object, application-defined content to publish
               // to topic subscribers, required
}
```

Topic subscribers receive the `content` in the `{data}` message. By default the originating session gets a copy of `{data}` like any other session currently attached to the topic. If for some reason the originating session does not want to receive the copy of the data it just published, set `noecho` to `true`.

#### `{get}`

Query topic for metadata, such as description or a list of subscribers, or query message history.

```js
get: {
  id: "1a2b3", // string, client-provided message id, optional
  what: "sub info data", // string, space-separated list of parameters to query;
                        // unknown strings are ignored; required

  // Optional parameters for {get what="data"}
  browse: {
    since: 123, // integer, load messages with server-issued IDs greater or equal
				 // to this (inclusive/closed), optional
    before: 321, // integer, load messages with server-issed sequential IDs less
				  // than this (exclusive/open), optional
    limit: 20, // integer, limit the number of returned objects, default: 32,
               // optional
  } // object, what=data query parameters
}
```

* `{get what="info"}`

Query topic description. Server responds with a `{meta}` message containing requested data. See `{meta}` for details.

* `{get what="sub"}`

Get a list of subscribers. Server responds with a `{meta}` message containing a list of subscribers. See `{meta}` for details.
For `me` topic the request returns a list of user's subscriptions.

* `{get what="data"}`

Query message history. Server sends `{data}` messages matching parameters provided in the `browse` field of the query.
The `id` field of the data messages is not provided as it's common for data messages.


#### `{set}`

Update topic metadata, delete messages or topic.

```js
set: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic to publish to, required
  what: "sub info", // string, space separated list of data to update,
                        // unknown strings are ignored
  info: {
    defacs: { // new default access mode
      auth: "RWP",  // access permissions for authenticated users
      anon: "X" // access permissions for anonymous users
    },
    public: { ... }, // application-defined payload to describe topic
    private: { ... } // per-user private application-defined content
  }, // object, payload for what == "info"
  sub: {
    user: "usr2il9suCbuko", // string, user affected by this request;
                            // default (empty) means current user
    mode: "RWP", // string, access mode change, either given or requested
                 // depending on context
    info: { ... } // object, application-defined payload to pass to
                  // the invited user or to the topic manager in {data}
                  // message on 'me' topic
  } // object, payload for what == "sub"
}
```

#### `{del}`

Delete messages or topic.

```js
del: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic affect, required
  what: "msg", // string, either "topic" or "msg"; what to delete - the
               // entire topic or just the messages, optional, default: "msg"
  hard: false, // boolean, request to delete messages for all users, default: false
  before: 123, // integer, delete messages with server-issued ID lower or equal
               // to this value (inclusive of the value itself), optional,
               // default: delete all messages
}
```

User can soft-delete and hard-delete messages `what="msg"`. Soft-deleting messages hides them from the requesting user but does not delete them from storage. No special permission is needed to soft-delete messages `hard=false`. Hard-deleting messages deletes them from storage affecting all users. `D` permission is needed to hard-delete messages.
Deleting a topic `what="topic"` deletes the topic including all subscriptions, and all messages. The `hard` parameter has no effect on topic deletion: all topic deletions are hard-deletions. Only the owner can delete a topic. The greatest deleted ID is reported back in the `clear` of the `{meta}` message.

#### `{note}`

Client-generated notification for other clients currently attached to the topic. Notes are "fire and forget": not stored to disk and not acknowledged by the server. Messages deemed invalid are silently dropped. They are intended for ephemeral notifications, such as typing notifications and delivery receipts.

```js
note: {
  topic: "grp1XUtEhjv6HND", // string, topic to notify, required
  what: "kp", // string, one of "kp" (key press), "read" (read notification),
              // "rcpt" (received notification), any other string will cause
              // message to be silently dropped, required
  seq: 123, // integer, ID of the message being acknowledged, required for
            // rcpt & read
}
```

The following actions are currently recognized:
 * kp: key press, i.e. a typing notification. The client should use it to indicate that the user is composing a new message.
 * recv: a `{data}` message is received by the client software but not yet seen by user.
 * read: a `{data}` message is seen by the user. It implies `recv`.

### Server to client messages

Messages to a session generated in response to a specific request contain an `id` field equal to the id of the
originating message. The `id` is not interpreted by the server.

Most server to client messages have a `ts` field which is a timestamp when the message was generated by the server.

#### `{data}`

Content published in the topic. These messages are the only messages persisted in database; `{data}` messages are
broadcast to all topic subscribers with an `R` permission.

```js
data: {
  topic: "grp1XUtEhjv6HND", // string, topic which distributed this message,
                            // always present
  from: "usr2il9suCbuko", // string, id of the user who published the
                          // message; could be missing if the message was
                          // generated by the server
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
  seq: 123, // integer, server-issued sequential ID
  content: { ... } // object, application-defined content exactly as published
              // by the user in the {pub} message
}
```

Data messages have a `seq` field which holds a sequential numeric ID generated by the server. The IDs are guaranteed to be unique within a topic. IDs start from 1 and sequentially increment with every successful `{pub}` message received by the topic.

#### `{ctrl}`

Generic response indicating an error or a success condition. The message is sent to the originating session.

```js
ctrl: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic name, if this is a response in context
                            // of a topic, optional
  code: 200, // integer, code indicating success or failure of the request, follows
             // the HTTP status codes model, always present
  text: "OK", // string, text with more details about the result, always present
  params: { ... }, // object, generic response parameters, context-dependent,
                   // optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
}
```

#### `{meta}`

Information about topic metadata or subscribers, sent in response to `{set}` or `{sub}` message to the originating session.

```js
meta: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic name, if this is a response in
                            // context of a topic, optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
	info: {
    created: "2015-10-24T10:26:09.716Z",
    updated: "2015-10-24T10:26:09.716Z",
    defacs: { // topic's default access permissions; present only if the current
              //user has 'S' permission
      auth: "RWP", // default access for authenticated users
      anon: "X" // default access for anonymous users
    },
    acs: {  // user's actual access permissions
      "want":"RWP", // string, requested access permission
      "given":"RWP" // string, granted access permission
    },
    seq: 123, // integer, server-issued id of the last {data} message
    read: 112, // integer, ID of the message user claims through {note} message
              // to have read, optional
    recv: 115, // integer, like 'read', but received, optional
    clear: 12, // integer, in case some messages were deleted, the greatest ID
               // of a deleted message, optional
    public: { ... }, // application-defined data that's available to all topic
                     // subscribers
    private: { ...} // application-deinfed data that's available to the current
                    // user only
  }, // object, topic description, optional
	sub:  [ // array of objects, topic subscribers or user's subscriptions, optional
    {
      user: "usr2il9suCbuko", // string, ID of the user this subscription
                            // describes, absent when querying 'me'
	    online: "on", // string, current online status of the user with respect to
					// the topic, i.e. if the user is listening to messages
      updated: "2015-10-24T10:26:09.716Z", // timestamp of the last change in the
                                           // subscription, present only for
                                           // requester's own subscriptions
      mode: "RWPSDO",  // string, user's access permission, equal to bitwise
                       // AND (info.given & info.want)
      read: 112, // integer, ID of the message user claims through {note} message
                 // to have read, optional
      recv: 315, // integer, like 'read', but received, optional
      clear: 12, // integer, in case some messages were deleted, the greatest ID
                 // of a deleted message, optional
      private: { ... } // application-defined user's 'private' object, present only
                       // for the requester's own subscriptions

      // The following fields are present only when querying 'me' topic

      topic: "grp1XUtEhjv6HND", // string, topic this subscription describes
      seq: 321, // integer, server-issued id of the last {data} message

      // The following fields are present only when querying 'me' topic and the
      // topic described is a P2P topic

      with: "usr2il9suCbuko", // string, if this is a P2P topic, peer's ID, optional
      public: { ... }, // application-defined user's 'public' object, present for
                      // P2P topics only
      seen: { // object, if this is a P2P topic, info on when the peer was last
              //online
        when: "2015-10-24T10:26:09.716Z", // timestamp
        ua: "Tinode/1.0 (Android 5.1)" // string, user agent of peer's client
      }
    },
    ...
  ]
}
```

#### `{pres}`

Tinode uses `{pres}` message to inform users of important events. The following events are tracked by the server and will generate `{pres}` messages provided user has appropriate access permissions:

1. A user joins `me`. User receives presence notifications for each of his/her subscriptions: `{pres topic="me" src="<user ID or topic ID>" what="on", ua="..."}`. Only online status is reported.
2. A user came online or went offline. The user triggers this event by joining/leaving the `me` topic. The message is sent to all users who have P2P topics with the first user. Users receive this event on the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"on"` or `"off"`: `{pres topic="me" src="<user ID>" what="on|off" ua="..."}`.
3. User's `public` is updated. The event is sent to all users who have P2P topics with the first user. Users receive `{pres topic="me" src="<user ID>" what="upd"}`.
4. User joins/leaves a topic. This event is sent to other users who currently joined the topic: `{pres topic="<topic name>" src="<user ID>" what="on|off"}`.
5. A group topic is activated/deactivated. Topic becomes active when at least one user joins it. The topic becomes inactive when all users leave it (possibly after some delay). The event is sent to all topic subscribers. They will receive it on their `me` topics: `{pres topic="me" src="<topic name>" what="on|off"}`.
6. A message is published in a topic. The event is sent to users who have subscribed to the topic but currently not joined `{pres topic="me" src="<topic name>" what="msg"}`.
7. Topic's `public` is updated. The event is sent to all topic subscribers. Topic's subscribers receive `{pres topic="me" src="<topic name>" what="upd"}`.
8. User is has multiple sessions attached to 'me', sessions have different _user agents_. If the current most recently active session has a different _user agent_ than the previous most recent session (the most recently active session is the session which was the last to receive any message from the client) an event is sent to all users who have P2P topics with the first user. Users receive it on the `me` topic, `src` field contains user ID `src: "usr2il9suCbuko"`, `what` contains `"ua"`: `{pres topic="me" src="<user ID>" what="ua" ua="<new user agent>"}`.
9. User sent a `{note}` message indicating that some or all of the messages in the topic have been received or read. The event is sent to user's other sessions (not the one that originated the `{note}` message): `{pres topic="me" src="<topic name>" what="recv|read" seq=123}`.
10. User sent a `{del hard=false}` message soft-deleting some messages. Like above, the event is sent to user's other sessions: `{pres topic="me" src="<topic name>" what="del" seq=123}`.
11. Some or all messages in the topic were hard-deleted by the topic manager. The event is sent to all topic subscribers, joined (excluding the originating session) and not joined:
 a. joined: `{pres topic="<topic name>" src="<user id>" what="del" seq=123}`.
 b. not joined: `{pres topic="me" src="<topic name>" what="del" seq=123}`.

```js
pres: {
  topic: "grp1XUtEhjv6HND", // string, topic affected by the change, always present
  src: "usr2il9suCbuko", // string, user or topic affected by the change, always present
  what: "on", // string, what's changed, always present
  seq: 123, // integer, if "what" is "msg", server-issued ID of the message,
              // optional
  ua: "Tinode/1.0 (Android 2.2)" // string, if "what" is "on" or "ua", a
        // User Agent string identifying client software, optional
}
```

The `{pres}` messages are purely transient. No attempt is made to store a `{pres}` message or deliver it later if the destination is unavailable.

Timestamp is not present in `{pres}` messages.

#### `{info}`

Forwarded client-generated notification `{note}`. Server guarantees that the message complies with this specification and that content of `topic` and `from` fields are valid. The other content is copied from the `{note}` message verbatim.

```js
info: {
  topic: "grp1XUtEhjv6HND", // string, topic affected, always present
  from: "usr2il9suCbuko", // string, id of the user who published the
                          // message, always present
  what: "read", // string, one of "kp", "recv", "read", see client-side {note},
                // always present
  seq: 123, // integer, ID of the message that client has acknowledged,
            // guaranteed 0 < read <= recv <= {ctrl.info.seq}; present for rcpt &
            // read
}
```


## Users

User is meant to represent a person, an end-user: producer and consumer of messages.

There are two types of users: authenticated and anonymous. When a connection is first established, the client application can only send either an `{acc}` or a `{login}` message. Sending a `{login}` message will authenticate user or allow him to continue as an anonymous. _Anonymous users are not supported as of the time of this writing._

Each user is assigned a unique ID. The IDs are composed as `usr` followed by base64-encoded 64-bit numeric value, e.g. `usr2il9suCbuko`. Users also have the following properties:

* created: timestamp when the user record was created
* updated: timestamp of when user's `public` was last updated
* username: unique string used in `basic` authentication; username is not accessible to other users
* defacs: object describing user's default access mode for peer to peer conversations with authenticated and anonymous users; see [Access control][] for details
 * auth: default access mode for authenticated users
 * anon: default access for anonymous users
* public: an application-defined object that describes the user. Anyone who can query user for `public` data.
* private: an application-defined object that is unique to the current user and accessible only by the user.

A user may maintain multiple simultaneous connections (sessions) with the server. Each session is tagged with a client-provided User Agent string intended to differentiate client software.

Logging out is not supported by design. If an application needs to change the user, it should open a new connection and authenticate it with the new user credentials.

## Access control

Access control manages user's access to topics through access control lists (ACLs) or bearer tokens (_bearer tokens are not implemented as of version 0.4_).

Access control is mostly usable for group topics. Its usability for `me` and P2P topics is limited to managing presence notifications and banning uses from initiating or continuing P2P conversations.

User's access to a topic is defined by two sets of permissions: user's desired permissions "want", and permissions granted to user by topic's manager(s) "given". Each permission is represented by a bit in a bitmap. It can be either present or absent. The actual access is determined as a bitwise AND of wanted and given permissions. The permissions are communicated in messages as a set of ASCII characters, where presence of a character means a set permission bit:

* No access: `N` is not a permission per se but an indicator that permissions are explicitly cleared/not set. It usually indicates that the default permissions should *not* be applied.
* Read: `R`, permission to subscribe to topic and receive `{data}` packets
* Write: `W`, permission to `{pub}` to topic
* Presence: `P`, permission to receive presence updates `{pres}`
* Sharing: `S`, permission to invite other people to join the topic and to approve requests to join; a user with such permission is topic's manager
* Delete: `D`, permission to hard-delete messages; only owners can completely delete topics
* Owner: `O`, user is the topic owner; topic may have a single owner only
* Banned: `X`, user has no access, requests to share/gain access are silently ignored

Topic's default access is established at the topic creation time by `{sub.init.defacs}` and can be subsequently modified by `{set}` messages. Default access is defined for two categories of users: authenticated and anonymous. This value is applied as a default "given" permission to all new subscriptions.

Client may replace explicit permissions in `{sub}` and `{set}` messages with an empty string to tell Tinode to use default permissions. If client specifies no default access permissions at topic creation time, authenticated users will receive a `RWP` permission, anonymous users will receive and empty permission which means every subscription request must be explicitly approved by the topic manager.

Access permissions can be assigned on a per-user basis by `{set}` messages.

## Topics

Topic is a named communication channel for one or more people. Topics have persistent properties. These following topic properties can be queried by `{get what="info"}` message.

Topic properties independent of the user making the query:
* created: timestamp of topic creation time
* updated: timestamp of when topic's `public` or `private` was last updated
* defacs: object describing topic's default access mode for authenticated and anonymous users; see [Access control][] for details
 * auth: default access mode for authenticated users
 * anon: default access for anonymous users
* seq: integer server-issued sequential ID of the latest `{data}` message sent through the topic
* public: an application-defined object that describes the topic. Anyone who can subscribe to topic can receive topic's `public` data.

User-dependent topic properties:
* acs: object describing given user's current access permissions; see [Access control][] for details
 * want: access permission requested by this user
 * given: access permissions given to this user
* private: an application-defined object that is unique to the current user.

Topic usually have subscribers. One the the subscribers may be designated as topic owner (`O` access permission) with full access permissions. The list of subscribers can be queries with a `{get what="sub"}` message. The list of subscribers is returned in a `sub` section of a `{meta}` message.

### `me` topic

Topic `me` is automatically created for every user at the account creation time. It serves as means for account updates, receiving presence notification from people and topics of interest, invites to join topics, requests to approve subscription for topics where this user is a manager (has `S` permission). Topic `me` has no owner. The topic cannot be deleted or unsubscribed from. One can leave the topic which will stop all relevant communication and indicate that the user is offline (although the user may still be logged in and may continue to use other topics).

Joining or leaving `me` generates a `{pres}` presence update sent to all users who have peer to peer topics with the given user and `P` permissions set.

Topic `me` is read-only. `{pub}` messages to `me` are rejected.

The `{data}` message represents invites and requests to confirm a subscription. The `from` field of the message contains ID of the user who originated the request, for instance, the user who asked current user to join a topic or the user who requested an approval for subscription. The `content` field of the message contains the following information:
* act: request action as string; possible actions are:
 * "info" to notify the user that user's request to subscribe was approved; in case of peer to peer topics this could be a notification that the peer has subscribed to the topic
 * "join" is an invitation to subscribe to a topic
 * "appr" is a request to approve a subscription
* topic: the name of the topic, in case of an invite the current user is invited to this topic; in case of a request to approve, another user wants to subscribe to this topic where the current user is a manager (has `S` permission)
* user: user ID as a string of the user who is the target of this request. In case of an invite this is the ID of the current user; in case of an approval request this is the ID of the user who is being subscribed.
* acs: object describing access permissions of the subscription, see [Access control][] for details
* info: object with a free-form payload. It's passed unchanged from the originating `{sub}` or `{set}` message.

Message `{get what="info"}` to `me` is automatically replied with a `{meta}` message containing `info` section with the topic parameters (see intro to [Topics][] section). The `public` parameter of `me` topic is associated with the user. Changing it changes `public` not just for the `me` topic, but also everywhere where user's public is shown, such as 'public' of all user's peer to peer topics.

Message `{get what="sub"}` to `me` is different from any other topic as it returns the list of topics that the current user is subscribed to as opposite to the expected user's subscription to `me`.
* seq: server-issued numeric id of the last message in the topic
* with: for P2P subscriptions, id of the peer
* seen: for P2P subscriptions, timestamp of user's last presence and User Agent string are reported
 * when: timestamp when the user was last online
 * ua: user agent string of the user's client software last used

Message `{get what="data"}` to `me` queries the history of invites/notifications. It's handled the same way as to any other topic.

### Peer to Peer Topics

Peer to peer (P2P) topics represent communication channels between strictly two users. The name of the topic is `p2p` followed by base64 URL-encoded concatenation of two 8-byte IDs of participating users. Lower user ID comes first. For example `p2pOj0B3-gSBSshT8s5XBE2xw` is a topic for users `usrOj0B3-gSBSs` and `usrIU_LOVwRNsc`. The P2P topic has no owner.

A P2P topic is created by one user subscribing to topic with the name equal to the ID of the other user. For instance, user `usrOj0B3-gSBSs` can establish a P2P topic with user `usrIU_LOVwRNsc` by sending a `{sub topic="usrIU_LOVwRNsc"}`. Tinode will respond with a `{ctrl}` packet with the name of the newly created topic, `p2pOj0B3-gSBSshT8s5XBE2xw` in this example. The other user will receive a `{data}` message on `me` topic with either a request to confirm the subscription or a notification of a successful subscription, depending on user's default permissions.

The 'public' parameter of P2P topics is user-dependent. For instance a P2P topic between users A and B would show user A's 'public' to user B and vice versa. If a user updates 'public', all user's P2P topics will automatically update 'public' too.

The 'private' parameter of a P2P topic is defined by each participant individually as with any other topic type.

### Group Topics

Group topics represent communication channels between multiple users. The name of a group topic is `grp` followed by a string of characters from base64 URL-encoding set. No other assumptions can be made about internal structure or length of the group name.

A group topic is created by sending a `{sub}` message with the topic field set to "new". Tinode will respond with a `{ctrl}` message with the name of the newly created topic, i.e. `{sub topic="new"}` is replied with `{ctrl topic="grpmiKBkQVXnm3P"}`. The user who created the topic becomes topic owner. Ownership can be transferred to another user with a `{set}` message but at least one user must remain the topic owner.

A user joining or leaving the topic generates a `{pres}` message to all other users who are currently in the joined state with the topic.


## Using Server-Issued Message IDs

Tinode provides basic support for client-side caching of `{data}` messages in the form of server-issued message IDs. The client may request the last message id from the topic by issuing a `{get what="info"}` message. If the returned ID is greater than the ID of the latest received message, the client knows that the topic has unread messages and their count. The client may fetch these messages using `{get what="data"}` message. The client may also paginate history retrieval by using message IDs.

## User Agent and Presence Notifications

A user is reported as being online when one or more of user's sessions are attached to the `me` topic. Client-side software identifies itself to the server using `ua` (user agent) field of the `{login}` message. The _user agent_ is published in `{meta}` and `{pres}` messages in the following way:

 * When user's first session attaches to `me`, the _user agent_ from that session is broadcast in the `{pres what="on" ua="..."}` message.
 * When multiple user sessions are attached to `me`, the _user agent_ of the session where the most recent action has happened is reported in `{pres what="ua" ua="..."}`; the 'action' in this context means any message sent by the client. To avoid potentially excessive traffic, user agent changes are broadcast no more frequently than once a minute.
 * When user's last session detaches from `me`, the _user agent_ from that session is recorded together with the timestamp; the user agent is broadcast in the `{pres what="off"  ua="..."}` message and subsequently reported as the last online timestamp and user agent.

An empty `ua=""` _user agent_ is not reported. I.e. if user attaches to `me` with non-empry _user agent_ then does so with an empty one, the change is not reported. An empty _user agent_ may be disallowed in the future.
