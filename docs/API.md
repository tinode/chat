<!-- TOC depthFrom:1 depthTo:6 withLinks:1 updateOnSave:1 orderedList:0 -->

- [Server API](#server-api)
	- [How it Works?](#how-it-works)
	- [General Considerations](#general-considerations)
	- [Connecting to the Server](#connecting-to-the-server)
		- [gRPC](#grpc)
		- [WebSocket](#websocket)
		- [Long Polling](#long-polling)
		- [Out of Band Large Files](#out-of-band-large-files)
		- [Running Behind a Reverse Proxy](#running-behind-a-reverse-proxy)
	- [Users](#users)
		- [Authentication](#authentication)
			- [Creating an Account](#creating-an-account)
			- [Logging in](#logging-in)
			- [Changing Authentication Parameters](#changing-authentication-parameters)
			- [Resetting a Password, i.e. "Forgot Password"](#resetting-a-password-ie-forgot-password)
		- [Suspending a User](#suspending-a-user)
		- [Credential Validation](#credential-validation)
		- [Access Control](#access-control)
	- [Topics](#topics)
		- [`me` Topic](#me-topic)
		- [`fnd` and Tags: Finding Users and Topics](#fnd-and-tags-finding-users-and-topics)
			- [Query Language](#query-language)
			- [Incremental Updates to Queries](#incremental-updates-to-queries)
			- [Query Rewrite](#query-rewrite)
			- [Possible Use Cases](#possible-use-cases)
		- [Peer to Peer Topics](#peer-to-peer-topics)
		- [Group Topics](#group-topics)
		- [`sys` Topic](#sys-topic)
	- [Using Server-Issued Message IDs](#using-server-issued-message-ids)
	- [User Agent and Presence Notifications](#user-agent-and-presence-notifications)
	- [Public and Private Fields](#public-and-private-fields)
		- [Public](#public)
		- [Private](#private)
	- [Format of Content](#format-of-content)
	- [Out-of-Band Handling of Large Files](#out-of-band-handling-of-large-files)
		- [Uploading](#uploading)
		- [Downloading](#downloading)
	- [Push Notifications](#push-notifications)
		- [Tinode Push Gateway](#tinode-push-gateway)
		- [Google FCM](#google-fcm)
		- [Stdout](#stdout)
	- [Messages](#messages)
		- [Client to Server Messages](#client-to-server-messages)
			- [`{hi}`](#hi)
			- [`{acc}`](#acc)
			- [`{login}`](#login)
			- [`{sub}`](#sub)
			- [`{leave}`](#leave)
			- [`{pub}`](#pub)
			- [`{get}`](#get)
			- [`{set}`](#set)
			- [`{del}`](#del)
			- [`{note}`](#note)
		- [Server to Client Messages](#server-to-client-messages)
			- [`{data}`](#data)
			- [`{ctrl}`](#ctrl)
			- [`{meta}`](#meta)
			- [`{pres}`](#pres)
			- [`{info}`](#info)

<!-- /TOC -->

# Server API

## How it Works?

Tinode is an IM router and a store. Conceptually it loosely follows a publish-subscribe model.

Server connects sessions, users, and topics. Session is a network connection between a client application and the server. User represents a human being who connects to the server with a session. Topic is a named communication channel which routes content between sessions.

Users and topics are assigned unique IDs. User ID is a string with 'usr' prefix followed by base64-URL-encoded pseudo-random 64-bit number, e.g. `usr2il9suCbuko`. Topic IDs are described below.

Clients such as mobile or web applications create sessions by connecting to the server over a websocket or through long polling. Client authentication is required in order to perform most operations. Client authenticates the session by sending a `{login}` packet. See [Authentication](#authentication) section for details. Once authenticated, the client receives a a token which is used for authentication later. Multiple simultaneous sessions may be established by the same user. Logging out is not supported (and not needed).

Once the session is established, the user can start interacting with other users through topics. The following
topic types are available:

* `me` is a topic for managing one's profile and receiving notifications about other topics; `me` topic exists for every user.
* `fnd` topic is used for finding other users and topics; `fnd` topic also exists for every user.
* Peer to peer topic is a communication channel strictly between two users. Each participant sees topic name as the ID of the other participant: 'usr' prefix followed by a base64-URL-encoded numeric part of user ID, e.g. `usr2il9suCbuko`.
* Group topic is a channel for multi-user communication. It's named as 'grp' followed by 11 pseudo-random characters, i.e. `grpYiqEXb4QY6s`. Group topics must be explicitly created.

Session joins a topic by sending a `{sub}` packet. Packet `{sub}` serves three functions: creating a new topic, subscribing user to a topic, and attaching session to a topic. See [`{sub}`](#sub) section below for details.

Once the session has joined the topic, the user may start generating content by sending `{pub}` packets. The content is delivered to other attached sessions as `{data}` packets.

The user may query or update topic metadata by sending `{get}` and `{set}` packets.

Changes to topic metadata, such as changes in topic description, or when other users join or leave the topic, is reported to live sessions with `{pres}` (presence) packet. The `{pres}` packet is sent either to the topic being affected or to the `me` topic.

When user's `me` topic comes online (i.e. an authenticated session attaches to `me` topic), a `{pres}` packet is sent to `me` topics of all other users, who have peer to peer subscriptions with the first user.

## General Considerations

Timestamps are always represented as [RFC 3339](http://tools.ietf.org/html/rfc3339)-formatted string with precision up to milliseconds and timezone always set to UTC, e.g. `"2015-10-06T18:07:29.841Z"`.

Whenever base64 encoding is mentioned, it means base64 URL encoding with padding characters stripped, see [RFC 4648](http://tools.ietf.org/html/rfc4648).

The `{data}` packets have server-issued sequential IDs: base-10 numbers starting at 1 and incrementing by one with every message. They are guaranteed to be unique per topic.

In order to connect requests to responses, client may assign message IDs to all packets set to the server. These IDs are strings defined by the client. Client should make them unique at least per session. The client-assigned IDs are not interpreted by the server, they are returned to the client as is.

## Connecting to the Server

There are three ways to access the server over the network: websocket, long polling, and [gRPC](https://grpc.io/).

When the client establishes a connection to the server over HTTP(S), such as over a websocket or long polling, the server offers the following endpoints:
 * `/v0/channels` for websocket connections
 * `/v0/channels/lp` for long polling
 * `/v0/file/u` for file uploads
 * `/v0/file/s` for serving files (downloads)

`v0` denotes API version (currently zero). Every HTTP(S) request must include the API key. The server checks for the API key in the following order:
* HTTP header `X-Tinode-APIKey`
* URL query parameter `apikey` (/v0/file/s/abcdefg.jpeg?apikey=...)
* Form value `apikey`
* Cookie `apikey`

A default API key is included with every demo app for convenience. Generate your own key for production using [`keygen` utility](../keygen).

Once the connection is opened, the client must issue a `{hi}` message to the server. Server responds with a `{ctrl}` message which indicates either success or an error. The `params` field of the response contains server's protocol version `"params":{"ver":"0.15"}` and may include other values.

### gRPC

See definition of the gRPC API in the [proto file](../pbx/model.proto). gRPC API has slightly more functionality than the API described in this document: it allows the `root` user to send messages on behalf of other users as well as delete users.

The `bytes` fields in protobuf messages expect JSON-encoded UTF-8 content. For example, a string should be quoted before being converted to bytes as UTF-8: `[]byte("\"some string\"")` (Go), `'"another string"'.encode('utf-8')` (Python 3).

### WebSocket

Messages are sent in text frames, one message per frame. Binary frames are reserved for future use. By default server allows connections with any value in the `Origin` header.

### Long Polling

Long polling works over `HTTP POST` (preferred) or `GET`. In response to client's very first request server sends a `{ctrl}` message containing `sid` (session ID) in `params`. Long polling client must include `sid` in every subsequent request either in the URL or in the request body.

Server allows connections from all origins, i.e. `Access-Control-Allow-Origin: *`

### Out of Band Large Files

Large files are sent out of band using `HTTP POST` as `Content-Type: multipart/form-data`. See [below](#out-of-band-handling-of-large-files) for details.

### Running Behind a Reverse Proxy

Tinode server can be set up to run behind a reverse proxy, such as NGINX. For efficiency it can accept client connections from Unix sockets by setting `listen` and/or `grpc_listen` config parameters to the path of the Unix socket file, e.g. `unix:/run/tinode.sock`. The server may also be configured to read peer's IP address from `X-Forwarded-For` HTTP header by setting `use_x_forwarded_for` config parameter to `true`.

## Users

User is meant to represent a person, an end-user: producer and consumer of messages.

Users are generally assigned one of the two authentication levels: authenticated `auth` or anonymous `anon`. The third level `root` is only accessible over `gRPC` where it permits the `root` to send messages on behalf of other users.

When a connection is first established, the client application can send either an `{acc}` or a `{login}` message which authenticates the user at one the levels.

Each user is assigned a unique ID. The IDs are composed as `usr` followed by base64-encoded 64-bit numeric value, e.g. `usr2il9suCbuko`. Users also have the following properties:

* `created`: timestamp when the user record was created
* `updated`: timestamp of when user's `public` was last updated
* `status`: state of the account
* `username`: unique string used in `basic` authentication; username is not accessible to other users
* `defacs`: object describing user's default access mode for peer to peer conversations with authenticated and anonymous users; see [Access control](#access-control) for details
  * `auth`: default access mode for authenticated `auth` users
  * `anon`: default access for anonymous `anon` users
* `public`: an application-defined object that describes the user. Anyone who can query user for `public` data.
* `private`: an application-defined object that is unique to the current user and accessible only by the user.
* `tags`: [discovery](#fnd-and-tags-finding-users-and-topics) and credentials.

User's account has a state. The following states are defined:
 * `ok` (normal): the default state which means the account is not restricted in any way and can be used normally;
 * `susp` (suspended): the user is prevented from accessing the account as well as not found through [search](#fnd-and-tags-finding-users-and-topics); the state can be assigned by the administrator and fully reversible.
 * `del` (soft-deleted): user is marked as deleted but user's data is retained; un-deleting the user is not currenly supported.
 * `undef` (undefined): used internally by authenticators; should not be used elsewhere.

A user may maintain multisple simultaneous connections (sessions) with the server. Each session is tagged with a client-provided `User Agent` string intended to differentiate client software.

Logging out is not supported by design. If an application needs to change the user, it should open a new connection and authenticate it with the new user credentials.


### Authentication

Authentication is conceptually similar to [SASL](https://en.wikipedia.org/wiki/Simple_Authentication_and_Security_Layer): it's provided as a set of adapters each implementing a different authentication method. Authenticators are used during account registration [`{acc}`](#acc) and during [`{login}`](#login). The server comes with the following authentication methods out of the box:

 * `token` provides authentication by a cryptographic token.
 * `basic` provides authentication by a login-password pair.
 * `anonymous` is designed for cases where users are temporary, such as handling customer support requests through chat.
 * `rest` is a [meta-method](../server/auth/rest/) which allows use of external authentication systems by means of JSON RPC.

Any other authentication method can be implemented using adapters.

The `token` is intended to be the primary means of authentication. Tokens are designed in such a way that token authentication is light weight. For instance, token authenticator generally does not make any database calls, all processing is done in-memory. All other authentication methods are intended to be used only to obtain or refresh the token. Once the token is obtained, subsequent logins should use it.

The `basic` authentication scheme expects `secret` to be a base64-encoded string of a string composed of a user name followed by a colon `:` followed by a plan text password. User name in the `basic` scheme must not contain the colon character `:` (ASCII 0x3A).

The `anonymous` scheme can be used to create accounts, it cannot be used for logging in: a user creates an account using `anonymous` scheme and obtains a cryptographic token which it uses for subsequent `token` logins. If the token is lost or expired, the user is no longer able to access the account.

Compiled-in authenticator names may be changed by using `logical_names` configuration feature. For example, a custom `rest` authenticator may be exposed as `basic` instead of default one or `token` authenticator could be hidden from users. The feature is activated by providing an array of mappings in the config file: `logical_name:actual_name` to rename or `actual_name:` to hide. For instance, to use a `rest` service for basic authentication use `"logical_names": ["basic:rest"]`.


#### Creating an Account

When a new account is created, the user must inform the server which authentication method will be later used to gain access to this account as well as provide shared secret, if appropriate. Only `basic` and `anonymous` can be used during account creation. The `basic` requires the user to generate and send a unique login and password to the server. The `anonymous` does not exchange secrets.

User may optionally set `{acc login=true}` to use the new account for immediate authentication. When `login=false` (or not set), the new account is created but the authentication status of the session which created the account remains unchanged. When `login=true` the server will attempt to authenticate the session with the new account, the response to the `{acc}` request will contain the authentication token on success. This is particularly important for the `anonymous` authentication.

#### Logging in

Logging in is performed by issuing a `{login}` request. Logging in is possible with `basic` and `token` only. Response to any login is a `{ctrl}` message with either a code 200 and a token which can be used in subsequent logins with `token` authentication, or a code 300 request for additional information, such as verifying credentials or responding to a method-dependent challenge in multi-step authentication, or a code 4xx error.

Token has server-configured expiration time so it needs to be periodically refreshed.

#### Changing Authentication Parameters

User may change authentication parameters, such as changing login and password, by issuing an `{acc}` request. Only `basic` authentication currently supports changing parameters:
```js
acc: {
  id: "1a2b3", // string, client-provided message id, optional
  user: "usr2il9suCbuko", // user being affected by the change, optional
  token: "XMg...g1Gp8+BO0=", // authentication token if the session
                             // is not yet authenticated, optional.
  scheme: "basic", // authentication scheme being updated.
  secret: base64encode("new_username:new_password") // new parameters
}
```
In order to change just the password, `username` should be left empty, i.e. `secret: base64encode(":new_password")`.

If the session is not authenticated, the request must include a `token`. It can be a regular authentication token obtained during login, or a restricted token received through [Resetting a Password](#resetting-a-password) process. If the session is authenticated, the token must not be included. If the request is authenticated for access level `ROOT`, then the `user` may be set to a valid ID of another user. Otherwise it must be blank (defaulting to the current user) or equal to the ID of the current user.


#### Resetting a Password, i.e. "Forgot Password"

To reset login or password, (or any other authentication secret, if such action is supported by the authenticator), one sends a `{login}` message with the `scheme` set to `reset` and the `secret` containing a base64-encoded string "`authentication scheme to reset secret for`:`reset method`:`reset method value`". Most basic case of resetting a password by email is
```js
login: {
  id: "1a2b3",
  scheme: "reset",
  secret: base64encode("basic:email:jdoe@example.com")
}
```
where `jdoe@example.com` is an earlier validated user's email.

If the email matches the registration, the server will send a message using specified method and address with instructions for resetting the secret. The email contains a restricted security token which the user can include into an `{acc}` request with the new secret as described in [Changing Authentication Parameters](#changing-authentication-parameters).

### Suspending a User

User's account can be suspended by service administrator. Once the account is suspended, the user is no longer able to login and use the service.

Only the `root` user may suspend the account. To suspend the account the root user sends the following message:
```js
acc: {
  id: "1a2b3", // string, client-provided message id, optional
  user: "usr2il9suCbuko", // user being affected by the change
  status: "suspended"
}
```
Sending the same message with `status: "ok"` un-suspends the account. A root user may check account status by executing `{get what="desc"}` command against user's `me` topic.


### Credential Validation

Server may be optionally configured to require validation of certain credentials associated with the user accounts and authentication scheme. For instance, it's possible to require user to provide a unique email or a phone number, or to solve a captcha as a condition of account registration.

The server supports verification of email out of the box with just a configuration change. is mostly functional, verification of phone numbers is not functional because a commercial subscription is needed in order to be able to send text messages (SMS).

If certain credentials are required, then user must maintain them in validated state at all times. It means if a required credential has to be changed, the user must first add and validate the new credential and only then remove the old one.

Credentials are initially assigned at registration time by sending an `{acc}` message, added using `{set topic="me"}`, deleted using `{del topic="me"}`, and queries by `{get topic="me"}` messages. Credentials are verified by the client by sending either a `{login}` or an `{acc}` message.


### Access Control

Access control manages user's access to topics through access control lists (ACLs) or bearer tokens (_bearer tokens are not implemented as of version 0.15_).

Access control is mostly usable for group topics. Its usability for `me` and P2P topics is limited to managing presence notifications and banning uses from initiating or continuing P2P conversations.

User's access to a topic is defined by two sets of permissions: user's desired permissions "want", and permissions granted to user by topic's manager(s) "given". Each permission is represented by a bit in a bitmap. It can be either present or absent. The actual access is determined as a bitwise AND of wanted and given permissions. The permissions are communicated in messages as a set of ASCII characters, where presence of a character means a set permission bit:

* No access: `N` is not a permission per se but an indicator that permissions are explicitly cleared/not set. It usually indicates that the default permissions should *not* be applied.
* Join: `J`, permission to subscribe to a topic
* Read: `R`, permission to receive `{data}` packets
* Write: `W`, permission to `{pub}` to topic
* Presence: `P`, permission to receive presence updates `{pres}`
* Approve: `A`, permission to approve requests to join a topic; a user with such permission is topic's manager
* Sharing: `S`, permission to invite other people to join the topic
* Delete: `D`, permission to hard-delete messages; only owners can completely delete topics
* Owner: `O`, user is the topic owner; topic may have a single owner only; some topics have no owner

Topic's default access is established at the topic creation time by `{sub.desc.defacs}` and can be subsequently modified by `{set}` messages. Default access is defined for two categories of users: authenticated and anonymous. This value is applied as a default "given" permission to all new subscriptions.

Client may replace explicit permissions in `{sub}` and `{set}` messages with an empty string to tell Tinode to use default permissions. If client specifies no default access permissions at topic creation time, authenticated users will receive a `RWP` permission, anonymous users will receive an empty permission which means every subscription request must be explicitly approved by the topic manager.

Access permissions can be assigned on a per-user basis by `{set}` messages.

## Topics

Topic is a named communication channel for one or more people. Topics have persistent properties. These topic properties can be queried by `{get what="desc"}` message.

Topic properties independent of the user making the query:
* `created`: timestamp of topic creation time
* `updated`: timestamp of when topic's `public` or `private` was last updated
* `touched`: timestamp of the last message sent to the topic
* `defacs`: object describing topic's default access mode for authenticated and anonymous users; see [Access control](#access-control) for details
 * `auth`: default access mode for authenticated users
 * `anon`: default access for anonymous users
* `seq`: integer server-issued sequential ID of the latest `{data}` message sent through the topic
* `public`: an application-defined object that describes the topic. Anyone who can subscribe to topic can receive topic's `public` data.

User-dependent topic properties:
* `acs`: object describing given user's current access permissions; see [Access control](#access-control) for details
 * `want`: access permission requested by this user
 * `given`: access permissions given to this user
* `private`: an application-defined object that is unique to the current user.

Topic usually have subscribers. One of the subscribers may be designated as topic owner (`O` access permission) with full access permissions. The list of subscribers can be queries with a `{get what="sub"}` message. The list of subscribers is returned in a `sub` section of a `{meta}` message.

### `me` Topic

Topic `me` is automatically created for every user at the account creation time. It serves as means of managing account information, receiving presence notification from people and topics of interest. Topic `me` has no owner. The topic cannot be deleted or unsubscribed from. One can `leave` the topic which will stop all relevant communication and indicate that the user is offline (although the user may still be logged in and may continue to use other topics).

Joining or leaving `me` generates a `{pres}` presence update sent to all users who have peer to peer topics with the given user and `P` permissions set.

Topic `me` is read-only. `{pub}` messages to `me` are rejected.

Message `{get what="desc"}` to `me` is automatically replied with a `{meta}` message containing `desc` section with the topic parameters (see intro to [Topics](#topics) section). The `public` parameter of `me` topic is data that the user wants to show to his/her connections. Changing it changes `public` not just for the `me` topic, but also everywhere where user's `public` is shown, such as `public` of all user's peer to peer topics.

Message `{get what="sub"}` to `me` is different from any other topic as it returns the list of topics that the current user is subscribed to as opposite to the expected user's subscription to `me`.
* seq: server-issued numeric id of the last message in the topic
* recv: seq value self-reported by the current user as received
* read: seq value self-reported by the current user as read
* seen: for P2P subscriptions, timestamp of user's last presence and User Agent string are reported
 * when: timestamp when the user was last online
 * ua: user agent string of the user's client software last used

Message `{get what="data"}` to `me` is rejected.

### `fnd` and Tags: Finding Users and Topics

Topic `fnd` is automatically created for every user at the account creation time. It serves as an endpoint for discovering other users and group topics.

Users and group topics can be discovered by optional `tags`. Tags are optionally assigned at the topic or user creation time then can be updated by using `{set what="tags"}` against a `me` or a group topic.

A tag is an arbitrary case-insensitive Unicode string (forced to lowercase) starting with a Unicode letter or digit. `Tags` must not contain the double quote `"`, `\u0022` but may contain spaces. `Tags` may have a prefix which serves as a namespace. The prefix is a string followed by a colon `:`, ex. prefixed phone tag `tel:+14155551212` or prefixed email tag `email:alice@example.com`. Some prefixed `tags` are optionally enforced to be unique. In that case only one user or topic may have such a tag. Certain `tags` may be forced to be immutable to the user, i.e. user's attempts to add or remove an immutable tag will be rejected by the server.

The `tags` are indexed server-side and used in user and topic discovery. Search returns users and topics sorted by the number of matched tags in descending order.

In order to find users or topics, a user sets either `public` or `private` parameter of the `fnd` topic to a search query (see [Query language](#query-language)) then issues a `{get topic="fnd" what="sub"}` request. If both `public` and `private` are set, the `public` query is used. The `private` query is persisted across sessions and devices, i.e. all user's sessions see the same `private` query. The value of the `public` query is ephemeral, i.e. it's not saved to database and not shared between user's sessions. The `private` query is intended for large queries which do not change often, such as finding matches for everyone in user's contact list on a mobile phone. The `public` query is intended to be short and specific, such as finding some topic or a user who is not in the contact list.

The system responds with a `{meta}` message with the `sub` section listing details of the found users or topics formatted as subscriptions.

Topic `fnd` is read-only. `{pub}` messages to `fnd` are rejected.

(The following functionality is not implemented yet) When a new user registers with tags matching the given query, the `fnd` topic will receive `{pres}` notification for the new user.

[Plugins](../pbx) support `Find` service which can be used to replace default search with a custom one.

#### Query Language

Tinode query language is used to define search queries for finding users and topics. The query is a string containing tags separated by spaces or commas. Tags are strings - individual query terms which are matched against user's or topic's tags. The tags can be written in an RTL language but the query as a whole is parsed left to right. Spaces are treated as the `AND` operator, commas (as well as commas preceded and/or followed by a space) as the `OR` operator. The order of operators is ignored: all `AND` tags are grouped together, all `OR` tags are grouped together. `OR` takes precedence over `AND`: if a tag is preceded of followed by a comma, it's an `OR` tag, otherwise an `AND`. For example, `a AND b OR c` is rewritten as `(b OR c) AND a`.

Tags containing spaces or commas must be enclosed in double quotes (`"`, `\u0022`): i.e. `"abc, def"` is treated as a single token `abc, def`. Tags must start with a Unicode letter or digit. Tags must not contain the double quote (`"`, `\u0022`).

**Some examples:**
* `flowers`: find topics or users which contain tag `flowers`.
* `flowers travel`: find topics or users which contain both tags `flowers` and `travel`.
* `flowers, travel`: find topics or users which contain either tag `flowers` or `travel` (or both).
* `flowers travel, puppies`: find topics or users which contain `flowers` and either `travel` or `puppies`, i.e. `(travel OR puppies) AND flowers`.
* `flowers, travel puppies, kittens`: find topics or users which contain either one of `flowers`, `travel`, `puppies`, or `kittens`, i.e. `flowers OR travel OR puppies OR kittens`. The space between `travel` and `puppies` is treated as `OR` due to `OR` taking precedence over `AND`.

#### Incremental Updates to Queries

Queries, particularly `fnd.private` could be arbitrarily large, limited only by the limits on the message size and by the underlying database limits of query size. Instead of rewriting the entire query to add or remove a tag, tag can be added or removed incrementally.

The incremental update request is processed left to right. It may contain the same tag multiple times, i.e. `-a_tag+a_tag` is a valid request.

#### Query Rewrite

Finding users by login, phone or email requires query terms to be written with tags, i.e. `email:alice@example.com` instead of `alice@example.com`. This may present a problem to end users because it requires them to learn the query language. Tinode solves this problem by implementing _query rewrite_ on the server: if query term does not contain a tag, server rewrites it by adding the tag also keeping the original term (terms with tags are left intact). All terms that look like email, for instance, `alice@example.com` are rewritten to `email:alice@example.com OR alice@example.com`. Terms which look like phone numbers are converted to [E.164](https://en.wikipedia.org/wiki/E.164) and also rewritten as `tel:+14155551212 OR +14155551212`. All other untagged terms are rewritten as logins: `basic:alice OR alice`.

#### Possible Use Cases
* Restricting users to organisations.
  An immutable tag(s) may be assigned to the user which denotes the organisation the user belongs to. When the user searches for other users or topics, the search can be restricted to always contain the tag. This approach can be used to segment users into organisations with limited visibility into each other.

* Search by geographical location.
  Client software may periodically assign a [geohash](https://en.wikipedia.org/wiki/Geohash) tag to the user based on current location. Searching for users in a given area would mean matching on geohash tags.

* Search by numerical range, such as age range.
  The approach is similar to geohashing. The entire range of numbers is covered by the smallest possible power of 2, for instance the range of human ages is covered by 2<sup>7</sup>=128 years. The entire range is split in two halves: the range 0-63 is denoted by 0, 64-127 by 1. The operation is repeated with each subrange, i.e. 0-31 is 00, 32-63 is 01, 0-15 is 000, 32-47 is 010. Once completed, the age 30 will belong to the following ranges: 0 (0-63), 00 (0-31), 001 (16-31), 0011 (24-31), 00111 (28-31), 001111 (30-31), 0011110 (30). A 30 y.o. user is assigned a few tags to indicate the age, i.e. `age:00111`, `age:001111`,  and `age:0011110`. Technically, all 7 tags may be assigned but usually it's impractical. To query for anyone in the age range 28-35 convert the range into a minimal number of tags: `age:00111` (28-31), `age:01000` (32-35). This query will match the 30 y.o. user by tag `age:00111`.


### Peer to Peer Topics

Peer to peer (P2P) topics represent communication channels between strictly two users. The name of the topic is different for each of the two participants. Each of them sees the name of the topic as the user ID of the other participant: `usr` followed by base64 URL-encoded ID of the user. For example, if two users `usrOj0B3-gSBSs` and `usrIU_LOVwRNsc` start a P2P topic, the first one will see it as `usrIU_LOVwRNsc`, the second as `usrOj0B3-gSBSs`. The P2P topic has no owner.

A P2P topic is created by one user subscribing to topic with the name equal to the ID of the other user. For instance, user `usrOj0B3-gSBSs` can establish a P2P topic with user `usrIU_LOVwRNsc` by sending a `{sub topic="usrIU_LOVwRNsc"}`. Tinode will respond with a `{ctrl}` packet with the name of the newly created topic as described above. The other user will receive a `{pres}` message on `me` topic with updated access permissions.

The 'public' parameter of P2P topics is user-dependent. For instance a P2P topic between users A and B would show user A's 'public' to user B and vice versa. If a user updates 'public', all user's P2P topics will automatically update 'public' too.

The 'private' parameter of a P2P topic is defined by each participant individually as with any other topic type.

### Group Topics

Group topics represent communication channels between multiple users. The name of a group topic is `grp` followed by a string of characters from base64 URL-encoding set. No other assumptions can be made about internal structure or length of the group name.

A group topic is created by sending a `{sub}` message with the topic field set to string `new` optionally followed by any characters, e.g. `new` or `newAbC123` are equivalent. Tinode will respond with a `{ctrl}` message with the name of the newly created topic, i.e. `{sub topic="new"}` is replied with `{ctrl topic="grpmiKBkQVXnm3P"}`. If topic creation fails, the error is reported on the original topic name, i.e. `new` or `newAbC123`. The user who created the topic becomes topic owner. Ownership can be transferred to another user with a `{set}` message but one user must remain the owner at all times.

A user joining or leaving the topic generates a `{pres}` message to all other users who are currently in the joined state with the topic.

### `sys` Topic

The `sys` topic serves as an always available channel of communication with the system administrators. A normal non-root user cannot subscribe to `sys` but can publish to it without subscription. Existing clients use this channel to report abuse by sending a Drafty-formatted `{pub}` message with the report as JSON attachment. A root user can subscribe to `sys` topic. Once subscribed, the root user will receive messages sent to `sys` topic by other users.

## Using Server-Issued Message IDs

Tinode provides basic support for client-side caching of `{data}` messages in the form of server-issued sequential message IDs. The client may request the last message id from the topic by issuing a `{get what="desc"}` message. If the returned ID is greater than the ID of the latest received message, the client knows that the topic has unread messages and their count. The client may fetch these messages using `{get what="data"}` message. The client may also paginate history retrieval by using message IDs.

## User Agent and Presence Notifications

A user is reported as being online when one or more of user's sessions are attached to the `me` topic. Client-side software identifies itself to the server using `ua` (user agent) field of the `{login}` message. The _user agent_ is published in `{meta}` and `{pres}` messages in the following way:

 * When user's first session attaches to `me`, the _user agent_ from that session is broadcast in the `{pres what="on" ua="..."}` message.
 * When multiple user sessions are attached to `me`, the _user agent_ of the session where the most recent action has happened is reported in `{pres what="ua" ua="..."}`; the 'action' in this context means any message sent by the client. To avoid potentially excessive traffic, user agent changes are broadcast no more frequently than once a minute.
 * When user's last session detaches from `me`, the _user agent_ from that session is recorded together with the timestamp; the user agent is broadcast in the `{pres what="off"  ua="..."}` message and subsequently reported as the last online timestamp and user agent.

An empty `ua=""` _user agent_ is not reported. I.e. if user attaches to `me` with non-empty _user agent_ then does so with an empty one, the change is not reported. An empty _user agent_ may be disallowed in the future.

## Public and Private Fields

Topics and subscriptions have `public` and `private` fields. Generally, the fields are application-defined. The server does not enforce any particular structure of these fields except for `fnd` topic. At the same time, client software should use the same format for interoperability reasons.

### Public
The format of the `public` field in group and peer to peer topics is expected to be a [vCard](https://en.wikipedia.org/wiki/VCard) although only `fn` and `photo` fields are currently used by client software:

```js
vcard: {
  fn: "John Doe", // string, formatted name
  n: {
    surname: "Miner", // last of family name
    given: "Coal", // first or given name
    additional: "Diamond", // additional name, such as middle name or patronymic or nickname.
    prefix: "Dr.", // prefix, such as honorary title or gender designation.
    suffix: "Jr.", // suffix, such as 'Jr' or 'II'
  }, // object, user's structured name
  org: "Most Evil Corp", // string, name of the organisation the user belongs to.
  title: "CEO", // string, job title
  tel: [
    {
      type: "HOME", // string, optional designation
      uri: "tel:+17025551234" // string, phone number
    }, ...
  ], // array of objects, list of phone numbers associated with the user
  email: [
    {
      type: "WORK", // string, optional designation
      uri: "email:alice@example.com", // string, email address
    }, ...
  ], // array of objects, list of user's email addresses
  impp: [
    {
      type: "OTHER",
      uri: "tinode:usrRkDVe0PYDOo", // string, email address
    }, ...
  ], // array of objects, list of user's IM handles
  photo: {
    type: "jpeg", // image type
    data: "..." // base64-encoded binary image data
  } // object, avatar photo. Java does not have a useful bitmap class, so keeping it as bits here.
}
```

The `fnd` topic expects `public` to be a string representing a [search query](#query-language)).

### Private

The format of the `private` field in group and peer to peer topics is a set of key-value pairs. The following keys are currently defined:
```js
private: {
  comment: "some comment", // string, optional user comment about a topic or a peer user
  arch: true, // boolean value indicating that the topic is archived by the user, i.e.
              // should not be shown in the UI with other non-archived topics.
  accepted: "JRWS" // string, 'given' mode accepted by the user.
}
```

Although it's not yet enforced, custom fields should start with an `x-` followed by the application name, e.g. `x-myapp-value: "abc"`. The fields should contain primitive types only, i.e. `string`, `boolean`, `number`, or `null`.

The `fnd` topic expects `private` to be a string representing a [search query](#query-language)).

## Format of Content

Format of `content` field in `{pub}` and `{data}` is application-defined and as such the server does not enforce any particular structure of the field. At the same time, client software should use the same format for interoperability reasons. Currently the following two types of `content` are supported:
 * Plain text
 * [Drafty](./drafty.md)

If Drafty is used, message header `"head": {"mime": "text/x-drafty"}` must be set.


## Out-of-Band Handling of Large Files

Large files create problems when sent in-band for multiple reasons:
 * limits on database storage as in-band messages are stored in database fields
 * in-band messages must be downloaded completely as a part of downloading chat history

Tinode provides two endpoints for handling large files: `/v0/file/u` for uploading files and `v0/file/s` for downloading. The endpoints require the client to provide both [API key](#connecting-to-the-server) and login credentials. The server checks credentials in the following order:

**Login credentials**
 * HTTP header `Authorization` (https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Authorization)
 * URL query parameters `auth` and `secret` (/v0/file/s/abcdefg.jpeg?auth=...&secret=...)
 * Form values `auth` and `secret`
 * Cookies `auth` and `secret`

### Uploading

To upload a file first create an RFC 2388 multipart request then send it to the server using HTTP POST. The server responds to the request either with a `307 Temporary Redirect` with the new upload URL, or with a `200 OK` and a `{ctrl}` message in response body:

```js
ctrl: {
  params: {
    url: "/v0/file/s/mfHLxDWFhfU.pdf"
  },
  code: 200,
  text: "ok",
  ts: "2018-07-06T18:47:51.265Z"
}
```
If `307 Temporary Redirect` is returned, the client must retry the upload at the provided URL. The URL returned in `307` response should be used for just this one upload. All subsequent uploads should try the default URL first.

The `ctrl.params.url` contains the path to the uploaded file at the current server. It could be either the full path like `/v0/file/s/mfHLxDWFhfU.pdf`, a relative path like `./mfHLxDWFhfU.pdf`, or just the file name `mfHLxDWFhfU.pdf`. Anything but the full path is interpreted against the default *download* endpoint `/v0/file/s/`. For instance, if `mfHLxDWFhfU.pdf` is returned then the file is located at `http(s)://current-tinode-server/v0/file/s/mfHLxDWFhfU.pdf`.

Once the URL of the file is received, either immediately or after following the redirect, the client may use the URL to send a `{pub}` message with the uploaded file as an attachment. The URL should be used to produce a [Drafty](./drafty.md)-formatted `pub.content` field and also should be referenced in the `pub.head.attachments`:

```js
pub: {
  id: "121103",
  topic: "grpnG99YhENiQU",
  head: {
    attachments: ["/v0/file/s/sJOD_tZDPz0.jpg"],
    mime: "text/x-drafty"
  },
  content: {
    ent: [
    {
      data: {
      mime: "image/jpeg",
      name: "roses-are-red.jpg",
      ref:  "/v0/file/s/sJOD_tZDPz0.jpg",
      size: 437265
    },
      tp: "EX"
    }
  ],
  fmt: [
    {
      at: -1,
    key:0,
    len:1
    }
  ]
  }
}
```

It's important to list the URLs in the `head.attachments` field. Tinode server uses this field to maintain the uploaded file's use counter. Once the counter drops to zero for the given file (for instance, because a message with the shared URL was deleted or because the client failed to include the URL in the `head.attachments` field), the server will garbage collect the file. Only relative URLs should be used. Absolute URLs in the `head.attachments` field are ignored. The URL value is expected to be the `ctrl.params.url` returned in response to upload.

### Downloading

The serving endpoint `/v0/file/s` serves files in response to HTTP GET requests. The client must evaluate relative URLs against this endpoint, i.e. if it receives a URL `mfHLxDWFhfU.pdf` or `./mfHLxDWFhfU.pdf` it should interpret it as a path `/v0/file/s/mfHLxDWFhfU.pdf` at the current Tinode HTTP server.

_Important!_ As a security measure, the client should not send security credentials if the download URL is absolute and leads to another server.

## Push Notifications

Tinode uses compile-time adapters for handling push notifications. The server comes with [Tinode Push Gateway](../server/push/tnpg/), [Google FCM](https://firebase.google.com/docs/cloud-messaging/), and `stdout` adapters. Tinode Push Gateway and Google FCM support Android with [Play Services](https://developers.google.com/android/guides/overview) (may not be supported by some Chinese phones), iOS devices and all major web browsers excluding Safari. The `stdout` adapter does not actually send push notifications. It's mostly useful for debugging, testing and logging. Other types of push notifications such as [TPNS](https://intl.cloud.tencent.com/product/tpns) can be handled by writing appropriate adapters.

If you are writing a custom plugin, the notification payload is the following:
```js
{
  topic: "grpnG99YhENiQU", // Topic which received the message.
  xfrom: "usr2il9suCbuko", // ID of the user who sent the message.
  ts: "2019-01-06T18:07:30.038Z", // message timestamp in RFC3339 format.
  seq: "1234", // sequential ID of the message (integer value sent as text).
  mime: "text/x-drafty", // optional message MIME-Type.
  content: "Lorem ipsum dolor sit amet, consectetur adipisci", // The first 80 characters of the message content as plain text.
}
```

### Tinode Push Gateway

Tinode Push Gateway (TNPG) is a proprietary Tinode service which sends push notifications on behalf of Tinode. Internally it uses Google FCM and as such supports the same platforms as FCM. The main advantage of using TNPG over FCM is simplicity of configuration: mobile clients do not need to be recompiled, all is needed is a [configuration update](../server/push/tnpg/) on a server.

### Google FCM

[Google FCM](https://firebase.google.com/docs/cloud-messaging/) supports Android with [Play Services](https://developers.google.com/android/guides/overview), iPhone and iPad devices, and all major web browsers excluding Safari. In order to use FCM mobile clients (iOS, Android) must be recompiled with credentials obtained from Google. See [instructions](../server/push/fcm/) for details.

### Stdout

The `stdout` adapter is mostly useful for debugging and logging. It writes push payload to `STDOUT` where it can be redirected to file or read by some other process.


## Messages

A message is a logically associated set of data. Messages are passed as JSON-formatted UTF-8 text.

All client to server messages may have an optional `id` field. It's set by the client as means to receive an acknowledgement from the server that the message was received and processed. The `id` is expected to be a session-unique string but it can be any string. The server does not attempt to interpret it other than to check JSON validity. The `id` is returned unchanged by the server when it replies to the client message.

Server requires strictly valid JSON, including double quotes around field names. For brevity the notation below omits double quotes around field names as well as outer curly brackets. Examples use `//` comments only for expressiveness. The comments cannot be used in actual communication with the server.

For messages that update application-defined data, such as `{set}` `private` or `public` fields, when server-side
data needs to be cleared, use a string with a single Unicode DEL character "&#x2421;" (`\u2421`). I.e. sending `"public": null` will not clear the field, but sending `"public": "␡"` will.

Any unrecognized fields are silently ignored by the server.

### Client to Server Messages

#### `{hi}`

Handshake message client uses to inform the server of its version and user agent. This message must be the first that
the client sends to the server. Server responds with a `{ctrl}` which contains server build `build`, wire protocol version `ver`,
session ID `sid` in case of long polling, as well as server constraints, all in `ctrl.params`.

```js
hi: {
  id: "1a2b3",     // string, client-provided message id, optional
  ver: "0.15.8-rc2", // string, version of the wire protocol supported by the client, required
  ua: "JS/1.0 (Windows 10)", // string, user agent identifying client software,
                   // optional
  dev: "L1iC2...dNtk2", // string, unique value which identifies this specific
                   // connected device for the purpose of push notifications; not
                   // interpreted by the server.
                   // see [Push notifications support](#push-notifications-support); optional
  platf: "android", // string, underlying OS for the purpose of push notifications, one of
                   // "android", "ios", "web"; if missing, the server will try its best to
                   // detect the platform from the user agent string; optional
  lang: "en-US"    // human language of the client device; optional
}
```
The user agent `ua` is expected to follow [RFC 7231 section 5.5.3](http://tools.ietf.org/html/rfc7231#section-5.5.3) recommendation but the format is not enforced. The message can be sent more than once to update `ua`, `dev` and `lang` values. If sent more than once, the `ver` field of the second and subsequent messages must be either unchanged or not set.

#### `{acc}`

Message `{acc}` creates users or updates `tags` or authentication credentials `scheme` and `secret` of exiting users. To create a new user set `user` to the string `new` optionally followed by any character sequence, e.g. `newr15gsr`. Either authenticated or anonymous session can send an `{acc}` message to create a new user. To update authentication data or validate a credential of the current user leave `user` unset.

The `{acc}` message **cannot** be used to modify `desc` or `cred` of an existing user. Update user's `me` topic instead.

```js
acc: {
  id: "1a2b3", // string, client-provided message id, optional
  user: "new", // string, "new" to create a new user, default: current user, optional
  token: "XMgS...8+BO0=", // string, authentication token to use for the request if the
               // session is not authenticated, optional
  status: "ok", // change user's status; no default value, optional.
  scheme: "basic", // authentication scheme for this account, required;
               // "basic" and "anon" are currently supported for account creation.
  secret: base64encode("username:password"), // string, base64 encoded secret for the chosen
              // authentication scheme; to delete a scheme use a string with a single DEL
              // Unicode character "\u2421"; "token" and "basic" cannot be deleted
  login: true, // boolean, use the newly created account to authenticate current session,
              // i.e. create account and immediately use it to login.
  tags: ["alice johnson",... ], // array of tags for user discovery; see 'fnd' topic for
              // details, optional (if missing, user will not be discoverable other than
              // by login)
  cred: [  // account credentials which require verification, such as email or phone number.
    {
      meth: "email", // string, verification method, e.g. "email", "tel", "recaptcha", etc.
      val: "alice@example.com", // string, credential to verify such as email or phone
      resp: "178307", // string, verification response, optional
      params: { ... } // parameters, specific to the verification method, optional
    },
  ...
  ],

  desc: {  // object, user initialisation data closely matching that of table
           // initialisation; used only when creating an account; optional
    defacs: {
      auth: "JRWS", // string, default access mode for peer to peer conversations
                   // between this user and other authenticated users
      anon: "N"  // string, default access mode for peer to peer conversations
                 // between this user and anonymous (un-authenticated) users
    }, // Default access mode for user's peer to peer topics
    public: { ... }, // application-defined payload to describe user,
                // available to everyone
    private: { ... } // private application-defined payload available only to user
                // through 'me' topic
  }
}
```

Server responds with a `{ctrl}` message with `params` containing details of the new user. If `desc.defacs` is missing,
server will assign server-default access values.

The only supported authentication schemes for account creation are `basic` and `anonymous`.

#### `{login}`

Login is used to authenticate the current session.

```js
login: {
  id: "1a2b3",     // string, client-provided message id, optional
  scheme: "basic", // string, authentication scheme; "basic",
                   // "token", and "reset" are currently supported
  secret: base64encode("username:password"), // string, base64-encoded secret for the chosen
                  // authentication scheme, required
  cred: [
    {
      meth: "email", // string, verification method, e.g. "email", "tel", "captcha", etc, required
      resp: "178307" // string, verification response, required
    },
  ...
  ],   // response to a request for credential verification, optional
}
```

Server responds to a `{login}` packet with a `{ctrl}` message. The `params` of the message contains the id of the logged in user as `user`. The `token` contains an encrypted string which can be used for authentication. Expiration time of the token is passed as `expires`.

#### `{sub}`

The `{sub}` packet serves the following functions:
 * creating a new topic
 * subscribing user to an existing topic
 * attaching session to a previously subscribed topic
 * fetching topic data

User creates a new group topic by sending `{sub}` packet with the `topic` field set to `"new"`. Server will create a topic and respond back to session with the name of the newly created topic.

User creates a new peer to peer topic by sending `{sub}` packet with `topic` set to peer's user ID.

The user is always subscribed to and the session is attached to the newly created topic.

If the user had no relationship with the topic, sending `{sub}` packet creates it. Subscribing means to establish a relationship between session's user and the topic where no relationship existed in the past.

Joining (attaching to) a topic means for the session to start consuming content from the topic. Server automatically differentiates between subscribing and joining/attaching based on context: if the user had no prior relationship with the topic, the server subscribes the user then attaches the current session to the topic. If relationship existed, the server only attaches the session to the topic. When subscribing, the server checks user's access permissions against topic's access control list. It may grant immediate access, deny access, may generate a request for approval from topic managers.

Server replies to the `{sub}` with a `{ctrl}`.

The `{sub}` message may include a `get` and `set` fields which mirror `{get}` and `{set}` messages. If included, server will treat them as a subsequent `{set}` and `{get}` messages on the same topic. They `get` is set the reply may include `{meta}` and `{data}` messages.


```js
sub: {
  id: "1a2b3",  // string, client-provided message id, optional
  topic: "me",  // topic to be subscribed or attached to
  bkg: true,    // request to attach to topic is issued by an automated agent, server should delay sending
                // presence notifications because the agent is expected to disconnect very quickly
  // Object with topic initialisation data, new topics & new
  // subscriptions only, mirrors {set} message
  set: {
  // New topic parameters, mirrors {set desc}
    desc: {
      defacs: {
        auth: "JRWS", // string, default access for new authenticated subscribers
        anon: "N"    // string, default access for new anonymous (un-authenticated)
                     // subscribers
      }, // Default access mode for the new topic
      public: { ... }, // application-defined payload to describe topic
      private: { ... } // per-user private application-defined content
    }, // object, optional

    // Subscription parameters, mirrors {set sub}. 'sub.user' must be blank
    sub: {
      mode: "JRWS", // string, requested access mode, optional;
                   // default: server-defined
    }, // object, optional

    tags: [ // array of strings, update to tags (see fnd topic description), optional.
        "email:alice@example.com", "tel:1234567890"
    ],

    cred: { // update to credentials, optional.
      meth: "email", // string, verification method, e.g. "email", "tel", "recaptcha", etc.
      val: "alice@example.com", // string, credential to verify such as email or phone
      resp: "178307", // string, verification response, optional
      params: { ... } // parameters, specific to the verification method, optional
    }
  },

  get: {
    // Metadata to request from the topic; space-separated list, valid strings
    // are "desc", "sub", "data", "tags"; default: request nothing; unknown strings are
    // ignored; see {get  what} for details
    what: "desc sub data", // string, optional

    // Optional parameters for {get what="desc"}
    desc: {
      ims: "2015-10-06T18:07:30.038Z" // timestamp, "if modified since" - return
              // public and private values only if at least one of them has been
              // updated after the stated timestamp, optional

    },

    // Optional parameters for {get what="sub"}
    sub: {
      ims: "2015-10-06T18:07:30.038Z", // timestamp, "if modified since" - return
              // public and private values only if at least one of them has been
              // updated after the stated timestamp, optional
    user: "usr2il9suCbuko", // string, return results for a single user,
                            // any topic other than 'me', optional
    topic: "usr2il9suCbuko", // string, return results for a single topic,
                            // 'me' topic only, optional
      limit: 20 // integer, limit the number of returned objects
    },

    // Optional parameters for {get what="data"}, see {get what="data"} for details
    data: {
      since: 123, // integer, load messages with server-issued IDs greater or equal
            // to this (inclusive/closed), optional
      before: 321, // integer, load messages with server-issued sequential IDs less
            // than this (exclusive/open), optional
      limit: 20, // integer, limit the number of returned objects,
                 // default: 32, optional
    } // object, optional
  }
}
```

See [Public and Private Fields](#public-and-private-fields) for `private` and `public` format considerations.

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
}
```

#### `{pub}`

The message is used to distribute content to topic subscribers.

```js
pub: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic to publish to, required
  noecho: false, // boolean, suppress echo (see below), optional
  head: { key: "value", ... }, // set of string key-value pairs,
               // passed to {data} unchanged, optional
  content: { ... }  // object, application-defined content to publish
               // to topic subscribers, required
}
```

Topic subscribers receive the `content` in the [`{data}`](#data) message. By default the originating session gets a copy of `{data}` like any other session currently attached to the topic. If for some reason the originating session does not want to receive the copy of the data it just published, set `noecho` to `true`.

See [Format of Content](#format-of-content) for `content` format considerations.

The following values are currently defined for the `head` field:

 * `attachments`: an array of paths indicating media attached to this message `["/v0/file/s/sJOD_tZDPz0.jpg"]`.
 * `auto`: `true` when the message was sent automatically, i.e. by a chatbot or an auto-responder.
 * `forwarded`: an indicator that the message is a forwarded message, a unique ID of the original message, `"grp1XUtEhjv6HND:123"`.
 * `hashtags`: an array of hashtags in the message without the leading `#` symbol: `["onehash", "twohash"]`.
 * `mentions`: an array of user IDs mentioned (`@alice`) in the message: `["usr1XUtEhjv6HND", "usr2il9suCbuko"]`.
 * `mime`: MIME-type of the message content, `"text/x-drafty"`; a `null` or a missing value is interpreted as `"text/plain"`.
 * `priority`: message display priority: hint for the client that the message should be displayed more prominently for a set period of time; only `"high"` is currently defined; `{"level": "high", "expires": "2019-10-06T18:07:30.038Z"}`; `priority` can be set by the topic owner or administrator (`A` permission) only. The `"expires"` qualifier is optional.
 * `replace`: an indicator that the message is a correction/replacement for another message, a topic-unique ID of the message being updated/replaced, `":123"`
 * `reply`: an indicator that the message is a reply to another message, a unique ID of the original message, `"grp1XUtEhjv6HND:123"`.
 * `sender`: a user ID of the sender added by the server when the message is sent by on behalf of another user, `"usr1XUtEhjv6HND"`.
 * `thread`: an indicator that the message is a part of a conversation thread, a topic-unique ID of the first message in the thread, `":123"`; `thread` is intended for tagging a flat list of messages as opposite to a creating a tree.

Application-specific fields should start with an `x-<application-name>-`. Although the server does not enforce this rule yet, it may start doing so in the future.

The unique message ID should be formed as `<topic_name>:<seqId>` whenever possible, such as `"grp1XUtEhjv6HND:123"`. If the topic is omitted, i.e. `":123"`, it's assumed to be the current topic.

#### `{get}`

Query topic for metadata, such as description or a list of subscribers, or query message history. The requester must be [subscribed and attached](#sub) to the topic to receive the full response. Some limited `desc` and `sub` information is available without being attached.

```js
get: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, name of topic to request data from
  what: "sub desc data del cred", // string, space-separated list of parameters to query;
                        // unknown values are ignored; required

  // Optional parameters for {get what="desc"}
  desc: {
    ims: "2015-10-06T18:07:30.038Z" // timestamp, "if modified since" - return
          // public and private values only if at least one of them has been
          // updated after the stated timestamp, optional
  },

  // Optional parameters for {get what="sub"}
  sub: {
    ims: "2015-10-06T18:07:30.038Z", // timestamp, "if modified since" - return
          // public and private values only if at least one of them has been
          // updated after the stated timestamp, optional
    user: "usr2il9suCbuko", // string, return results for a single user,
                          // any topic other than 'me', optional
    topic: "usr2il9suCbuko", // string, return results for a single topic,
                           // 'me' topic only, optional
    limit: 20 // integer, limit the number of returned objects
  },

  // Optional parameters for {get what="data"}
  data: {
    since: 123, // integer, load messages with server-issued IDs greater or equal
                // to this (inclusive/closed), optional
    before: 321, // integer, load messages with server-issed sequential IDs less
               // than this (exclusive/open), optional
    limit: 20, // integer, limit the number of returned objects, default: 32,
               // optional
  },

  // Optional parameters for {get what="del"}
  del: {
    since: 5, // integer, load deleted ranges with the delete transaction IDs greater
              // or equal to this (inclusive/closed), optional
    before: 12, // integer, load deleted ranges with the delete transaction IDs less
                // than this (exclusive/open), optional
    limit: 25, // integer, limit the number of returned objects, default: 32,
               // optional
  }
}
```

* `{get what="desc"}`

Query topic description. Server responds with a `{meta}` message containing requested data. See `{meta}` for details.
If `ims` is specified and data has not been updated, the message will skip `public` and `private` fields.

Limited information is available without [attaching](#sub) to topic first.

See [Public and Private Fields](#public-and-private-fields) for `private` and `public` format considerations.

* `{get what="sub"}`

Get a list of subscribers. Server responds with a `{meta}` message containing a list of subscribers. See `{meta}` for details.
For `me` topic the request returns a list of user's subscriptions. If `ims` is specified and data has not been updated,
responds with a `{ctrl}` "not modified" message.

Only user's own subscription is returned without [attaching](#sub) to topic first.

* `{get what="tags"}`

Query indexed tags. Server responds with a `{meta}` message containing an array of string tags. See `{meta}` and `fnd` topic for details.
Supported only for `me` and group topics.

* `{get what="data"}`

Query message history. Server sends `{data}` messages matching parameters provided in the `data` field of the query.
The `id` field of the data messages is not provided as it's common for data messages. When all `{data}` messages are transmitted, a `{ctrl}` message is sent.

* `{get what="del"}`

Query message deletion history. Server responds with a `{meta}` message containing a list of deleted message ranges.

* `{get what="cred"}`

Query [credentials](#credentail-validation). Server responds with a `{meta}` message containing an array of credentials. Supported for `me` topic only.

#### `{set}`

Update topic metadata, delete messages or topic. The requester is generally expected to be [subscribed and attached](#sub) to the topic. Only `desc.private` and requester's `sub.mode` can be updated without attaching first.

```js
set: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, name of topic to update, required

  // Optional payload to update topic description
  desc: {
    defacs: { // new default access mode
      auth: "JRWP",  // access permissions for authenticated users
      anon: "JRW" // access permissions for anonymous users
    },
    public: { ... }, // application-defined payload to describe topic
    private: { ... } // per-user private application-defined content
  },

  // Optional payload to update subscription(s)
  sub: {
    user: "usr2il9suCbuko", // string, user affected by this request;
                            // default (empty) means current user
    mode: "JRWP" // string, access mode change, either given ('user'
                 // is defined) or requested ('user' undefined)
  }, // object, payload for what == "sub"

  // Optional update to tags (see fnd topic description)
  tags: [ // array of strings
    "email:alice@example.com", "tel:1234567890"
  ],

  cred: { // Optional update to credentials.
    meth: "email", // string, verification method, e.g. "email", "tel", "recaptcha", etc.
    val: "alice@example.com", // string, credential to verify such as email or phone
    resp: "178307", // string, verification response, optional
    params: { ... } // parameters, specific to the verification method, optional
  }
}
```

#### `{del}`

Delete messages, subscriptions, topics, users.

```js
del: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic affected, required for "topic", "sub",
               // "msg"
  what: "msg", // string, one of "topic", "sub", "msg", "user", "cred"; what
               // to delete - the entire topic, a subscription, some or all messages,
               // a user, a credential; optional, default: "msg"
  hard: false, // boolean, request to hard-delete vs mark as deleted; in case of
               // what="msg" delete for all users vs current user only;
               // optional, default: false
  delseq: [{low: 123, hi: 125}, {low: 156}], // array of ranges of message IDs
               // to delete, inclusive-exclusive, i.e. [low, hi), optional
  user: "usr2il9suCbuko" // string, user being deleted (what="user") or whose
               // subscription is being deleted (what="sub"), optional
  cred: { // credential to delete ('me' topic only).
    meth: "email", // string, verification method, e.g. "email", "tel", etc.
    val: "alice@example.com" // string, credential being deleted
  }
}
```

`what="msg"`

User can soft-delete `hard=false` (default) or hard-delete `hard=true` messages. Soft-deleting messages hides them from the requesting user but does not delete them from storage. An `R` permission is required to soft-delete messages. Hard-deleting messages deletes message content from storage (`head`, `content`) leaving a message stub. It affects all users. A `D` permission is needed to hard-delete messages. Messages can be deleted in bulk by specifying one or more message ID ranges in `delseq` parameter. Each delete operation is assigned a unique `delete ID`. The greatest `delete ID` is reported back in the `clear` of the `{meta}` message.

`what="sub"`

Deleting a subscription removes specified user from topic subscribers. It requires an `A` permission. A user cannot delete own subscription. A `{leave}` should be used instead. If the subscription is soft-deleted (default), it's marked as deleted without actually deleting a record from storage.

`what="topic"`

Deleting a topic deletes the topic including all subscriptions, and all messages. Only the owner can delete a topic.

`what="user"`

Deleting a user is a very heavy operation. Use caution.

`what="cred"`

Delete credential. Validated credentials and those with no attempts at validation are hard-deleted. Credentials with failed attempts at validation are soft-deleted which prevents their reuse by the same user.


#### `{note}`

Client-generated ephemeral notification for forwarding to other clients currently attached to the topic, such as typing notifications or delivery receipts. The message is "fire and forget": not stored to disk per se and not acknowledged by the server. Messages deemed invalid are silently dropped.
The `{note.recv}` and `{note.read}` do alter persistent state on the server. The value is stored and reported back in the corresponding fields of the `{meta.sub}` message.

```js
note: {
  topic: "grp1XUtEhjv6HND", // string, topic to notify, required
  what: "kp", // string, one of "kp" (key press), "read" (read notification),
              // "rcpt" (received notification), any other string will cause
              // message to be silently ignored, required
  seq: 123,   // integer, ID of the message being acknowledged, required for
              // rcpt & read
  unread: 10  // integer, client-reported total count of unread messages, optional.
}
```

The following actions are currently recognised:
 * kp: key press, i.e. a typing notification. The client should use it to indicate that the user is composing a new message.
 * recv: a `{data}` message is received by the client software but may not yet seen by user.
 * read: a `{data}` message is seen by the user. It implies `recv` as well.

The `read` and `recv` notifications may optionally include `unread` value which is the total count of unread messages as determined by this client. The per-user `unread` count is maintained by the server: it's incremented when new `{data}` messages are sent to user and reset to the values reported by the `{note unread=...}` message. The `unread` value is never decremented by the server. The value is included in push notifications to be shown on a badge on iOS:
<p align="center">
  <img src="./ios-pill-128.png" alt="Tinode iOS icon with a pill counter" width=64 height=64 />
</p>


### Server to Client Messages

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
  head: { key: "value", ... }, // set of string key-value pairs, passed
                               // unchanged from {pub}, optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
  seq: 123, // integer, server-issued sequential ID
  content: { ... } // object, application-defined content exactly as published
              // by the user in the {pub} message
}
```

Data messages have a `seq` field which holds a sequential numeric ID generated by the server. The IDs are guaranteed to be unique within a topic. IDs start from 1 and sequentially increment with every successful `{pub}`](#pub) message received by the topic.

See [Format of Content](#format-of-content) for `content` format considerations.

See [`{pub}`](#pub) message for the possible values of the `head` field.


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

Information about topic metadata or subscribers, sent in response to `{get}`, `{set}` or `{sub}` message to the originating session.

```js
meta: {
  id: "1a2b3", // string, client-provided message id, optional
  topic: "grp1XUtEhjv6HND", // string, topic name, if this is a response in
                            // context of a topic, optional
  ts: "2015-10-06T18:07:30.038Z", // string, timestamp
  desc: {
    created: "2015-10-24T10:26:09.716Z",
    updated: "2015-10-24T10:26:09.716Z",
    status: "ok", // account status; included for `me` topic only, and only if
                  // the request is sent by a root-authenticated session.
    defacs: { // topic's default access permissions; present only if the current
              //user has 'S' permission
      auth: "JRWP", // default access for authenticated users
      anon: "N" // default access for anonymous users
    },
    acs: {  // user's actual access permissions
      want: "JRWP", // string, requested access permission
      given: "JRWP", // string, granted access permission
    mode: "JRWP" // string, combination of want and given
    },
    seq: 123, // integer, server-issued id of the last {data} message
    read: 112, // integer, ID of the message user claims through {note} message
              // to have read, optional
    recv: 115, // integer, like 'read', but received, optional
    clear: 12, // integer, in case some messages were deleted, the greatest ID
               // of a deleted message, optional
    public: { ... }, // application-defined data that's available to all topic
                     // subscribers
    private: { ...} // application-defined data that's available to the current
                    // user only
  }, // object, topic description, optional
  sub:  [ // array of objects, topic subscribers or user's subscriptions, optional
    {
      user: "usr2il9suCbuko", // string, ID of the user this subscription
                            // describes, absent when querying 'me'.
      updated: "2015-10-24T10:26:09.716Z", // timestamp of the last change in the
                                           // subscription, present only for
                                           // requester's own subscriptions
      touched: "2017-11-02T09:13:55.530Z", // timestamp of the last message in the
                                           // topic (may also include other events
                                           // in the future, such as new subscribers)
      acs: {  // user's access permissions
        want: "JRWP", // string, requested access permission, present for user's own
              // subscriptions and when the requester is topic's manager or owner
        given: "JRWP", // string, granted access permission, optional exactly as 'want'
        mode: "JRWP" // string, combination of want and given
      },
      read: 112, // integer, ID of the message user claims through {note} message
                 // to have read, optional.
      recv: 315, // integer, like 'read', but received, optional.
      clear: 12, // integer, in case some messages were deleted, the greatest ID
                 // of a deleted message, optional.
      public: { ... }, // application-defined user's 'public' object, absent when
                       // querying P2P topics.
      private: { ... } // application-defined user's 'private' object.
      online: true, // boolean, current online status of the user; if this is a
                    // group or a p2p topic, it's user's online status in the topic,
                    // i.e. if the user is attached and listening to messages; if this
                    // is a response to a 'me' query, it tells if the topic is
                    // online; p2p is considered online if the other party is
                    // online, not necessarily attached to topic; a group topic
                    // is considered online if it has at least one active
                    // subscriber.

      // The following fields are present only when querying 'me' topic

      topic: "grp1XUtEhjv6HND", // string, topic this subscription describes
      seq: 321, // integer, server-issued id of the last {data} message

      // The following field is present only when querying 'me' topic and the
      // topic described is a P2P topic
      seen: { // object, if this is a P2P topic, info on when the peer was last
              //online
        when: "2015-10-24T10:26:09.716Z", // timestamp
        ua: "Tinode/1.0 (Android 5.1)" // string, user agent of peer's client
      }
    },
    ...
  ],
  tags: [ // array of tags that the topic or user (in case of "me" topic) is indexed by
    "email:alice@example.com", "tel:+1234567890"
  ],
  cred: [ // array of user's credentials
    {
      meth: "email", // string, validation method
      val: "alice@example.com", // string, credential value
      done: true     // validation status
    },
    ...
  ],
  del: {
    clear: 3, // ID of the latest applicable 'delete' transaction
    delseq: [{low: 15}, {low: 22, hi: 28}, ...], // ranges of IDs of deleted messages
  }
}
```

#### `{pres}`

Tinode uses `{pres}` message to inform clients of important events. A separate [document](https://docs.google.com/spreadsheets/d/e/2PACX-1vStUDHb7DPrD8tF5eANLu4YIjRkqta8KOhLvcj2precsjqR40eDHvJnnuuS3bw-NcWsP1QKc7GSTYuX/pubhtml?gid=1959642482&single=true) explains all possible use cases.

```js
pres: {
  topic: "me", // string, topic which receives the notification, always present
  src: "grp1XUtEhjv6HND", // string, topic or user affected by the change, always present
  what: "on", // string, what's changed, always present
  seq: 123, // integer, "what" is "msg", a server-issued ID of the message,
            // optional
  clear: 15, // integer, "what" is "del", an update to the delete transaction ID.
  delseq: [{low: 123}, {low: 126, hi: 136}], // array of ranges, "what" is "del",
             // ranges of IDs of deleted messages, optional
  ua: "Tinode/1.0 (Android 2.2)", // string, a User Agent string identifying client
             // software if "what" is "on" or "ua", optional
  act: "usr2il9suCbuko",  // string, user who performed the action, optional
  tgt: "usrRkDVe0PYDOo",  // string, user affected by the action, optional
  acs: {want: "+AS-D", given: "+S"} // object, changes to access mode, "what" is "acs",
                          // optional
}
```

The `{pres}` messages are purely transient: they are not stored and no attempt is made to deliver them later if the destination is temporarily unavailable.

Timestamp is not present in `{pres}` messages.


#### `{info}`

Forwarded client-generated notification `{note}`. Server guarantees that the message complies with this specification and that content of `topic` and `from` fields is correct. The other content is copied from the `{note}` message verbatim and may potentially be incorrect or misleading if the originator  so desires.

```js
info: {
  topic: "grp1XUtEhjv6HND", // string, topic affected, always present
  from: "usr2il9suCbuko", // string, id of the user who published the
                          // message, always present
  what: "read", // string, one of "kp", "recv", "read", see client-side {note},
                // always present
  seq: 123, // integer, ID of the message that client has acknowledged,
            // guaranteed 0 < read <= recv <= {ctrl.params.seq}; present for rcpt &
            // read
}
```
