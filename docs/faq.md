# Frequently Asked Questions

### Q: Where can I find server logs when running in Docker?<br/>
**A**: The log is in the container at `/var/log/tinode.log`. Attach to a running container with command
```
docker exec -it name-of-the-running-container /bin/bash
```
Then, for instance, see the log with `tail -50 /var/log/tinode.log`

If the container has stopped already, you can copy the log out of the container (saving it to `./tinode.log`):
```
docker cp name-of-the-container:/var/log/tinode.log ./tinode.log
```

Alternatively, you can instruct the docker container to save the logs to a directory on the host by mapping a host directory to `/var/log/` in the container. Add `-v /where/to/save/logs:/var/log` to the `docker run` command.


### Q: What are the options for enabling push notifications?<br/>
**A**: You can use Tinode Push Gateway (TNPG) or you can use Google FCM:
 * _Tinode Push Gateway_ requires minimum configuration changes by sending pushes on behalf of Tinode.
 * _Google FCM_ does not rely on Tinode infrastructure for pushes but requires you to build your own mobile apps (iOS and Android).


### Q: How to setup push notifications with Tinode Push Gateway?<br/>
**A**: Enabling TNPG push notifications requires two steps:
 * register at console.tinode.co and obtain a TNPG token.
 * configure server with the token.
See detailed instructions [here](../server/push/tnpg/).


### Q: How to setup push notifications with Google FCM?<br/>
**A**: This option requires you to build and release your own mobile apps. If you do not want to do it, the the TNPG option above.

Enabling FCM push notifications requires the following steps:
 * enable push sending from the server
 * enable receiving pushes in the clients

#### Server and TinodeWeb

1. Create a project at https://firebase.google.com/ if you have not done so already.
2. Follow instructions at https://cloud.google.com/iam/docs/creating-managing-service-account-keys to download the credentials file.
3. Update the server config [`tinode.conf`](../server/tinode.conf#L255), section `"push"` -> `"name": "fcm"`. Do _ONE_ of the following:
  * _Either_ enter the path to the downloaded credentials file into `"credentials_file"`.
  * _OR_ copy the file contents to `"credentials"`.<br/><br/>
    Remove the other entry. I.e. if you have updated `"credentials_file"`, remove `"credentials"` and vice versa.
4. Update [TinodeWeb](/tinode/webapp/) config [`firebase-init.js`](https://github.com/tinode/webapp/blob/master/firebase-init.js): update `apiKey`, `messagingSenderId`, `projectId`, `appId`, `messagingVapidKey`. See more info at https://github.com/tinode/webapp/#push_notifications

#### iOS and Android
1. If you are using an Android client, add `google-services.json` to [Tindroid](/tinode/tindroid/) by following instructions at https://developers.google.com/android/guides/google-services-plugin and recompile the client. You may also optionally submit it to Google Play Store.
See more info at https://github.com/tinode/tindroid/#push_notifications
2. If you are using an iOS client, add `GoogleService-Info.plist` to [Tinodios](/tinode/ios/) by following instructions at https://firebase.google.com/docs/cloud-messaging/ios/client) and recompile the client. You may optionally submit the app to Apple AppStore.
See more info at https://github.com/tinode/ios/#push_notifications


### Q: How to add new users?<br/>
**A**: There are three ways to create accounts:
* A user can create a new account using one of the applications (web, Android, iOS).
* A new account can be created using [tn-cli](../tn-cli/) (`acc` command or `useradd` macro). The process can be scripted.
* If the user already exists in an external database, the Tinode account can be automatically created on the first login using the [rest authenticator](../server/auth/rest/).


### Q: How to create a `root` user?<br/>
**A**: Starting with Tinode version 0.18 the `root` access can be granted to a user by running the following command:
```sh
./tinode-db -auth=ROOT -uid=usrAbcDef123 -scheme=basic
```
Where `usrAbcDef123` is the ID of the user to update.

In version 0.17 and older the `root` access can be granted to a user only by executing a database query.
First create or choose the user you want to promote to `root` then execute the query:
* RethinkDB:
```js
r.db('tinode').table('auth').get('basic:login-of-the-user-to-make-root').update({authLvl: 30})
```
* MySQL:
```sql
USE 'tinode';
UPDATE auth SET authlvl=30 WHERE uname='basic:login-of-the-user-to-make-root';
```
* MongoDB:
```js
db.getCollection('auth').updateOne({_id: 'basic:login-of-the-user-to-make-root'}, {$set: {authlvl: 30}})
```
The test database has a stock user `xena` which has root access.


### Q: Once the number of network connections reaches about 1000 per node, all kinds of problems start. Is this a bug?<br/>
**A**: It is likely not a bug. To ensure good server performance Linux limits the total number of open file descriptors (live network connections, open files) for each process at the kernel level. The default limit is usually 1024. There are other possible restrictions on the number of file descriptors. The problems you are experiencing are likely caused by exceeding one of the Linux-imposed limits. Please seek assistance of a system administrator.


### Q: What is the difference between a group topic and a channel?<br/>
**A**: Channel is a special case of a group topic. Normal group topics allow limited number of subscribers (128 by default). Each subscriber can be managed individually: invited, removed, banned, promoted to administrator or owner, other access permissions can be personally adjusted. Group topics with enabled channel functionality additionally permit an unlimited number of `readers`. The readers have read-only access to the topic, they cannot be managed individually, cannot be invited or removed, they cannot post messages. Readers do not generate presence notifications when joining or un-joining the topic and do not receive presence notifications from normal group members. Readers receive channel messages with `From` field set to `null`, i.e. they do not know who personally posted any given message to the channel. Readers cannot delete channel messages.


### Q: What is the proper way to format gRPC {pub content}?<br/>
**A**: The gPRC sends `content` field of a `{pub}` message as a byte array while the client applications expect it to be valid JSON. Consequently, you have to format the field to be valid JSON before passing it to gRPC. For example, to send a plain text `Hello world` message you have to send a quoted string `"Hello world"`. In most cases the string you pass to the gRPC call would look like `"\"Hello world\""` or `'"Hello world"'`.
