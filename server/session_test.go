package main

import (
	"sync"
	"testing"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

func TestDispatchHello(t *testing.T) {
	s := &Session{
		send:    make(chan interface{}, 10),
		uid:     types.Uid(1),
		authLvl: auth.LevelAuth,
	}
	wg := sync.WaitGroup{}
	r := Responses{}
	wg.Add(1)
	go s.testWriteLoop(&r, &wg)
	msg := &ClientComMessage{
		Hi: &MsgClientHi{
			Id:        "123",
			Version:   "1",
			UserAgent: "test-ua",
			Lang:      "en-GB",
		},
	}
	s.dispatch(msg)
	close(s.send)
	wg.Wait()
	if len(r.messages) != 1 {
		t.Errorf("Responses: expected 1, received %d.", len(r.messages))
	}
	resp := r.messages[0].(*ServerComMessage)
	if resp == nil {
		t.Fatal("Response must be ServerComMessage")
	}
	if resp.Ctrl != nil {
		if resp.Ctrl.Code != 201 {
			t.Errorf("Response code: expected 201, got %d", resp.Ctrl.Code)
		}
		if resp.Ctrl.Params == nil {
			t.Error("Response is expected to contain params dict.")
		}
	} else {
		t.Error("Response must contain a ctrl message.")
	}

	if s.lang != "en-GB" {
		t.Errorf("Session language expected to be 'en-GB' vs '%s'", s.lang)
	}
	if s.userAgent != "test-ua" {
		t.Errorf("Session UA expected to be 'test-ua' vs '%s'", s.userAgent)
	}
	if s.countryCode != "GB" {
		t.Errorf("Country code expected to be 'GB' vs '%s'", s.countryCode)
	}
}
