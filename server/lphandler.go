/******************************************************************************
 *
 *  Description :
 *
 *  Handler of long polling clients (see also wshandler for web sockets)
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

/*
func lp_writePkt(wrt http.ResponseWriter, pkt *ServerComMessage) error {
	data, _ := json.Marshal(pkt)
	_, err := wrt.Write(data)
	return err
}
*/

func (sess *Session) writeOnce() {
	// Next call may change wrt, save it here
	wrt := sess.wrt

	notifier, _ := wrt.(http.CloseNotifier)
	closed := notifier.CloseNotify()

	select {
	case msg, ok := <-sess.send:
		if !ok {
			log.Println("writeOnce: reading from a closed channel")
		} else if _, err := wrt.Write(msg); err != nil {
			log.Println("sess.writeOnce: " + err.Error())
			sess.wrt = nil
		}

	case <-closed:
		log.Println("conn.writeOnce: connection closed by peer")
		sess.wrt = nil

	case msg := <-sess.stop:
		// Make session unavailable
		globals.sessionStore.Delete(sess)
		sess.wrt = nil
		wrt.Write(msg)

	case topic := <-sess.detach:
		delete(sess.subs, topic)

	case <-time.After(pingPeriod):
		// just write an empty packet on timeout
		if _, err := wrt.Write([]byte{}); err != nil {
			log.Println("sess.writeOnce: timout/" + err.Error())
		}
	}
}

func (sess *Session) readOnce(req *http.Request) bool {
	if raw, err := ioutil.ReadAll(req.Body); err == nil {
		sess.dispatchRaw(raw)
		return true
	} else {
		log.Println("longPoll: " + err.Error())
		return false
	}
}

// serveLongPoll handles long poll connections when WebSocket is not available
// Connection could be without sid or with sid:
//  - if sid is empty, create session, expect a login in the same request, respond and close
//  - if sid is not empty and there is an initialized session, payload is optional
//   - if no payload, perform long poll
//   - if payload exists, process it and close
//  - if sid is not empty but there is no session, report an error
func serveLongPoll(wrt http.ResponseWriter, req *http.Request) {

	// Use lowest common denominator - this is a legacy handler after all (otherwise would use application/json)
	wrt.Header().Set("Content-Type", "text/plain")

	enc := json.NewEncoder(wrt)

	if isValid, _ := checkApiKey(getApiKey(req)); !isValid {
		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(
			&ServerComMessage{Ctrl: &MsgServerCtrl{
				Code: http.StatusForbidden,
				Text: "valid API key is required"}})
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

	log.Printf("HTTP %s %s?%s from '%s' %d bytes", req.Method,
		req.URL.Path, req.URL.RawQuery, req.RemoteAddr, req.ContentLength)

	// Get session id
	sid := req.FormValue("sid")
	var sess *Session
	if sid == "" {
		// New session
		sess = globals.sessionStore.Create(wrt, "")
		log.Println("longPoll: new session created, sid=", sess.sid)

	} else {
		// Existing session
		sess = globals.sessionStore.Get(sid)
		if sess == nil {
			log.Println("longPoll: invalid or expired session id ", sid)

			wrt.WriteHeader(http.StatusForbidden)
			enc.Encode(
				&ServerComMessage{Ctrl: &MsgServerCtrl{
					Code: http.StatusForbidden,
					Text: "invalid or expired session id"}})

			return
		}
	}

	sess.wrt = wrt
	sess.remoteAddr = req.RemoteAddr

	if req.ContentLength > 0 {
		// Read payload and send it for processing.
		if !sess.readOnce(req) {
			// Failed to red, stop
			return
		}
	}

	// Wait for data, write it to the connection or timeout
	sess.writeOnce()
}
