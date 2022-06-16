---
name: Bug report
about: Create a report to help us improve Tinode
title: ''
labels: 'bug'
assignees: ''

---

**If you are not reporting a bug, please post to https://groups.google.com/d/forum/tinode instead.**
---

### Subject of the issue
Describe your issue here.

### Your environment
#### Server-side
- [ ] web.tinode.co, api.tinode.co
- [ ] sandbox.tinode.co
- [ ] Your own setup:
  * platform (Windows, Mac, Linux etc)
  * version of Tinode server, e.g. `0.15.2-rc3`
  * database backend
  * cluster or standalone

#### Client-side
- [ ] TinodeWeb/tinodejs: javascript client
  * Browser make and version.
  * IMPORTANT! Use `index-dev.html` to reproduce the problem, not `index.html`.
- [ ] Tindroid: Android app
  * Android API level (e.g. 25).
  * Emulator or hardware, if hardware describe it.
- [ ] Tinodios: iOS app
  * iOS version
  * Simulator or hardware, if hardware describe it.
- [ ] tn-cli
  * Python version
- [ ] Chatbot
  * Python version
- Version of the client, e.g. `0.15.1`
- [ ] Your own client. Describe it:
  * Transport (gRPC, websocket, long polling)
  * Programming language.
  * gRPC version, if applicable.


### Steps to reproduce
Tell us how to reproduce this issue.

### Expected behaviour
Tell us what should happen.

### Actual behaviour
Tell us what happens instead.

### Server-side log
Copy server-side log here. You may also attach it to the issue as a file.

### Client-side log
Copy client-side log here (Android logcat, Javascript console, etc). You may also attach it to the issue as a file. When posting console log from Webapp, please use `index-dev.html`, not `index.html`; `index.html` uses minified javascript which produces unusable logs).
