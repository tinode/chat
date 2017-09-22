/******************************************************************************
 *
 *  Description :
 *
 *    Handler of websocket connections. See also lphandler.go for long polling.
 *
 *****************************************************************************/

package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = IDLETIMEOUT

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

func (s *Session) closeWS() {
	if s.proto == WEBSOCK {
		s.ws.Close()
	}
}

func (sess *Session) readLoop() {
	defer func() {
		log.Println("serveWebsocket - stop")
		sess.closeWS()
		globals.sessionStore.Delete(sess)
		globals.cluster.sessionGone(sess)
		for _, sub := range sess.subs {
			// sub.done is the same as topic.unreg
			sub.done <- &sessionLeave{sess: sess, unsub: false}
		}
	}()

	sess.ws.SetReadLimit(globals.maxMessageSize)
	sess.ws.SetReadDeadline(time.Now().Add(pongWait))
	sess.ws.SetPongHandler(func(string) error {
		sess.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	sess.remoteAddr = sess.ws.RemoteAddr().String()

	for {
		// Read a ClientComMessage
		if _, raw, err := sess.ws.ReadMessage(); err != nil {
			log.Println("sess.readLoop: " + err.Error())
			return
		} else {
			sess.dispatchRaw(raw)
		}
	}
}

func (sess *Session) writeLoop() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		sess.closeWS() // break readLoop
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok {
				// channel closed
				return
			}
			if err := ws_write(sess.ws, websocket.TextMessage, msg); err != nil {
				log.Println("sess.writeLoop: " + err.Error())
				return
			}
		case msg := <-sess.stop:
			// Shutdown requested, don't care if the message is delivered
			if msg != nil {
				ws_write(sess.ws, websocket.TextMessage, msg)
			}
			return

		case topic := <-sess.detach:
			delete(sess.subs, topic)

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
	// Allow connections from any Origin
	CheckOrigin: func(r *http.Request) bool { return true },
}

func serveWebSocket(wrt http.ResponseWriter, req *http.Request) {
	if isValid, _ := checkApiKey(getApiKey(req)); !isValid {
		http.Error(wrt, "Missing, invalid or expired API key", http.StatusForbidden)
		log.Println("ws: Missing, invalid or expired API key")
		return
	}

	if req.Method != "GET" {
		http.Error(wrt, "Method not allowed", http.StatusMethodNotAllowed)
		log.Println("ws: Invalid HTTP method")
		return
	}

	ws, err := upgrader.Upgrade(wrt, req, nil)
	if _, ok := err.(websocket.HandshakeError); ok {
		log.Println("ws: Not a websocket handshake")
		return
	} else if err != nil {
		log.Println("ws: failed to Upgrade ", err.Error())
		return
	}

	sess := globals.sessionStore.Create(ws, "")

	go sess.writeLoop()
	sess.readLoop()
}
