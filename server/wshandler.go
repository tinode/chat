/******************************************************************************
 *
 *  Copyright (C) 2014 Tinode, All Rights Reserved
 *
 *  This program is free software; you can redistribute it and/or modify it
 *  under the terms of the GNU Affero General Public License as published by
 *  the Free Software Foundation; either version 3 of the License, or (at your
 *  option) any later version.
 *
 *  This program is distributed in the hope that it will be useful, but
 *  WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY
 *  or FITNESS FOR A PARTICULAR PURPOSE.
 *  See the GNU Affero General Public License for more details.
 *
 *  You should have received a copy of the GNU Affero General Public License
 *  along with this program; if not, see <http://www.gnu.org/licenses>.
 *
 *  This code is available under licenses for commercial use.
 *
 *  File        :  wshandler.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
 *
 *  Description :
 *
 *    Handler of websocket connections. See also lphandler.go for long polling.
 *
 *****************************************************************************/
package main

import (
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"time"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = IDLETIMEOUT

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1 << 15 // 32K
)

func (sess *Session) readLoop() {
	defer func() {
		log.Println("serveWebsocket - stop")
		sess.close()
		globals.sessionStore.Delete(sess.sid)
		for _, sub := range sess.subs {
			// sub.done is the same as topic.unreg
			sub.done <- &sessionLeave{sess: sess, unsub: false}
		}
		// FIXME(gene): this currently causes occasional panics
		close(sess.send)
	}()

	sess.ws.SetReadLimit(maxMessageSize)
	sess.ws.SetReadDeadline(time.Now().Add(pongWait))
	sess.ws.SetPongHandler(func(string) error {
		sess.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	sess.remoteAddr = sess.ws.RemoteAddr().String()

	for {
		// Try reading a ClientComMessage
		_, raw, err := sess.ws.ReadMessage()
		if err != nil {
			log.Println("sess.readLoop: " + err.Error())
			return
		}

		sess.dispatch(raw)
	}
}

func (sess *Session) writeLoop() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		sess.close() // break readLoop
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok {
				// channel closed
				return
			}
			if err := ws_write(sess.ws, websocket.TextMessage, []byte(msg)); err != nil {
				log.Println("sess.writeLoop: " + err.Error())
				return
			}
		case <-ticker.C:
			if err := ws_write(sess.ws, websocket.PingMessage, []byte{}); err != nil {
				log.Println("sess.writeLoop: ping/" + err.Error())
				return
			}
		}
	}
}

// Writes a message with the given message type (mt) and payload.
func ws_write(ws *websocket.Conn, mt int, payload []byte) error {
	ws.SetWriteDeadline(time.Now().Add(writeWait))
	return ws.WriteMessage(mt, payload)
}

// Handles websocket requests from peers
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func serveWebSocket(wrt http.ResponseWriter, req *http.Request) {
	var appid uint32 = 0
	if appid, _ = checkApiKey(getApiKey(req)); appid == 0 {
		http.Error(wrt, "Missing, invalid or expired API key", http.StatusForbidden)
		return
	}

	if req.Method != "GET" {
		http.Error(wrt, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ws, err := upgrader.Upgrade(wrt, req, nil)
	if _, ok := err.(websocket.HandshakeError); ok {
		http.Error(wrt, "Not a websocket handshake", http.StatusBadRequest)
		return
	} else if err != nil {
		log.Println(err)
		return
	}

	sess := globals.sessionStore.Create(ws, appid)

	sess.QueueOut(&ServerComMessage{Ctrl: &MsgServerCtrl{
		Code:      http.StatusCreated,
		Text:      http.StatusText(http.StatusCreated),
		Params:    map[string]interface{}{"ver": VERSION},
		Timestamp: time.Now().UTC().Round(time.Millisecond)}})

	go sess.writeLoop()
	sess.readLoop()
}
