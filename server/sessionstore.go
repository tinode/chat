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
	"github.com/tinode/chat/server/store"
)

type SessionStore struct {
	rw sync.RWMutex

	// Support for long polling sessions: a list of sessions sorted by last access time.
	// Needed for cleaning abandoned sessions.
	lru      *list.List
	lifeTime time.Duration

	// All sessions indexed by session ID
	sessCache map[string]*Session
}

func (ss *SessionStore) Create(conn interface{}, sid string) *Session {
	var s Session

	s.sid = sid

	switch c := conn.(type) {
	case *websocket.Conn:
		s.proto = WEBSOCK
		s.ws = c
	case http.ResponseWriter:
		s.proto = LPOLL
		s.wrt = c
	case *ClusterNode:
		s.proto = RPC
		s.rpcnode = c
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
	if s.sid == "" {
		s.sid = store.GetUidString()
	}

	ss.rw.Lock()
	ss.sessCache[s.sid] = &s

	if s.proto == LPOLL {
		// Only LP sessions need to be sorted by last active
		s.lpTracker = ss.lru.PushFront(&s)

		// Remove expired sessions
		expire := s.lastTouched.Add(-ss.lifeTime)
		for elem := ss.lru.Back(); elem != nil; elem = ss.lru.Back() {
			if elem.Value.(*Session).lastTouched.Before(expire) {
				ss.lru.Remove(elem)
				delete(ss.sessCache, elem.Value.(*Session).sid)
			} else {
				break // don't need to traverse further
			}
		}
	}

	ss.rw.Unlock()

	return &s
}

func (ss *SessionStore) Get(sid string) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	if sess := ss.sessCache[sid]; sess != nil {
		if sess.proto == LPOLL {
			ss.lru.MoveToFront(sess.lpTracker)
			sess.lastTouched = time.Now()
		}

		return sess
	}

	return nil
}

func (ss *SessionStore) Delete(s *Session) {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	delete(ss.sessCache, s.sid)

	if s.proto == LPOLL {
		ss.lru.Remove(s.lpTracker)
	}
}

// Shutting down sessionStore. No need to clean up.
func (ss *SessionStore) Shutdown() {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	shutdown, _ := json.Marshal(NoErrShutdown(time.Now().UTC().Round(time.Millisecond)))
	for _, s := range ss.sessCache {
		if s.send != nil {
			s.send <- shutdown
		}
	}

	log.Printf("SessionStore shut down, sessions terminated: %d", len(ss.sessCache))
}

func NewSessionStore(lifetime time.Duration) *SessionStore {
	store := &SessionStore{
		lru:      list.New(),
		lifeTime: lifetime,

		sessCache: make(map[string]*Session),
	}

	return store
}
