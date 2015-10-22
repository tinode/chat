/*

{pub} send content intended for topic subscribers
{meta} manipulates topic state, not delivered to topic subscribers directly

CRUD

CRUD is needed for user management (creating/reading/updating own profiles, admins managing profiles of other users)
 Users:
	C - yes, by {pub} to "!sys.user" (must {sub} to {pub})
	R - yes, self - fully by sub to "!me", others - partially by !pres?
	U - yes (except some fields) by {pub} to "!sys"?
	D - ?
 Messages:
	C - yes, by {pub} to a topic
	R - yes, reading message archive by {meta} to topic
	U - no, messages are immutable
	D - ?
 Topics
 	C - yes, by {sub} to "!new" (treat it as a shortcut of [{pub} to "!make.topic", then {sub} to the new topic])
	    create anything by {pub} to "!make.X"?
	R - yes, reading the list of topics of interest by {meta browse} to "!pres"
	U - yes, changing ACL (sharing), by {meta share} to topic
	D - yes, by {meta} or by {unsub} with {"params": {"delete": true}}, garbage collection too?
 Topic invites invites (contacts):
	C - yes, invite loop
	 Case 1: user A wants to initiate a p2p conversation or to subscribe to a topic WXY
	   1. user A initiates invite loop by {sub} to "WXY" or "!new:B"
	   2. user A gets {ctrl code=1xx} akn from WXY or p2p:A.B that the request is pending approval
	   3. topic owner B or user B gets {meta} message from !me with a requested access level for WXY or p2p:A.B
	   4. user B responds by {meta} to "WXY" or "!p2p:A.B" with a UID of A and granted ACL
	   5. user A gets {ctrl code=XXX} from WXY or !p2p:A.B, and either considered to be subscribed or subscription is rejected
	   6. multiple invites to the same user/topic are possible
     Alternative case 1: user A wants to initiate a p2p conversation or to subscribe to a topic WXY
	   1. user A sends {sub} to "WXY" or "!new:B"
	   2. user A gets {ctrl code=4xxx} rejected from WXY or p2p:A.B that access is not allowed
	   3  User A sends {meta} to topic requesting access
	   3. topic owner B or user B gets {meta} message from !me with a requested access level for WXY or p2p:A.B
	   4. user B responds by {meta} to "WXY" or "!p2p:A.B" with a UID of A and granted ACL
	   5. user A gets {meta} from !me that access is granted/request rejected
	   6. user A can now {sub} to topic
	 Case 2: user B wants user A to subscribe to topic WXY
	   4. user B sends {meta} to "WXY" with UID of A and granted access level
	   5. user A gets {meta} from "!me" with WXY and granted access level, A can now subscribe to WXY
	   6. multiple invites to the same user/topic are possible
	R - yes, {meta} to !pres or !invite?
	U - maybe, change access level: "can message me": Y/N, "can see online status": Y/N
	D - yes, user is no longer of interest.

Manage CRUD through dedicated topic(s):
 * All CRUD requests go through a single Topic [!meta] or [!sys]
 * Individual topics for each type of data, [!sys.users] or [!users]


Pass action/method in Params, payload, if any, in Data

Inbox handling

1. How to deal with topics?
  * Treat inbox as a list of "topics of interest", don't automatically subscribe to them.
    Like contacts are "users of interest".

Topics
1. If topics have persistent parameters, which must survive server restarts, there should be a way to restore topic
  Load existing topic on first subscribe, unload/remove topic after a timeout

2. How to handle messages, accumulated on the topic while the user was away
 * Topics must be made persistent: a user can query topic's unread count, manage the access control list
 * Need a way to notify user if a topic got pending messages
   * Maybe treat contacts and inbox as one and the same? Use !pres as a notification channel
    "something happened in the topic of interest".
   * create channel for topic management [!meta] or [!sys] or even [!pres]? Maybe use the same topic for contacts?
 * User explicitly states what he wants to receive on {sub}:
   * unread count (define), ts of the most recent message, ts of the first unread, actual messages (all or some),
     number of subscribers
 * How to request messages (and topic stats)?
     * use a new {meta} client to server message
     * pagination in Params like {Since: Time, Before: Time, Limit: Count} or  {Between: [Time, Time], Limit: count}
	 * format of multiple messages, maybe just [msg, msg, ...]?

3. Topics:
  * channel to send and receive content;
  * label on notifications regarding topic's content changes (when the topic is not currently subscribed)

3.1. Be explicit about subscription to a topic: content + notifications, or notifications only?
  * Maybe send all topic notifications on !pres? Even "topic is online"?

4. Double opt-in for contacts (adding someone as contact is the same as subscribing to his/her presence,
must get publisher's concsent), otherwise presence could be leaked
  * Invitations to subscribe
  * Request for permission to subscribe
  * Contact:
    a. can write to me: y/n  A->B, B->A
	b. can read my status: y/n; i want to read his status: y/n; A->B, B->A
  * Store p2p links as topics, store the ACL
    * that means multiple topic owners

5. All possible topic permissions:
  * Can read/{sub}, can write/{pub}, can write only if subscribed ({sub} to {pub}), can manage ACL - invite/ban others,
	can delete, can change permissions, can get presence notifications
*/
package main
