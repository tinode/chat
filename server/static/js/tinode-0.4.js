/*****************************************************************************
 *
 * Copyright 2014-2015, Tinode, All Rights Reserved
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 *  File        :  tinode-0.3.js
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 *****************************************************************************
 *
 *  Description :
 *
 *  Common module methods and parameters
 *
 *****************************************************************************/

var Tinode = (function() {
	var tinode = {
		"PROTOVERSION": "0",
		"VERSION": "0.4",
		"TOPIC_NEW": "new",
		"TOPIC_ME": "me",
		"USER_NEW": "new"
	}

	var _logging_on = false

	/**
	 * Initialize Tinode module.
	 *
	 * @param apikey API key issued by Tinode
	 * @baseurl debugging only. It will be hardcoded in the release version as https://api.tinode.com/
	 */
	tinode.init = function(apikey, baseurl) {
		// API Key
		tinode.apikey = apikey
			// Authentication token returned by call to Login
		tinode.token = null
			// Expiration time of the current authentication token
		tinode.tokenExpires = null
			// UID of the current user
		tinode.myUID = null
			// API endpoint. This should be hardcoded in the future
			// tinode.baseUrl = "https://api.tinode.com/v0"
		var parser = document.createElement('a')
		parser.href = baseurl
		parser.pathname = parser.pathname || "/"
		parser.pathname += "v" + Tinode.PROTOVERSION
		tinode.baseUrl = parser.protocol + '//' + parser.host + parser.pathname +
			parser.search

		// Tinode-global cache for topics
		tinode.cache = {}
	}

	/**
	 * Basic cross-domain requester.
	 * Supports normal browsers and IE8+
	 */
	tinode.xdreq = function() {
		var xdreq = null

		// Detect browser support for CORS
		if ('withCredentials' in new XMLHttpRequest()) {
			// Support for standard cross-domain requests
			xdreq = new XMLHttpRequest()
		} else if (typeof XDomainRequest !== "undefined") {
			// IE-specific "CORS" with XDR
			xdreq = new XDomainRequest()
		} else {
			// Browser without CORS support, don't know how to handle
			throw new Error("browser not supported");
		}

		return xdreq
	}

	tinode.getCurrentUserID = function() {
		return tinode.myUID
	}

	tinode.getTokenExpiration = function() {
		return tinode.tokenExpires
	}

	tinode.getAuthToken = function() {
		return tinode.token
	}

	// Utility functions
	// rfc3339 formater of Date
	function _rfc3339DateString(d) {
		function pad(n) {
			return n < 10 ? '0' + n : n
		}
		return d.getUTCFullYear() + '-' + pad(d.getUTCMonth() + 1) + '-' + pad(d.getUTCDate()) + 'T' + pad(d.getUTCHours()) +
			':' + pad(d.getUTCMinutes()) + ':' + pad(d.getUTCSeconds()) + 'Z'
	}

	// JSON helper - pre-processor for JSON.stringify
	tinode._jsonHelper = function(key, val) {
		// strip out empty elements while serializing objects to JSON
		if (val == null || val.length === 0 ||
			((typeof val === "object") && (Object.keys(val).length === 0))) {
			return undefined
				// Convert javascript Date objects to rfc3339 strings
		} else if (val instanceof Date) {
			val = _rfc3339DateString(val)
		}
		return val
	}

	// Toggle console logging. Logging is off by default.
	tinode.logToConsole = function(val) {
		_logging_on = val
	}

	// Console logger. Turn on/off by calling Tinode.logToConsole
	tinode._logger = function(str) {
		if (_logging_on) {
			console.log(str)
		}
	}

	// Access to Tinode's cache of objects
	tinode.cachePut = function(type, name, obj) {
		tinode.cache[type + ":" + name] = obj
	}
	tinode.cacheGet = function(type, name) {
		return tinode.cache[type + ":" + name]
	}
	tinode.cacheDel = function(type, name) {
		delete tinode.cache[type + ":" + name]
	}

	// Reset topics to disconnected status
	tinode.resetConnected = function() {
		for (var idx in tinode.cache) {
			if (idx.indexOf("topic:") >= 0) {
				tinode.cache[idx].resetSub()
			}
		}
	}

	// Implementation of a basic circular buffer for caching messages
	tinode.CBuffer = function(size) {
		var pointer = 0,
			buffer = [],
			contains = 0

		return {
			get: function(at) {
				if (at >= contains || at < 0) return undefined
				return buffer[(at + pointer) % size]
			},
			// Variadic: takes one or more arguments. If a single array is passed, it's elements are
			// inserted individually
			put: function() {
				var insert
					// inspect arguments: if array, insert its elements, if one or more arguments, insert them one by one
				if (arguments.length == 1 && Array.isArray(arguments[0])) {
					insert = arguments[0]
				} else {
					insert = arguments
				}
				for (var idx in insert) {
					buffer[pointer] = insert[idx]
					pointer = (pointer + 1) % size
					contains = (contains == size ? size : contains + 1)
				}
			},
			size: function() {
				return size
			},
			contains: function() {
				return contains
			},
			reset: function(newSize) {
				if (newSize) {
					size = newSize
				}
				pointer = 0
				contains = 0
			},
			forEach: function(callback, context) {
				for (var i = 0; i < contains; i++) {
					if (callback(buffer[(i + pointer) % size], context)) {
						break
					}
				}
			}
		}
	}

	return tinode
})()

/*
 * Submodule for handling streaming connections. Websocket or long polling
 */
Tinode.streaming = (
	function() {
		var streaming = {}

		// Private variables
		// Websocket
		var _socket = null
			// Composed endpoint URL, with API key but without long polling SID
		var _channelURL = null
			// Url for long polling, complete with SID
		var _lpURL = null
			// Long polling xdreqs, one for long polling, the other for sending:
		var _poller = null
		var _sender = null
			// counter of received packets
		var _inPacketCount = 0

		/**
		 * Initialize the module.
		 *
		 * @param transport "lp" for long polling or "ws" (default) for websocket
		 */
		streaming.init = function(transport) {
			// Counter used for generation of message IDs
			streaming.messageId = 0

			// Callbacks:
			// Websocket opened
			streaming.onSocketOpen = null
				// Connection completed, sucess or failure
				// @code -- result code
				// @text -- "OK" or text of an error message
				// @params -- connection parametes
			streaming.onConnect = null
				// Login completed
			streaming.onLogin = null
				// Control message received
			streaming.onCtrlMessage = null
				// content message received
			streaming.onDataMessage = null
				// presence message received
			streaming.onPresMessage = null
				// Complete data packet, as object
			streaming.onMessage = null
				// Unparsed data as text
			streaming.onRawMessage = null
				// connection closed
			streaming.onDisconnect = null

			// Presence notifications
			streaming.onPresenceChange = null

			// Request echo packets
			//streaming.Echo = false;

			// Cache of outstanding promises; key is a message Id, value is a Promise
			// Every request to the server returns a Promise
			streaming.pending = {}

			if (transport === "lp") {
				// explicit request to use long polling
				init_lp()
			} else if (transport === "ws") {
				// explicit request to use web socket
				// if websockets are not available, horrible things will happen
				init_ws();
			} else {
				// Default transport selection
				if (!window["WebSocket"]) {
					// The browser has no websockets
					init_lp();
				} else {
					// Using web sockets -- default
					init_ws();
				}
			}
		}

		// Resolve or reject a pending promise.
		// Pending promises are stored in streaming.pending
		function promiseExec(id, code, onOK, errorText) {
			var callbacks = streaming.pending[id]
			if (callbacks) {
				delete streaming.pending[id]
				if (code >= 200 && code < 400) {
					if (callbacks.resolve) {
						callbacks.resolve(onOK)
					}
				} else if (callbacks.reject) {
					callbacks.reject(new Error("" + code + " " + errorText))
				}
			}
		}

		function get_message_id() {
			if (streaming.messageId != 0) {
				return "" + streaming.messageId++
			}
			return undefined
		}

		// Generator of client-side packets
		function make_packet(type, topic) {
			var pkt = null
			switch (type) {
				case "acc":
					return {
						"acc": {
							"id": get_message_id(),
							"user": null,
							"auth": [],
							"login": null,
							"init": {}
						}
					}

				case "login":
					return {
						"login": {
							"id": get_message_id(),
							"scheme": null,
							"secret": null,
							"expireIn": null
						}
					}

				case "sub":
					return {
						"sub": {
							"id": get_message_id(),
							"topic": topic,
							"mode": null,
							"init": {},
							"browse": {}
						}
					}

				case "leave":
					return {
						"leave": {
							"id": get_message_id(),
							"topic": topic,
							"unsub": false
						}
					}

				case "pub":
					return {
						"pub": {
							"id": get_message_id(),
							"topic": topic,
							"params": {},
							"content": {}
						}
					}

				case "get":
					return {
						"get": {
							"id": get_message_id(),
							"topic": topic,
							"what": null, // data, sub, info, space separated list; unknown strings are ignored
							"browse": {}
						}
					}

				case "set":
					return {
						"set": {
							"id": get_message_id(),
							"topic": topic,
							"what": null,
							"info": {},
							"sub": {}
						}
					}

				case "del":
					return {
						"del": {
							"id": get_message_id(),
							"topic": topic
						}
					}

				default:
					throw new Error("unknown packet type requested: " + type)
			}
		}

		// Helper function for creating endpoint URL
		function channel_url(url, protocol) {
			var parser = document.createElement('a')
			parser.href = url
			parser.pathname += "/channels"
			if (protocol === "http" || protocol === "https") {
				// Long polling endpoint end with "lp", i.e.
				// '/v0/channels/lp' vs just '/v0/channels'
				parser.pathname += "/lp"
			}
			parser.search = (parser.search ? parser.search : "?") + "apikey=" + Tinode.apikey
			return protocol + '://' + parser.host + parser.pathname + parser.search
		}

		// Message dispatcher
		function on_message(data) {
			// Skip empty response. This happens when LP times out.
			if (!data) return

			_inPacketCount++

			Tinode._logger("in: " + data)

			// Send raw message to listener
			if (streaming.onRawMessage) {
				streaming.onRawMessage(data)
			}

			var pkt = JSON.parse(data, function(key, val) {
				// Convert timestamps with optional milliseconds 2015-09-02T01:45:43[.123]Z
				if (key === "ts" && typeof val == "string" && val.length >= 20 && val.length <= 24) {
					var date = new Date(val)
					if (date) {
						return date
					}
				}
				return val
			})
			if (!pkt) {
				Tinode._logger("ERROR: failed to parse data '" + data + "'")
			} else {
				// Send complete packet to listener
				if (streaming.onMessage) {
					streaming.onMessage(pkt)
				}

				if (pkt.ctrl != null) {
					// Handling ctrl message
					if (streaming.onCtrlMessage) {
						streaming.onCtrlMessage(pkt.ctrl)
					}

					// Resolve or reject a pending promise, if any
					if (pkt.ctrl.id) {
						promiseExec(pkt.ctrl.id, pkt.ctrl.code, pkt.ctrl, pkt.ctrl.text)
					}

					if (_inPacketCount == 1) {
						// The very first incoming packet. This is a response to the connection attempt
						if (streaming.onConnect) {
							streaming.onConnect(pkt.ctrl.code, pkt.ctrl.text, pkt.ctrl.params)
						}
					}
				} else if (pkt.meta != null) {
					// Handling meta message.

					// Preferred API: Route meta to topic, if one is registered
					var topic = Tinode.cacheGet("topic", pkt.meta.topic)
					if (topic) {
						topic.routeMeta(pkt.meta)
					}

					// Secondary API: callback
					if (streaming.onMetaMessage) {
						streaming.onMetaMessage(pkt.meta)
					}
				} else if (pkt.data != null) {
					// Handling data message

					// Preferred API: Route data to topic, if one is registered
					var topic = Tinode.cacheGet("topic", pkt.data.topic)
					if (topic) {
						topic.routeData(pkt.data)
					}

					// Secondary API: Call callback
					if (streaming.onDataMessage) {
						streaming.onDataMessage(pkt.data)
					}
				} else if (pkt.pres != null) {
					// Handling pres message

					// Preferred API: Route presence to topic, if one is registered
					var topic = Tinode.cacheGet("topic", pkt.pres.topic)
					if (topic) {
						topic.routePres(pkt.pres)
					}

					// Secondary API - callback
					if (streaming.onPresMessage) {
						streaming.onPresMessage(pkt.pres)
					}
				} else {
					Tinode._logger("unknown packet received")
				}
			}
		}

		// Generator of default promises for sent packets
		var promiseMaker = function(id) {
			var promise = null
			if (id) {
				var promise = new Promise(function(resolve, reject) {
					// Stored callbacks will be called when the response packet with this Id arrives
					streaming.pending[id] = {
						"resolve": resolve,
						"reject": reject
					}
				})
			}
			return promise
		}

		streaming.CreateUser = function(auth, params) {
			var pkt = make_packet("acc")
			pkt.acc.user = Tinode.USER_NEW
			for (var idx in auth) {
				if (auth[idx].scheme && auth[idx].secret) {
					pkt.acc.auth.push({
						"scheme": auth[idx].scheme,
						"secret": auth[idx].secret
					})
				}
			}
			if (params) {
				if (params.login) {
					pkt.acc.login = scheme
				}
				pkt.acc.init.defacs = params.acs
				pkt.acc.init.public = params.public
				pkt.acc.init.private = params.private
			}

			// Setup promise
			var promise = promiseMaker(pkt.acc.id)
			streaming._send(pkt);
			return promise
		}

		streaming.CreateUserBasic = function(username, password, params) {
			return streaming.CreateUser([{
				"basic": username + ":" + password
			}], params)
		}

		// Authenticate current session
		// 	@authentication scheme. "basic" is the only currently supported scheme
		//	@secret	-- authentication secret
		streaming.Login = function(scheme, secret) {
			var pkt = make_packet("login")
			pkt.login.scheme = scheme
			pkt.login.secret = secret
			pkt.login.expireIn = "24h"

			// Setup promise
			var promise = null
			if (pkt.login.id) {
				var promise = new Promise(function(resolve, reject) {
					// Stored callbacks will be called when the response packet with this Id arrives
					streaming.pending[pkt.login.id] = {
						"resolve": function(ctrl) {
							// This is a response to a successful login, extract UID and security token, save it in Tinode module
							Tinode.token = ctrl.params.token
							Tinode.myUID = ctrl.params.uid
							Tinode.tokenExpires = new Date(ctrl.params.expires)

							if (streaming.onLogin) {
								streaming.onLogin(ctrl.code, ctrl.text)
							}

							if (resolve) {
								resolve(ctrl)
							}
						},
						"reject": reject
					}
				})
			}

			streaming._send(pkt);
			return promise
		}

		// Wrapper for Login with basic authentication
		// @uname -- user name
		// @password -- self explanatory
		streaming.LoginBasic = function(uname, password) {
			return streaming.Login("basic", uname + ":" + password)
		}

		// Send a subscription request to topic
		// 	@topic -- topic name to subscribe to
		// 	@params -- optional object with request parameters:
		//     @params.init -- initializing parameters for new topics. See streaming.Set
		//		 @params.sub
		//     @params.mode -- access mode, optional
		//     @params.get -- list of data to fetch, see streaming.Get
		//     @params.browse -- optional parameters for get.data. See streaming.Get
		streaming.Subscribe = function(topic, params) {
			var pkt = make_packet("sub", topic)
			if (params) {
				pkt.sub.get = params.get
				if (params.sub) {
					pkt.sub.sub.mode = params.sub.mode
					pkt.sub.sub.info = params.sub.info
				}
				// .init params are used for new topics only
				if (topic === Tinode.TOPIC_NEW) {
					pkt.sub.init.defacs = params.init.acs
					pkt.sub.init.public = params.init.public
					pkt.sub.init.private = params.init.private
				} else {
					// browse makes sense only in context of an existing topic
					pkt.sub.browse = params.browse
				}
			}

			// Setup promise
			var promise = promiseMaker(pkt.sub.id)
			streaming._send(pkt);
			return promise
		}

		streaming.Leave = function(topic, unsub) {
			var pkt = make_packet("leave", topic)
			pkt.leave.unsub = unsub

			var promise = promiseMaker(pkt.leave.id)
			streaming._send(pkt);
			return promise
		}

		streaming.Publish = function(topic, data, params) {
			var pkt = make_packet("pub", topic)
			if (params) {
				pkt.pub.params = params
			}
			pkt.pub.content = data

			var promise = promiseMaker(pkt.pub.id)
			streaming._send(pkt);
			return promise
		}

		streaming.Get = function(topic, what, browse) {
			var pkt = make_packet("get", topic)
			pkt.get.what = (what || "info")
			pkt.get.browse = browse

			var promise = promiseMaker(pkt.get.id)
			streaming._send(pkt);
			return promise
		}

		// Update topic's metadata: description (info), subscribtions (sub), or delete messages (del)
		// @topic: topic to Update
		// @params:
		// 	@params.info: update to topic description
		//  @params.sub: update to a subscription
		streaming.Set = function(topic, params) {
			var pkt = make_packet("set", topic)
			var what = []

			if (params) {
				if ((typeof params.info === "object") && (Object.keys(params.info).length != 0)) {
					what.push("info")

					pkt.set.info.defacs = params.info.acs
					pkt.set.info.public = params.info.public
					pkt.set.info.private = params.info.private
				}
				if ((typeof params.sub === "object") && (Object.keys(params.sub).length != 0)) {
					what.push("sub")

					pkt.set.sub.user = params.sub.user
					pkt.set.sub.mode = params.sub.mode
					pkt.set.sub.info = params.sub.info
				}
			}

			if (what.length > 0) {
				pkt.set.what = what.join(" ")
			} else {
				throw new Error("invalid parameters")
			}

			var promise = promiseMaker(pkt.set.id)
			streaming._send(pkt);
			return promise
		}

		streaming.DelMessages = function(topic, params) {
			var pkt = make_packet("del", topic)

			pkt.del.what = "msg"
			pkt.del.before = params.before
			pkt.del.hard = params.hard

			var promise = promiseMaker(pkt.del.id)
			streaming._send(pkt);
			return promise
		}

		streaming.DelTopic = function(topic) {
			var pkt = make_packet("del", topic)
			pkt.del.what = "topic"

			var promise = promiseMaker(pkt.del.id)
			streaming._send(pkt);
			return promise
		}


		// Initialization for Websocket
		function init_ws() {
			_socket = null

			streaming.Connect = function(secure) {
				_channelURL = channel_url(Tinode.baseUrl, secure ? "wss" : "ws")
				Tinode._logger("Connecting to: " + _channelURL)
				var conn = new WebSocket(_channelURL)

				conn.onopen = function(evt) {
					if (streaming.onSocketOpen) {
						streaming.onSocketOpen()
					}
				}

				conn.onclose = function(evt) {
					_socket = null
					_inPacketCount = 0
					Tinode.resetConnected()

					if (streaming.onDisconnect) {
						streaming.onDisconnect()
					}
				}

				conn.onmessage = function(evt) {
					on_message(evt.data)
				}

				_socket = conn
			}

			streaming.Disconnect = function() {
				if (_socket) {
					_socket.close()
				}
			}

			streaming._send = function(pkt) {
				var msg = JSON.stringify(pkt, Tinode._jsonHelper)
				Tinode._logger("out: " + msg)

				if (_socket && (_socket.readyState == _socket.OPEN)) {
					_socket.send(msg);
				} else {
					throw new Error("websocket not connected")
				}
			}
		}

		function init_lp() {
			// Fully composed endpoint URL, with key & SID
			_lpURL = null

			_poller = null
			_sender = null

			function lp_sender(url) {
				var sender = Tinode.xdreq()
				sender.open('POST', url, true);

				sender.onreadystatechange = function(evt) {
					if (sender.readyState == 4 && sender.status >= 400) {
						// Some sort of error response
						Tinode._logger("ERROR: lp sender failed, " + sender.status)
					}
				}

				return sender
			}

			function lp_poller(url) {
				var poller = Tinode.xdreq()
				poller.open('GET', url, true);

				poller.onreadystatechange = function(evt) {
					if (poller.readyState == 4) { // 4 == DONE
						if (poller.status == 201) { // 201 == HTTP.Created, get SID
							var pkt = JSON.parse(poller.responseText)
							var text = poller.responseText

							_lpURL = _channelURL + "&sid=" + pkt.ctrl.params.sid
							poller = lp_poller(_lpURL, true)
							poller.send(null)

							on_message(text)

							//if (streaming.onConnect) {
							//	streaming.onConnect()
							//}
						} else if (poller.status == 200) { // 200 = HTTP.OK
							on_message(poller.responseText)
							poller = lp_poller(_lpURL)
							poller.send(null)
						} else {
							// Don't throw an error here, gracefully handle server errors
							on_message(poller.responseText)
							if (streaming.onDisconnect) {
								streaming.onDisconnect()
							}
						}
					}
				}

				return poller
			}

			streaming._send = function(pkt) {
				var msg = JSON.stringify(pkt, Tinode._jsonHelper)
				Tinode._logger("out: " + msg)

				_sender = lp_sender(_lpURL)
				if (_sender && (_sender.readyState == 1)) { // 1 == OPENED
					_sender.send(msg);
				} else {
					throw new Error("long poller failed to connect")
				}
			}

			streaming.Connect = function(secure) {
				_channelURL = channel_url(Tinode.baseUrl, secure ? "https" : "http")
				_poller = lp_poller(_channelURL)
				_poller.send(null)
			}

			streaming.Disconnect = function() {
				if (_sender) {
					_sender.abort()
					_sender = null
				}
				if (_poller) {
					_poller.abort()
					_poller = null
				}
				if (streaming.onDisconnect) {
					streaming.onDisconnect()
				}
			}
		}

		streaming.wantAkn = function(status) {
			if (status) {
				streaming.messageId = Math.floor((Math.random() * 0xFFFFFF) + 0xFFFFFF)
			} else {
				streaming.messageId = 0
			}
		}

		//streaming.setEcho = function(state) {
		//	streaming.Echo = state
		//}

		return streaming
	})()

/*
 * A class for holding a single topic
 */
Tinode.topic = (function(name, callbacks) {
	// Check if topic with this name is already registered
	var topic = Tinode.cacheGet("topic", name)
	if (topic) {
		return topic
	}

	topic = {
		// Server-side data
		"name": name, // topic name
		"created": null, // timestamp when the topic was created
		"updated": null, // timestamp when the topic was updated
		"mode": null, // access mode
		"private": null, // per-topic private data
		"public": null, // per-topic public data

		// Local data
		"_users": {}, // List of subscribed users' IDs
		"_messages": Tinode.CBuffer(100), // message cache, sorted by message timestamp, from old to new, keep 100 messages
		"_subscribed": false, // boolean, true if the topic is currently live
		"_changes": [], // list of properties changed, for cache management
		"_callbacks": callbacks // onData, onMeta, onPres, onInfoChange, onSubsChange, onOffline
	}

	topic.isSubscribed = function() {
		return topic._subscribed
	}

	var processInfo = function(info) {
		// Copy parameters from obj.info to this topic.
		for (var prop in info) {
			if (info.hasOwnProperty(prop) && info[prop]) {
				topic[prop] = info[prop]
			}
		}

		if (typeof topic.created === "string") {
			topic.created = new Date(topic.created)
		}
		if (typeof topic.updated === "string") {
			topic.updated = new Date(topic.updated)
		}

		if (topic._callbacks && topic._callbacks.onInfoChange) {
			topic._callbacks.onInfoChange(info)
		}
	}

	var processSub = function(subs) {
		if (topic._callbacks && topic._callbacks.onSubsChange) {
			for (var idx in subs) {
				topic._callbacks.onSubsChange(subs[idx])
			}
		}

		for (var idx in subs) {
			var sub = subs[idx]
			if (sub.user) { // response to get.sub on !me topic does not have .user set
				Tinode.cachePut("user", sub.user, sub)
				topic._users[sub.user] = sub.user
			}
		}
	}

	topic.Subscribe = function(params) {
		// If the topic is already subscribed, return resolved promise
		if (topic._subscribed) {
			return Promise.resolve(topic)
		}

		var name = topic.name
			// Send subscribe message, handle async response
		return Tinode.streaming.Subscribe((name || Tinode.TOPIC_NEW), params)
			.then(function(ctrl) {

				// Handle payload
				if (ctrl.params) {
					// Handle "info" part of response - topic description
					if (ctrl.params.info) {
						processInfo(ctrl.params.info)
					}

					// Handle "sub" part of response - list of topic subscribers
					// or subscriptions in case of 'me' topic
					if (ctrl.params.sub && ctrl.params.sub.length > 0) {
						processSub(ctrl.params.sub)
					}
				}

				topic._subscribed = true

				if (topic.name != name) {
					Tinode.cacheDel("topic", name)
					Tinode.cachePut("topic", topic.name, topic)
				}

				return topic
			})
	}

	topic.Publish = function(data, params) {
		if (!topic._subscribed) {
			throw new Error("Cannot publish on inactive topic")
		}
		// Send data
		return Tinode.streaming.Publish(topic.name, data, params)
	}

	// Leave topic
	// @unsub: boolean, leave and unsubscribe
	topic.Leave = function(unsub) {
		if (!topic._subscribed) {
			throw new Error("Cannot leave inactive topic")
		}
		// Send unsubscribe message, handle async response
		return Tinode.streaming.Leave(topic.name, unsub)
			.then(function(obj) {
				topic.resetSub()
			})
	}

	topic.Set = function(params) {
		if (!topic._subscribed) {
			throw new Error("Cannot update inactive topic")
		}
		// Send Set message, handle async response
		return Tinode.streaming.Set(topic.name, params)
	}

	// Delete messages
	topic.Delete = function(params) {
		if (!topic._subscribed) {
			throw new Error("Cannot delete messages in inactive topic")
		}
		// Send Set message, handle async response
		return Tinode.streaming.DelMessages(topic.name, params)
	}

	topic.DelTopic = function(params) {
		if (!topic._subscribed) {
			throw new Error("Cannot delete inactive topic")
		}

		// Delete topic, handle async response
		return Tinode.streaming.DelTopic(topic.name)
			.then(function(obj) {
				topic.resetSub()
				Tinode.cacheDel("topic", topic.name)
			})
	}

	// Sync topic.info updates to the server
	topic.Sync = function() {
		// TODO(gene): make info object with just the updated topic fields, send it to the server
		throw new Error("Not implemented")
			// Tinode.streaming.Set(name, obj)
	}

	topic.UserInfo = function(uid) {
		// TODO(gene): handle asynchronous requests

		var user = Tinode.cacheGet("user", uid)
		if (user) {
			return user // Promise.resolve(user)
		}
		//return Tinode.streaming.Get(uid)
	}

	// Iterate over stored messages
	// Iteration stops if callback returns true
	topic.Messages = function(callback, context) {
		topic._messages.forEach(callback, context)
	}

	// Process data message
	topic.routeData = function(data) {
		topic._messages.put(data)
		if (topic._callbacks && topic._callbacks.onData) {
			topic._callbacks.onData(data)
		}
	}

	// Process metadata message
	topic.routeMeta = function(meta) {
		if (topic._callbacks && topic._callbacks.onMeta) {
			topic._callbacks.onMeta(meta)
		}

		if (meta.info) {
			processInfo(meta.info)
		}

		if (meta.sub && meta.sub.length > 0) {
			processSub(meta.sub)
		}
	}

	// Process presence change message
	topic.routePres = function(pres) {
		if (topic._callbacks && topic._callbacks.onPres) {
			topic._callbacks.onPres(pres)
		}
	}

	// Reset subscribed state
	topic.resetSub = function() {
		topic._subscribed = false
	}

	// Save topic in cache if it's a named topic
	if (name) {
		Tinode.cachePut("topic", name, topic)
	}

	return topic
})

// Special 'me' topic
Tinode.topicMe = (function(callbacks) {
	return Tinode.topic(Tinode.TOPIC_ME, {
		"onData": function(data) {
			if (callbacks && callbacks.onData) {
				callbacks.onData(data)
			}
		},
		"onMeta": function(meta) {
			if (callbacks && callbacks.onMeta) {
				callbacks.onMeta(meta)
			}
		},
		"onPres": function(pres) {
			if (callbacks && callbacks.onPres) {
				callbacks.onPres(pres)
			}
		},
		"onSubsChange": function(sub) {
			if (callbacks && callbacks.onSubsChange) {
				callbacks.onSubsChange(sub)
			}
		},
		"onInfoChange": function(info) {
			if (callbacks && callbacks.onInfoChange) {
				callbacks.onInfoChange(info)
			}
		}
	})
})
