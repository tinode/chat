# Tinode Instant Messaging Server

**This documentation covers the next 0.4 release of Tinode. ETA mid-November 2015.**  

Instant messaging server. Backend in pure [Go](http://golang.org) ([Affero GPL 3.0](http://www.gnu.org/licenses/agpl-3.0.en.html)), client-side binding in Java for Android and Javascript ([Apache 2.0](http://www.apache.org/licenses/LICENSE-2.0)), persistent storage [RethinkDB](http://rethinkdb.com/), JSON over websocket (long polling is also available). No UI components other than demo apps. Tinode is meant as a replacement for XMPP.

This is alpha-quality software. Bugs should be expected. Version 0.4

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
* Support for client-side content caching
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

## Connecting to the server

Client establishes a connection to the server over HTTP. Server offers two end points:
 * `/v0/channels` for websocket connections
 * `/v0/v0/channels/lp` for long polling

`v0` denotes API version (currently zero). Every HTTP request must include API key in the request. It may be included in the URL as `...?apikey=<YOUR_API_KEY>`, in the request body, or in an HTTP header `X-Tinode-APIKey`.

Once the connection is opened, the server sends a `{ctrl}` message. The `params` field of response contains server's protocol version:
`"params":{"ver":"0.4"}`. `params` may include other values.

### Websocket

Messages are sent in text frames, one message per frame. Binary frames are reserved for future use. Server allows connections with any value in the `Origin` header.

### Long polling

Long polling works over `HTTP POST` (preferred) or `GET`. In response to client's veru rirst request server sends a `{ctrl}` message containing `sid` (session ID) in `params`. Long polling client must include `sid` in every subsequent request either in URL or in request body.

Server allows connections from all origins, i.e. `Access-Control-Allow-Origin: *`

## Messages

A message is a logically associated set of data. Messages are passed as JSON-formatted text.

All client to server messages may have an optional `id` field. It's set by the client as means to receive an aknowledgement from the server that the message was received and processed. The `id` is expected to be a session-unique string but it can be any string. The server does not attempt to interpret it other than to check JSON validity. It's returned unchanged by the server when it replies to client messages.

For brievity the notation below omits double quotes around field names as well as outer curly brackets.

For messages that update application-defined data, such as `{set}` `private` or `public` fields, in case server-side
data needs to be cleared, use a string with a single Unicode DEL character "&#x2421;" `"\u2421"`.  

### Client to server messages

#### `{acc}`

Message `{acc}` is used for creating users or updating authentication credentials. To create a new user set
`acc.user` to string "new". Either authenticated or anonymous session can send an `{acc}` message to create a new user.
To update credentials leave `acc.user` unset.

```js
acc: {
  id: "1a2b3",     // string, client-provided message id, optional
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
    public: {}, // Free-form application-dependent payload to describe user,
                // available to everyone
    private: {} // Private application-dependent payload available to user only
                // through `me` topic
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
  expireIn: "24h", // string, login expiration time in Go's time.ParseDuration
                   //  format, see below, optional
  tag: "some string" // string, client instance ID; tag is used to support caching,
                     // optional
}
```
Basic authentication scheme expects `secret` to be a string composed of a user name followed by a colon `:` followed by a plan text password.

[time.ParseDuration](https://golang.org/pkg/time/#ParseDuration) is used to parse `expireIn`. The recognized format is a possibly signed sequence of decimal numbers, each with optional fraction and a unit suffix, such as "300ms", "-1.5h" or "2h45m". Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".

Server responds to a `{login}` packet with a `{ctrl}` packet.

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
    public: {}, // Free-form application-dependent payload to describe topic
    private: {} // Per-subscription private application-dependent payload
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

  // Optional parameters for get: "info", see {get what="data"} for details
  browse: {
    ascnd: true, // boolean, sort in ascending order by time, otherwise
                 // descending (default), optional
    since: "2015-09-06T18:07:30.134Z", // datetime as RFC 3339-formated string,
                    // load objects newer than this (inclusive/closed), optional
    before: "2015-10-06T18:07:30.134Z", // datetime as  RFC 3339-formated string,
                    // load objects older than this (exclusive/open), optional
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
  topic: "grp1XUtEhjv6HND", // topic to publish to, required
  content: {}  // object, free-form content to publish to topic
               // subscribers, required
}
```

Topic subscribers receive the content as `{data}` message.

#### `{get}`

Query topic for metadata, such as description or a list of subscribers, or query message history.

```js
get: {
  what: "sub info data", // string, space-separated list of parameters to query;
                        // unknown strings are ignored; required
  browse: {
    ascnd: true, // boolean, sort in ascending order by time, otherwise
                 // descending (default), optional
    since: "2015-09-06T18:07:30.134Z", // datetime as RFC 3339-formated string,
                        // load objects newer than this (inclusive/closed), optional
    before: "2015-10-06T18:07:30.134Z", // datetime as  RFC 3339-formated string,
                        // load objects older than this (exclusive/open), optional
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
  info: {}, // object, payload for what == "info"
  sub: {} // object, payload for what == "sub"
}
```

#### `{del}`

Delete messages or topic.

```js
del: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic affect, required
  what: "msg", // string, either "topic" or "msg" (default); what to delete - the
               // entire topic or just the messages, optional, default: "msg"
  hard: false, // boolean, request to delete messages for all users, default: false
  before: "2015-10-06T18:07:30.134Z", // datetime as a RFC 3339-
              // formated string, delete messages older than this
              // (exclusive of the value itself), optional
}
```

No special permission is needed to soft-delete messages `hard=false`. Soft-deleting messages hides them from the
requesting user. `D` permission is needed to hard-delete messages. Only owner can delete a topic.

### Server to client messages

Messages to a session generated in response to a specific request contain an `id` field equal to the id of the
originating message. The `id` is not interpreted by the server.

Most server to client messages have a `ts` field which is a timestamp when the message was generated by the server. Timestamp is in [RFC 3339](https://tools.ietf.org/html/rfc3339) format, timezone is always UTC, precision to milliseconds.

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
  content: {} // object, content exactly as published by the user in the
              // {pub} message
}
```

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
  params: {}, // object, generic response parameters, context-dependent, optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
}
```

#### `{meta}`

Information about topic metadata or subscribers, sent in response to `{set}` or `{sub}` message to the originating session.

```js
ctrl: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic name, if this is a response in
                            // context of a topic, optional
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
    lastMsg: "2015-10-29T16:19:15.03Z", // timestamp of the last {data} message
    public: { ... }, // application-defined data that's available to all topic subscribers
    private: { ...} // application-deinfed data that's available to the current user only
  }, // object, topic description, optional
	sub:  [
    {
      user: "usr2il9suCbuko", // string, ID of the user this subscription describes
      updated: "2015-10-24T10:26:09.716Z", // timestamp of the last change in the subscription
      mode: "RWPSDO",  // string, user's access permission, equal to bitwise (info.given & info.want)
      public: { ... }, // user's 'public' object
      private: { ... } // user's 'private' object, present only for the user's own subscription
    },
    ...
  ] // array of objects, topic subscribers, optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
}
```

#### `{pres}`

Notification that topic metadata has changed: `public` was updated, someone joined or left the topic. Timestamp is not present.

```js
pres: {
  topic: "grp1XUtEhjv6HND", // string, topic affected by the change, always present
  user: "usr2il9suCbuko", // user affected by the change, present if relevant
  what: ""  // string, what's changed, always present
}
```

## Users

TODO

## Access control

Access control manages user's access to topics through access control lists (ACLs) or bearer tokens (_bearer tokens are not implemented as of version 0.4_).

Access control is mostly usable for group topics. Its usability for `me` and P2P topics is limited to managing presence notifications and banning uses from initiating or continuing P2P conversations.

User's access to a topic is defined by two sets of permissions: user's desired permissions "want", and permissions granted to user by topic's manager(s) "given". Each permission is represented by a bit in a bitmap. It can be either present or absent. The actual access is determined as a bitwise AND of wanted and given permissions. The permissions are communicated in messages as a set of ASCII characters, where presence of a character means a set permission bit:

* No access: `N` is not a permission per se but an indicator that permissions are explicitly cleared/not set. It usually means that the default permissions should *not* be applied.
* Read: `R`, permission to subscribe to topic and receive `{data}` packets
* Write: `W`, permission to `{pub}` to topic
* Presence: `P`, permission to receive presence updates `{pres}`
* Sharing: `S`, permission to invite other people to join the topic and to approve requests to join; a user with such permission is topic's manager
* Delete: `D`, permission to hard-delete messages; only owners can completely delete topics
* Owner: `O`, user is the topic owner; topic may have a single owner only
* Banned: `X`, user has no access, requests to share/gain access/`{sub}` are silently ignored

Topic's default access is established at the topic creation time by `{sub.init.defacs}` and can be subsequently modified by `{set}` messages. Default access is defined for two categories of users: authenticated and anonymous. This value is applied as a default "given" permission to all new subscriptions.

Client may replace explicit permissions in `{sub}` and `{set}` messages with an empty string to tell Tinode to use default permissions. If client specifies no default access permissions at a topic creation time, "auth" will receive a `RWP` permission, "anon" will receive and empty permission which means every subscription request must be explicitly approved by the topic manager.

Access permissions can be assigned on a per-user basis by `{set}` messages.

## Topics

Topic is a named communication channel for one or more people. All timestamps are represented as RFC 3999-formatted string with precision to milliseconds and timezone always set to UTC, e.g. `"2015-10-06T18:07:29.841Z"`. Topics have persistent properties. These following topic properties can be queried by `{get what="info"}` message.

Topic properties independent of user making the query:
* created: timestamp of topic creation time
* updated: timestamp of when topic's `public` or `private` was last updated
* defacs: object describing topic's default access mode for authenticated and anonymous users; see [Access control][] for details
 * auth: default access mode for authenticated users
 * anon: default access for anonymous users
* lastMsg: timestamp when last `{data}` message was sent through the topic
* public: an application-defined object that describes the topic. Anyone who can subscribe to topic can receive topic's `public` data.

User-dependent topic properties:
* acs: object describing given user's current access permissions; see [Access control][] for details
 * want: access permission requested by this user
 * given: access permissions given to this user
* seen: an object describing when the topic was last accessed by the current user from any client instance. This should be useful if the client implements data caching. See [Support for Client-Side Caching][] for more details.
 * when": timestamp of the last access
 * tag: string provided by the client instance when it accessed the topic.
* seenTag: timestamp when the topic was last accessed from a session with the current client instance. See [Support for Client-Side Caching][] for more details
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

Message `{get what="sub"}` to `me` is different from any other topic as it returns the list of topics that the current user is subscribed to as opposite to the user's subscription to `me`.

Message `{get what="data"}` to `me` queries the history of invites/notifications. It's handled the same way as to any other topic.

### Peer to Peer Topics

Peer to peer (P2P) topics represent communication channels between strictly two users. The name of the topic is `p2p` followed by base64 URL-encoded concatenation of two 8-byte IDs of participating users. Lower user ID comes first. For example `p2pOj0B3-gSBSshT8s5XBE2xw` is a topic for users `usrOj0B3-gSBSs` and `usrIU_LOVwRNsc`. The P2P topic has no owner.

A P2P topic is created by one user subscribing to topic with the name equal to the ID of the other user. For instance, user `usrOj0B3-gSBSs` can establish a P2P topic with user `usrIU_LOVwRNsc` by sending a `{sub topic="usrIU_LOVwRNsc"}`. Tinode will respond with a `{ctrl}` packet with the name of the newly created topic, `p2pOj0B3-gSBSshT8s5XBE2xw` in this example. The other user will receive a `{data}` message on `me` topic with either a request to confirm the subscription or a notification of successful subscription, depending on user's default permissions.

The 'public' parameter of P2P topics is user-dependent. For instance a P2P topic between users A and B would show user A's 'public' to user B and vice versa. If a user updates 'public', app user's P2P topics will change 'public'.

The 'private' parameter of a P2P topic is defined by each participant individually as with any other topic type.

### Group Topics

Group topics represent communication channels between multiple users. The name of a group topic is `grp` followed by a string of characters from base64 URL-encoding set. No other assumptions can be made about internal structure or length of the group name.

A group topic is created by sending a `{sub}` message with the topic field set to "new". Tinode will respond with a `{ctrl}` message with the name of the newly created topic, i.e. `{sub topic="new"}` is replied with `{ctrl topic="grpmiKBkQVXnm3P"}`. The user who created the topic becomes topic owner. Ownership can be transferred to another user with a `{set}` message but at least one user must remain the topic owner.

A user joining or leaving the topic generates a `{pres}` message to all other users who are currently in the joined state with the topic.


## Support for Client-Side Caching

TODO
