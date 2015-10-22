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
 *  File        :  lphandler.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
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

func lp_writePkt(wrt http.ResponseWriter, pkt *ServerComMessage) error {
	data, _ := json.Marshal(pkt)
	_, err := wrt.Write(data)
	return err
}

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
	case <-time.After(pingPeriod):
		// just write an empty packet on timeout
		if _, err := wrt.Write([]byte{}); err != nil {
			log.Println("sess.writeOnce: timout/" + err.Error())
		}
	}
}

func (sess *Session) readOnce(req *http.Request) {
	if raw, err := ioutil.ReadAll(req.Body); err == nil {
		sess.dispatch(raw)
	} else {
		log.Println("longPoll: " + err.Error())
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
	var appid uint32

	// Use lowest common denominator - this is a legacy handler after all
	wrt.Header().Set("Content-Type", "text/plain")

	enc := json.NewEncoder(wrt)

	if appid, _ = checkApiKey(getApiKey(req)); appid == 0 {
		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(
			&ServerComMessage{Ctrl: &MsgServerCtrl{
				Code: http.StatusForbidden,
				Text: "Valid API key is required"}})
		return
	}

	// TODO(gene): should it be configurable?
	// Currently any domain is allowed to get data from the chat server
	wrt.Header().Set("Access-Control-Allow-Origin", "*")

	// Ensure the response is not cached
	if req.ProtoAtLeast(1, 1) {
		wrt.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // HTTP 1.1
	} else {
		wrt.Header().Set("Pragma", "no-cache")                                   // HTTP 1.0
	}
	wrt.Header().Set("Expires", "0")                                         // Proxies

	// TODO(gene): respond differently to valious HTTP methods

	log.Printf("HTTP %s %s?%s from '%s' %d bytes", req.Method,
		req.URL.Path, req.URL.RawQuery, req.RemoteAddr, req.ContentLength)

	// Get session id
	sid := req.FormValue("sid")
	if sid == "" {
		sess := globals.sessionStore.Create(wrt, appid)
		log.Println("longPoll: new session created, sid=", sess.sid)

		wrt.WriteHeader(http.StatusCreated)
		enc.Encode(
			&ServerComMessage{Ctrl: &MsgServerCtrl{
				Code:      http.StatusCreated,
				Text:      http.StatusText(http.StatusCreated),
				Params:    map[string]interface{}{"sid": sess.sid, "ver": VERSION, "build": buildstamp},
				Timestamp: time.Now().UTC().Round(time.Millisecond)}})

		// Any payload is ignored
		return
	}

	sess := globals.sessionStore.Get(sid)
	if sess == nil {
		log.Println("longPoll: invalid or expired session id ", sid)

		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(
			&ServerComMessage{Ctrl: &MsgServerCtrl{
				Code: http.StatusForbidden,
				Text: "Invalid or expired session id"}})

		return
	}

	sess.wrt = wrt
	sess.remoteAddr = req.RemoteAddr

	if req.ContentLength > 0 {
		// Got payload. Process it and return right away
		sess.readOnce(req)
		return
	}

	// Wait for data, write it to the connection or timeout
	sess.writeOnce()
}
