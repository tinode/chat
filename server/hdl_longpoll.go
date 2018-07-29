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

func (sess *Session) writeOnce(wrt http.ResponseWriter) {

	notifier, _ := wrt.(http.CloseNotifier)
	closed := notifier.CloseNotify()

	select {
	case msg, ok := <-sess.send:
		if !ok {
			log.Println("writeOnce: reading from a closed channel")
		} else if err := lpWrite(wrt, msg); err != nil {
			log.Println("sess.writeOnce: " + err.Error())
		}
	case <-closed:
		log.Println("conn.writeOnce: connection closed by peer")

	case msg := <-sess.stop:
		// Make session unavailable
		globals.sessionStore.Delete(sess)
		lpWrite(wrt, msg)

	case topic := <-sess.detach:
		sess.delSub(topic)

	case <-time.After(pingPeriod):
		// just write an empty packet on timeout
		if _, err := wrt.Write([]byte{}); err != nil {
			log.Println("sess.writeOnce: timout/" + err.Error())
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
		sess.dispatchRaw(raw)
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

	// log.Printf("HTTP %s %s?%s from '%s' %d bytes", req.Method,
	// 	req.URL.Path, req.URL.RawQuery, req.RemoteAddr, req.ContentLength)

	// Get session id
	sid := req.FormValue("sid")
	var sess *Session
	if sid == "" {
		// New session
		var count int
		sess, count = globals.sessionStore.Create(wrt, "")
		log.Println("lp: session started", sess.sid, count)
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

	sess.remoteAddr = req.RemoteAddr

	if req.ContentLength != 0 {
		// Read payload and send it for processing.
		if code, err := sess.readOnce(wrt, req); err != nil {
			log.Println("longPoll: " + err.Error())
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

	sess.writeOnce(wrt)
}
