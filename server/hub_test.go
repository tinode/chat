package main

import (
	"sync"
	"testing"
	"time"

	"github.com/tinode/chat/server/store/types"
)

func TestStopTopicsForUserSkipsUnrelatedBusyTopics(t *testing.T) {
	uid, other := types.Uid(1), types.Uid(2)
	for _, name := range []string{other.UserId(), other.FndName(), other.SlfName(),
		other.P2PName(types.Uid(3)), "grpUnrelated", "sys"} {
		t.Run(name, func(t *testing.T) {
			hub := &Hub{topics: &sync.Map{}}
			// No event loop consumes requests: this models a topic blocked in a plugin.
			topic := &Topic{name: name, userDelete: make(chan *userDeleteReq), done: make(chan struct{})}
			topic.setOwner(other)
			hub.topicPut(name, topic)
			completed := make(chan bool, 1)
			returned := make(chan struct{})
			go func() {
				hub.stopTopicsForUser(uid, StopDeleted, completed)
				close(returned)
			}()
			defer func() { close(topic.done); <-returned }()
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("account deletion blocked by an unrelated topic")
			}
			if hub.topicGet(name) != topic || hub.numTopics.Load() != 1 {
				t.Fatal("unrelated topic was removed")
			}
		})
	}
}

func TestStopTopicsForUserWaitsForRelatedTopics(t *testing.T) {
	uid := types.Uid(1)
	for _, name := range []string{uid.UserId(), uid.FndName(), uid.SlfName(),
		uid.P2PName(types.Uid(2)), "grpOwned"} {
		t.Run(name, func(t *testing.T) {
			hub := &Hub{topics: &sync.Map{}}
			topic := &Topic{name: name, userDelete: make(chan *userDeleteReq), done: make(chan struct{})}
			topic.setOwner(uid)
			hub.topicPut(name, topic)
			completed := make(chan bool, 1)
			returned := make(chan struct{})
			go func() {
				hub.stopTopicsForUser(uid, StopDeleted, completed)
				close(returned)
			}()
			defer func() { close(topic.done); <-returned }()
			select {
			case request := <-topic.userDelete:
				if request.forUser != uid || request.reason != StopDeleted {
					t.Error("incorrect deletion request")
				}
				select {
				case <-completed:
					t.Error("deletion completed before topic cleanup")
				default:
				}
				request.done <- true
			case <-time.After(time.Second):
				t.Fatal("related topic did not receive deletion request")
			}
			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("deletion did not complete after topic cleanup")
			}
		})
	}
}

func TestDeletionRoutingTracksOwnership(t *testing.T) {
	first, second := types.Uid(1), types.Uid(2)
	topic := &Topic{name: "grpOwned"}
	topic.setOwner(first)
	if !topic.mayDeleteForUser(first) || topic.mayDeleteForUser(second) {
		t.Fatal("initial owner not reflected in deletion routing")
	}
	topic.setOwner(second)
	if topic.mayDeleteForUser(first) || !topic.mayDeleteForUser(second) {
		t.Fatal("ownership transfer not reflected in deletion routing")
	}
}

func TestDeletionRoutingWaitsForGroupInitialization(t *testing.T) {
	for _, failed := range []bool{false, true} {
		uid := types.Uid(1)
		topic := &Topic{name: "grpLoading", initialized: make(chan struct{}), done: make(chan struct{})}
		result := make(chan bool, 1)
		go func() { result <- topic.mayDeleteForUser(uid) }()
		if failed {
			close(topic.done)
		} else {
			topic.setOwner(uid)
			close(topic.initialized)
		}
		select {
		case relevant := <-result:
			if relevant == failed {
				t.Fatalf("incorrect deletion routing after initialization (failed=%v)", failed)
			}
		case <-time.After(time.Second):
			t.Fatal("deletion routing did not resume after initialization")
		}
	}
}
