/******************************************************************************
 *
 *  Description :
 *
 *    Handler of long polling clients. See also hdl_websock.go for web sockets and
 *    hdl_grpc.go for gRPC
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

func (sess *Session) writeOnce(wrt http.ResponseWriter, req *http.Request) {

	for {
		select {
		case msg, ok := <-sess.send:
			if ok {
				if len(sess.send) > sendQueueLimit {
					log.Println("longPoll: outbound queue limit exceeded", sess.sid)
				} else {
					statsInc("OutgoingMessagesLongpollTotal", 1)
					if err := lpWrite(wrt, msg); err != nil {
						log.Println("longPoll: writeOnce failed", sess.sid, err)
					}
				}
			}
			return

		case <-sess.bkgTimer.C:
			if sess.background {
				sess.background = false
				sess.onBackgroundTimer()
			}

		case msg := <-sess.stop:
			// Request to close the session. Make it unavailable.
			globals.sessionStore.Delete(sess)
			// Don't care if lpWrite fails.
			lpWrite(wrt, msg)
			return

		case topic := <-sess.detach:
			// Request to detach the session from a topic.
			sess.delSub(topic)
			// No 'return' statement here: continue waiting

		case <-time.After(pingPeriod):
			// just write an empty packet on timeout
			if _, err := wrt.Write([]byte{}); err != nil {
				log.Println("longPoll: writeOnce: timout", sess.sid, err)
			}
			return

		case <-req.Context().Done():
			// HTTP request cancelled or connection lost.
			return
		}
	}
}

func lpWrite(wrt http.ResponseWriter, msg interface{}) error {
	// This will panic if msg is not []byte. This is intentional.
	wrt.Write(msg.([]byte))
	return nil
}

func (sess *Session) readOnce(wrt http.ResponseWriter, req *http.Request) (int, error) {
	if req.ContentLength > globals.maxMessageSize {
		return http.StatusExpectationFailed, errors.New("request too large")
	}

	req.Body = http.MaxBytesReader(wrt, req.Body, globals.maxMessageSize)
	raw, err := ioutil.ReadAll(req.Body)
	if err == nil {
		// Locking-unlocking is needed because the client may issue multiple requests in parallel.
		// Should not affect performance
		sess.lock.Lock()
		statsInc("IncomingMessagesLongpollTotal", 1)
		sess.dispatchRaw(raw)
		sess.lock.Unlock()
		return 0, nil
	}

	return 0, err
}

// serveLongPoll handles long poll connections when WebSocket is not available
// Connection could be without sid or with sid:
//  - if sid is empty, create session, expect a login in the same request, respond and close
//  - if sid is not empty and there is an initialized session, payload is optional
//   - if no payload, perform long poll
//   - if payload exists, process it and close
//  - if sid is not empty but there is no session, report an error
func serveLongPoll(wrt http.ResponseWriter, req *http.Request) {
	now := time.Now().UTC().Round(time.Millisecond)

	// Use the lowest common denominator - this is a legacy handler after all (otherwise would use application/json)
	wrt.Header().Set("Content-Type", "text/plain")
	if globals.tlsStrictMaxAge != "" {
		wrt.Header().Set("Strict-Transport-Security", "max-age"+globals.tlsStrictMaxAge)
	}

	enc := json.NewEncoder(wrt)

	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(ErrAPIKeyRequired(now))
		return
	}

	// TODO(gene): should it be configurable?
	// Currently any domain is allowed to get data from the chat server
	wrt.Header().Set("Access-Control-Allow-Origin", "*")

	// Ensure the response is not cached
	if req.ProtoAtLeast(1, 1) {
		wrt.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1
	} else {
		wrt.Header().Set("Pragma", "no-cache") // HTTP 1.0
	}
	wrt.Header().Set("Expires", "0") // Proxies

	// TODO(gene): respond differently to valious HTTP methods

	// Get session id
	sid := req.FormValue("sid")
	var sess *Session
	if sid == "" {
		// New session
		var count int
		sess, count = globals.sessionStore.NewSession(wrt, "")
		sess.remoteAddr = lpRemoteAddr(req)
		log.Println("longPoll: session started", sess.sid, sess.remoteAddr, count)

		wrt.WriteHeader(http.StatusCreated)
		pkt := NoErrCreated(req.FormValue("id"), "", now)
		pkt.Ctrl.Params = map[string]string{
			"sid": sess.sid,
		}
		enc.Encode(pkt)

		return
	}

	// Existing session
	sess = globals.sessionStore.Get(sid)
	if sess == nil {
		log.Println("longPoll: invalid or expired session id", sid)
		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(ErrSessionNotFound(now))
		return
	}

	addr := lpRemoteAddr(req)
	if sess.remoteAddr != addr {
		sess.remoteAddr = addr
		log.Println("longPoll: remote address changed", sid, addr)
	}

	if req.ContentLength != 0 {
		// Read payload and send it for processing.
		if code, err := sess.readOnce(wrt, req); err != nil {
			log.Println("longPoll: readOnce failed", sess.sid, err)
			// Failed to read request, report an error, if possible
			if code != 0 {
				wrt.WriteHeader(code)
			} else {
				wrt.WriteHeader(http.StatusBadRequest)
			}
			enc.Encode(ErrMalformed(req.FormValue("id"), "", now))
		}
		return
	}

	sess.writeOnce(wrt, req)
}

// Obtain IP address of the client.
func lpRemoteAddr(req *http.Request) string {
	var addr string
	if globals.useXForwardedFor {
		addr = req.Header.Get("X-Forwarded-For")
	}
	if addr != "" {
		return addr
	}
	return req.RemoteAddr
}
