/******************************************************************************
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
		// no need to store c for long polling, it changes with every request
	case *ClusterNode:
		s.proto = RPC
		s.rpcnode = c
	default:
		s.proto = NONE
	}

	if s.proto != NONE {
		s.subs = make(map[string]*Subscription)
		s.send = make(chan []byte, 256)  // buffered
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
			sess := elem.Value.(*Session)
			if sess.lastTouched.Before(expire) {
				ss.lru.Remove(elem)
				delete(ss.sessCache, sess.sid)
				globals.cluster.sessionGone(sess)
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
// Don't send to clustered sessions, their servers are not being shut down.
func (ss *SessionStore) Shutdown() {
	ss.rw.Lock()
	defer ss.rw.Unlock()

	shutdown, _ := json.Marshal(NoErrShutdown(time.Now().UTC().Round(time.Millisecond)))
	for _, s := range ss.sessCache {
		if s.send != nil && s.proto != RPC {
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
