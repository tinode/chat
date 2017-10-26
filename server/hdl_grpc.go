/******************************************************************************
 *
 *  Description :
 *
 *    Handler of gRPC connections. See also hdl_websock.go for websockets and
 *    hdl_longpoll.go for long polling.
 *
 *****************************************************************************/

package main

import (
	"time"

	"google.golang.org/grpc"
)

func (sess *Session) closeGrpc() {
	if sess.proto == GRPC {
		sess.grpcnode.Close()
	}
}

func (sess *Session) readGrpcLoop() {
	defer func() {
		log.Println("serveGrpc - stop")
		sess.closeGrpc()
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

func (sess *Session) writeGrpcLoop() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		sess.closeGrpc() // break readLoop
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
func grpc_write(ws *websocket.Conn, mt int, payload []byte) error {
	ws.SetWriteDeadline(time.Now().Add(writeWait))
	return ws.WriteMessage(mt, payload)
}

func serveGRpc(wrt http.ResponseWriter, req *http.Request) {
}
