# Tinode Instant Messaging Server

<img src="docs/logo.svg" align="left" width=128 height=128> Instant messaging server. Backend in pure [Go](http://golang.org) (license [GPL 3.0](http://www.gnu.org/licenses/gpl-3.0.en.html)), client-side binding in Java, Javascript, and Swift, as well as [gRPC](https://grpc.io/) client support for C++, C#, Go, Java, Node, PHP, Python, Ruby, Objective-C, etc. (license [Apache 2.0](http://www.apache.org/licenses/LICENSE-2.0)). Wire transport is JSON over websocket (long polling is also available) for custom bindings, or [protobuf](https://developers.google.com/protocol-buffers/) with gRPC. Persistent storage is any one of MySQL, MongoDB or [RethinkDB](http://rethinkdb.com/). Other databases can be supported by writing custom adapters.

Tinode is *not* XMPP/Jabber. It is *not* compatible with XMPP. It's meant as a replacement for XMPP. On the surface, it's a lot like open source WhatsApp or Telegram.

Version 0.20. This is beta-quality software: feature-complete and stable but probably with a few bugs or missing features. Follow [instructions](INSTALL.md) to install and run or use one of the cloud services below. Read [API documentation](docs/API.md).

<a href="https://apps.apple.com/us/app/tinode/id1483763538"><img src="docs/app-store.svg" height=36></a> <a href="https://play.google.com/store/apps/details?id=co.tinode.tindroidx"><img src="docs/play-store.svg" height=36></a> <a href="https://web.tinode.co/"><img src="docs/web-app.svg" height=36></a>

## Why?

The promise of [XMPP](http://xmpp.org/) was to deliver federated instant messaging: anyone would be able to spin up an IM server capable of exchanging messages with any other XMPP server in the world. Unfortunately, XMPP never delivered on this promise. Instant messengers are still a bunch of incompatible walled gardens, similar to what AoL of the late 1990s was to the open Internet.

The goal of this project is to deliver on XMPP's original vision: create a modern open platform for federated instant messaging with an emphasis on mobile communication. A secondary goal is to create a decentralized IM platform that is much harder to track and block by the governments.

An explicit NON-goal: we are not building yet another Slack replacement.

## Installing and running

See [general instructions](./INSTALL.md) or [docker-specific instructions](./docker/README.md).

## Getting support

* Read [API documentation](docs/API.md) and [FAQ](docs/faq.md). Read configuration instructions contained in the [`tinode.conf`](./server/tinode.conf) file.
* For support, general questions, discussions post to [https://groups.google.com/d/forum/tinode](https://groups.google.com/d/forum/tinode).
* For bugs and feature requests [open an issue](https://github.com/tinode/chat/issues/new/choose).
* Use https://tinode.co/contact for commercial inquiries.

## Helping out

* If you appreciate our work, please help spread the word! Sharing on Reddit, HN, and other communities helps more than you think.
* Consider buying paid support: https://tinode.co/support.html
* If you are a software developer, send us your pull requests with bug fixes and new features.
* If you use the app and discover bugs or missing features, let us know by filing bug reports and feature requests. Vote for existing [feature requests](https://github.com/tinode/chat/issues?q=is%3Aissue+is%3Aopen+sort%3Areactions-%2B1-desc+label%3A%22feature+request%22) you find most valuable.
* If you speak a language other than English, [translate](docs/translations.md) the apps into your language. You may also review and improve existing translations.
* If you are a UI/UX expert, help us polish the app UI.
* Use it: install it for your colleagues or friends at work or at home.

## Public service

A [public Tinode service](https://web.tinode.co/) is now available. You can use it just like any other instant messenger. Keep in mind that demo accounts present in [sandbox](https://sandbox.tinode.co/) are not available in the public service. You must register an account using valid email in order to use the service.

### Web

TinodeWeb, a single page web app, is available at https://web.tinode.co/ ([source](https://github.com/tinode/webapp/)). See screenshots below.

### Android

[Tinode for Android](https://play.google.com/store/apps/details?id=co.tinode.tindroidx) a.k.a Tindroid is stable and functional ([source](https://github.com/tinode/tindroid)). See the screenshots below. A [debug APK](https://github.com/tinode/tindroid/releases/latest) is also provided for convenience.

### iOS

[Tinode for iOS](https://apps.apple.com/us/app/tinode/id1483763538) a.k.a. Tinodios is stable and functional ([source](https://github.com/tinode/ios)). See the screenshots below.


## Demo/Sandbox

A sandboxed demo service is available at https://sandbox.tinode.co/.

Log in as one of `alice`, `bob`, `carol`, `dave`, `frank`. Password is `<login>123`, e.g. login for `alice` is `alice123`. You can discover other users by email or phone by prefixing them with `email:` or `tel:` respectively. Emails are `<login>@example.com`, e.g. `alice@example.com`, phones are `+17025550001` through `+17025550009`.

When you register a new account you are asked for an email address to send validation code to. For demo purposes you may use `123456` as a universal validation code. The code you get in the email is also valid.

### Sandbox Notes

* The sandbox server is reset (all data wiped) every night at 3:15am Pacific time. An error message `User not found or offline` means the server was reset while you were connected. If you see it on the web, reload and relogin. On Android log out and re-login. If the database was changed, delete the app then reinstall.
* Sandbox user `Tino` is a [basic chatbot](./chatbot) which responds with a [random quote](http://fortunes.cat-v.org/) to any message.
* As generally accepted, when you register a new account you are asked for an email address. The server will send an email with a verification code to that address and you can use it to validate the account. To make things easier for testing, the server will also accept `123456` as a verification code. Remove line `"debug_response": "123456"` from `tinode.conf` to disable this option.
* The sandbox server is configured to use [ACME](https://letsencrypt.org/) TLS [implementation](https://godoc.org/golang.org/x/crypto/acme) with hard-coded requirement for [SNI](https://en.wikipedia.org/wiki/Server_Name_Indication). If you are unable to connect then the most likely reason is your TLS client's missing support for SNI. Use a different client.
* The default web app loads a single minified javascript bundle and minified CSS. The un-minified version is also available at https://sandbox.tinode.co/index-dev.html
* [Docker images](https://hub.docker.com/u/tinode/) with the same demo are available.
* You are welcome to test your client software against the sandbox, hack it, etc. No DDoS-ing though please.

## Features

### Supported

* Multiple native platforms:
  * [Android](https://github.com/tinode/tindroid/) (Java)
  * [iOS](https://github.com/tinode/ios) (Swift)
  * [Web](https://github.com/tinode/webapp/) (React.js)
  * Scriptable [command line](tn-cli/) (Python)
* User features:
  * One-on-one and group messaging. Voice messages.
  * Video and audio calling.
  * Channels with unlimited number of read-only subscribers.
  * All chats are synchronized across all devices.
  * Granular access control with permissions for various actions.
  * User search/discovery.
  * Rich formatting of messages markdown-style: \*style\* &rarr; **style**, with inline images, file attachments.
  * Forms and templated responses suitable for chatbots.
  * Verified/staff/untrusted account markers.
  * Message status notifications: message delivery to server; received and read notifications; typing notifications.
  * Most recent message preview in contact list.
  * Server-generated presence notifications for people, group chats.
  * Forwarding and replying to messages.
* Administration:
  * Granular access control with permissions for various actions.
  * Support for custom authentication backends.
  * Ability to block unwanted communication server-side.
  * Anonymous users (important for use cases related to tech support over chat).
  * Plugins to extend functionality, for example, to support moderation or chatbots.
  * Scriptable [command-line tool](tn-cli/) for server administration.
* Performance, reliability and development:
  * Sharded clustering with failover.
  * Storage and out of band transfer of large objects like images or document files using local file system or Amazon S3 (other storage systems can be supported with plugins).
  * JSON or [protobuf version 3](https://developers.google.com/protocol-buffers/) wire protocols.
  * Bindings for various programming languages:
    * Javascript with no external dependencies.
    * Java with dependencies on [Jackson](https://github.com/FasterXML/jackson) and [Java-Websocket](https://github.com/TooTallNate/Java-WebSocket). Suitable for Android but with no Android SDK dependencies.
    * Swift with no external dependencies.
    * C/C++, C#, Go, Python, PHP, Ruby and many other languages using [gRPC](https://grpc.io/docs/languages/).
  * Choice of a database backend: MySQL, RethinkDB, MongoDB.

### Planned

* [Federation](https://en.wikipedia.org/wiki/Federation_(information_technology)).
* Location sharing.
* Previews of attached videos, documents, links.
* Hot standby.
* Different levels of message persistence (from strict persistence to "store until delivered" to purely ephemeral messaging).
* Message encryption at rest.
* End to end encryption with [OTR](https://en.wikipedia.org/wiki/Off-the-Record_Messaging) for one-on-one messaging and undecided method for group messaging.
* Full text search in messages.

### Translations

All client software has support for [internationalization](docs/translations.md). The following translations are provided:

| Language | Server | Webapp | Android | iOS |
| --- | :---: | :---: | :---: | :---: |
| English | &check; | &check; | &check; | &check; |
| Chinese (simplified) | &check; | &check; | &check; | &check; |
| Chinese (traditional) |   | &check; | &check; | &check; |
| French | &check; | &check; | &check; |   |
| German |   | &check; | &check; |   |
| Korean |   | &check; | &check; |   |
| Romanian |   | &check; |   |   |
| Russian | &check; | &check; | &check; | &check; |
| Spanish | &check; | &check; | &check; | &check; |
| Vietnamese | &check; |   |   |   |

More translations are [welcome](docs/translations.md). Particularly interested in Arabic, Vietnamese, Persian, Indonesian, Portuguese, Hindi, Bengali, Turkish.

## Third-Party

### Projects

* [Arango DB adapter](https://github.com/gfxlabs/chat/tree/master/server/db/arango)
* [DynamoDB adapter](https://github.com/riandyrn/chat/tree/master/server/db/dynamodb) (outdated)

### Licenses

* Demo avatars and some other graphics are from https://www.pexels.com/ under [CC0 license](https://www.pexels.com/photo-license/) and https://pixabay.com/ under their [license](https://pixabay.com/service/license/).
* Web and Android background patterns are from http://subtlepatterns.com/ under [CC BY-SA 3.0](https://creativecommons.org/licenses/by-sa/3.0/) license.
* Android icons are from https://material.io/tools/icons/ under [Apache 2.0](https://www.apache.org/licenses/LICENSE-2.0.html) license.

## Screenshots

### [Android](https://github.com/tinode/tindroid/)

<p align="center">
<img src="docs/android-contacts.png" alt="Android screenshot: list of chats" width=250 />
<img src="docs/android-chat.png" alt="Android screenshot: one conversation" width=250 />
<img src="docs/android-video-call.png" alt="Android screenshot: video call" width=250 />
</p>

### [iOS](https://github.com/tinode/ios)

<p align="center">
<img src="docs/ios-contacts.png" alt="iOS screenshot: list of chats" width=207 /> <img src="docs/ios-chat.png" alt="iOS screenshot: one conversation" width=207 /> <img src="docs/ios-acc-personal.png" alt="iOS screenshot: account settings" width="207" />
</p>

### [Desktop Web](https://github.com/tinode/webapp/)

<p align="center">
  <img src="docs/web-desktop.jpg" alt="Desktop web: full app" width=810 />
</p>

### [Mobile Web](https://github.com/tinode/webapp/)

<p align="center">
  <img src="docs/web-mob-contacts.png" alt="Mobile web: contacts" width=250 /> <img src="docs/web-mob-chat.png" alt="Mobile web: chat" width=250 /> <img src="docs/web-mob-video-call.png" alt="Mobile web: topic info" width=250 />
</p>


#### SEO Strings

Words 'chat' and 'instant messaging' in Chinese, Russian, Persian and a few other languages.

* 聊天室 即時通訊
* чат мессенджер
* インスタントメッセージ
* 인스턴트 메신저
* پیام رسان فوری
* تراسل فوري
* Nhắn tin tức thời
* anlık mesajlaşma sohbet
* mensageiro instantâneo
* pesan instan
* mensajería instantánea
* চ্যাট ইন্সট্যান্ট মেসেজিং
* चैट त्वरित संदेश
