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
	rw       sync.RWMutex
	sessions map[string]*list.Element
	lru      *list.List
	lifeTime time.Duration
}

func (ss *SessionStore) Create(conn interface{}, appid uint32) *Session {
	var s Session

	switch conn.(type) {
	case *websocket.Conn:
		s.proto = WEBSOCK
		s.ws, _ = conn.(*websocket.Conn)
	case http.ResponseWriter:
		s.proto = LPOLL
		s.wrt, _ = conn.(http.ResponseWriter)
	default:
		s.proto = NONE
	}

	if s.proto != NONE {
		s.subs = make(map[string]*Subscription)
		s.send = make(chan []byte, 64) // buffered
		s.stop = make(chan bool)
		s.detach = make(chan string, 64) // buffered
	}

	s.appid = appid
	s.lastTouched = time.Now()
	s.sid = getRandomString()
	s.uid = types.ZeroUid

	if s.proto != WEBSOCK {
		// Websocket connections are not managed by SessionStore
		ss.rw.Lock()

		elem := ss.lru.PushFront(&sessionStoreElement{s.sid, &s})
		ss.sessions[s.sid] = elem

		// Remove expired sessions
		expire := s.lastTouched.Add(-ss.lifeTime)
		for elem = ss.lru.Back(); elem != nil; elem = ss.lru.Back() {
			if elem.Value.(*sessionStoreElement).val.lastTouched.Before(expire) {
				ss.lru.Remove(elem)
				delete(ss.sessions, elem.Value.(*sessionStoreElement).key)
			} else {
				break // don't need to traverse further
			}
		}
		ss.rw.Unlock()
	}

	return &s
}

func (ss *SessionStore) Get(sid string) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	if elem := ss.sessions[sid]; elem != nil {
		ss.lru.MoveToFront(elem)
		elem.Value.(*sessionStoreElement).val.lastTouched = time.Now()
		return elem.Value.(*sessionStoreElement).val
	}

	return nil
}

func (ss *SessionStore) Delete(sid string) *Session {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	if elem := ss.sessions[sid]; elem != nil {
		ss.lru.Remove(elem)
		delete(ss.sessions, sid)

		return elem.Value.(*sessionStoreElement).val
	}

	return nil
}

func NewSessionStore(lifetime time.Duration) *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]*list.Element),
		lru:      list.New(),
		lifeTime: lifetime,
	}

	return store
}
