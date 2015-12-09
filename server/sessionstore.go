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
 *  File        :  sessionstore.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
 *
 *  Description :
 *
 *  Management of long polling sessions
 *
 *****************************************************************************/

package main

import (
	"container/list"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tinode/chat/server/store/types"
)

type sessionStoreElement struct {
	key string
	val *Session
}

type SessionStore struct {
	// Long polling sessions
	rw         sync.RWMutex
	lpSessions map[string]*list.Element
	lru        *list.List
	lifeTime   time.Duration

	// Websocket sessions
	wsSessions map[string]*Session
}

func (ss *SessionStore) Create(conn interface{}) *Session {
	var s Session

	switch c := conn.(type) {
	case *websocket.Conn:
		s.proto = WEBSOCK
		s.ws = c
	case http.ResponseWriter:
		s.proto = LPOLL
		s.wrt = c
	default:
		s.proto = NONE
	}

	if s.proto != NONE {
		s.subs = make(map[string]*Subscription)
		s.send = make(chan []byte, 64)   // buffered
		s.stop = make(chan []byte, 1)    // Buffered by 1 just to make it non-blocking
		s.detach = make(chan string, 64) // buffered
	}

	s.lastTouched = time.Now()
	s.sid = getRandomString()
	s.uid = types.ZeroUid

	// Websocket connections are not managed by SessionStore
	if s.proto == WEBSOCK {
		ss.rw.Lock()
		ss.wsSessions[s.sid] = &s
		ss.rw.Unlock()
	} else {
		ss.rw.Lock()

		elem := ss.lru.PushFront(&sessionStoreElement{s.sid, &s})
		ss.lpSessions[s.sid] = elem

		// Remove expired sessions
		expire := s.lastTouched.Add(-ss.lifeTime)
		for elem = ss.lru.Back(); elem != nil; elem = ss.lru.Back() {
			if elem.Value.(*sessionStoreElement).val.lastTouched.Before(expire) {
				ss.lru.Remove(elem)
				delete(ss.lpSessions, elem.Value.(*sessionStoreElement).key)
			} else {
				break // don't need to traverse further
			}
		}
		ss.rw.Unlock()
	}

	return &s
}

func (ss *SessionStore) GetLP(sid string) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	if elem := ss.lpSessions[sid]; elem != nil {
		ss.lru.MoveToFront(elem)
		elem.Value.(*sessionStoreElement).val.lastTouched = time.Now()
		return elem.Value.(*sessionStoreElement).val
	}

	return nil
}

func (ss *SessionStore) GetWS(sid string) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	return ss.wsSessions[sid]
}

func (ss *SessionStore) Delete(s *Session) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	if s.proto == WEBSOCK {
		delete(ss.wsSessions, s.sid)

	} else if elem := ss.lpSessions[s.sid]; elem != nil {
		ss.lru.Remove(elem)
		delete(ss.lpSessions, s.sid)

		return elem.Value.(*sessionStoreElement).val
	}

	return nil
}

func (ss *SessionStore) Shutdown() {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	shutdown, _ := json.Marshal(NoErrShutdown(time.Now().UTC().Round(time.Millisecond)))
	for _, s := range ss.wsSessions {
		s.send <- shutdown
	}

	for _, elem := range ss.lpSessions {
		elem.Value.(*sessionStoreElement).val.send <- shutdown
	}

	log.Printf("SessionStore shut down, sessions terminated: %d ws; %d lp", len(ss.wsSessions), len(ss.lpSessions))
}

func NewSessionStore(lifetime time.Duration) *SessionStore {
	store := &SessionStore{
		lpSessions: make(map[string]*list.Element),
		lru:        list.New(),
		lifeTime:   lifetime,

		wsSessions: make(map[string]*Session),
	}

	return store
}
