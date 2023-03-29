/******************************************************************************
 *
 *  Description :
 *
 *    Handler of websocket connections. See also hdl_longpoll.go for long polling
 *    and hdl_grpc.go for gRPC.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tinode/chat/server/logs"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = idleSessionTimeout

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

func (sess *Session) closeWS() {
	if sess.proto == WEBSOCK {
		sess.ws.Close()
	}
}

func (sess *Session) readLoop() {
	defer func() {
		sess.closeWS()
		sess.cleanUp(false)
	}()

	sess.ws.SetReadLimit(globals.maxMessageSize)
	sess.ws.SetReadDeadline(time.Now().Add(pongWait))
	sess.ws.SetPongHandler(func(string) error {
		sess.ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		// Read a ClientComMessage
		_, raw, err := sess.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure,
				websocket.CloseNormalClosure) {
				logs.Err.Println("ws: readLoop", sess.sid, err)
			}
			return
		}
		statsInc("IncomingMessagesWebsockTotal", 1)
		sess.dispatchRaw(raw)
	}
}

func (sess *Session) sendMessage(msg any) bool {
	if len(sess.send) > sendQueueLimit {
		logs.Err.Println("ws: outbound queue limit exceeded", sess.sid)
		return false
	}

	statsInc("OutgoingMessagesWebsockTotal", 1)
	if err := wsWrite(sess.ws, websocket.TextMessage, msg); err != nil {
		if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure,
			websocket.CloseNormalClosure) {
			logs.Err.Println("ws: writeLoop", sess.sid, err)
		}
		return false
	}
	return true
}

func (sess *Session) writeLoop() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		// Break readLoop.
		sess.closeWS()
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok {
				// Channel closed.
				return
			}
			switch v := msg.(type) {
			case []*ServerComMessage: // batch of unserialized messages
				for _, msg := range v {
					w := sess.serializeAndUpdateStats(msg)
					if !sess.sendMessage(w) {
						return
					}
				}
			case *ServerComMessage: // single unserialized message
				w := sess.serializeAndUpdateStats(v)
				if !sess.sendMessage(w) {
					return
				}
			default: // serialized message
				if !sess.sendMessage(v) {
					return
				}
			}

		case <-sess.bkgTimer.C:
			if sess.background {
				sess.background = false
				sess.onBackgroundTimer()
			}

		case msg := <-sess.stop:
			// Shutdown requested, don't care if the message is delivered
			if msg != nil {
				wsWrite(sess.ws, websocket.TextMessage, msg)
			}
			return

		case topic := <-sess.detach:
			sess.delSub(topic)

		case <-ticker.C:
			if err := wsWrite(sess.ws, websocket.PingMessage, nil); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure,
					websocket.CloseNormalClosure) {
					logs.Err.Println("ws: writeLoop ping", sess.sid, err)
				}
				return
			}
		}
	}
}

// Writes a message with the given message type (mt) and payload.
func wsWrite(ws *websocket.Conn, mt int, msg any) error {
	var bits []byte
	if msg != nil {
		bits = msg.([]byte)
	} else {
		bits = []byte{}
	}
	ws.SetWriteDeadline(time.Now().Add(writeWait))
	return ws.WriteMessage(mt, bits)
}

// Handles websocket requests from peers.
var upgrader = websocket.Upgrader{
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	EnableCompression: globals.wsCompression,
	// Allow connections from any Origin
	CheckOrigin: func(r *http.Request) bool { return true },
}

func serveWebSocket(wrt http.ResponseWriter, req *http.Request) {
	now := time.Now().UTC().Round(time.Millisecond)

	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		wrt.WriteHeader(http.StatusForbidden)
		json.NewEncoder(wrt).Encode(ErrAPIKeyRequired(now))
		logs.Err.Println("ws: Missing, invalid or expired API key")
		return
	}

	if req.Method != http.MethodGet {
		wrt.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(wrt).Encode(ErrOperationNotAllowed("", "", now))
		logs.Err.Println("ws: Invalid HTTP method", req.Method)
		return
	}

	ws, err := upgrader.Upgrade(wrt, req, nil)
	if _, ok := err.(websocket.HandshakeError); ok {
		logs.Err.Println("ws: Not a websocket handshake")
		return
	} else if err != nil {
		logs.Err.Println("ws: failed to Upgrade ", err)
		return
	}

	sess, count := globals.sessionStore.NewSession(ws, "")
	if globals.useXForwardedFor {
		sess.remoteAddr = req.Header.Get("X-Forwarded-For")
		if !isRoutableIP(sess.remoteAddr) {
			sess.remoteAddr = ""
		}
	}
	if sess.remoteAddr == "" {
		sess.remoteAddr = req.RemoteAddr
	}

	logs.Info.Println("ws: session started", sess.sid, sess.remoteAddr, count)

	// Do work in goroutines to return from serveWebSocket() to release file pointers.
	// Otherwise "too many open files" will happen.
	go sess.writeLoop()
	go sess.readLoop()
}
