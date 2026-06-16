package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
)

func TestServeLivezReturnsOK(t *testing.T) {
	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	store.Store = nil

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()

	serveLivez(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("livez status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("livez body = %q, want %q", rec.Body.String(), "ok\n")
	}
}

func TestServeReadyzReturnsServiceUnavailableWhenStoreIsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	ss := mock_store.NewMockPersistentStorageInterface(ctrl)
	ss.EXPECT().IsOpen().Return(false)

	store.Store = ss

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	serveReadyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "not ready\n" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "not ready\n")
	}
}

func TestServeReadyzReturnsOKWhenStoreHealthSucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	ss := mock_store.NewMockPersistentStorageInterface(ctrl)
	ss.EXPECT().IsOpen().Return(true)
	ss.EXPECT().CheckHealth().Return(nil)

	store.Store = ss

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	serveReadyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "ok\n")
	}
}

func TestServeReadyzReturnsServiceUnavailableWhenStoreHealthFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	ss := mock_store.NewMockPersistentStorageInterface(ctrl)
	ss.EXPECT().IsOpen().Return(true)
	ss.EXPECT().CheckHealth().Return(errors.New("db down"))

	store.Store = ss

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	serveReadyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "not ready\n" {
		t.Fatalf("readyz body = %q, want %q", rec.Body.String(), "not ready\n")
	}
}

func TestRegisterHealthHandlersRoutesHEADRequests(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	ss := mock_store.NewMockPersistentStorageInterface(ctrl)
	ss.EXPECT().IsOpen().Return(true)
	ss.EXPECT().CheckHealth().Return(nil)

	store.Store = ss

	mux := http.NewServeMux()
	registerHealthHandlers(mux)

	req := httptest.NewRequest(http.MethodHead, "/readyz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /readyz status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD /readyz body length = %d, want 0", rec.Body.Len())
	}
}

func TestRegisterHealthHandlersRoutesLivezAtRoot(t *testing.T) {
	prevStore := store.Store
	defer func() {
		store.Store = prevStore
	}()

	store.Store = nil

	mux := http.NewServeMux()
	registerHealthHandlers(mux)

	req := httptest.NewRequest(http.MethodHead, "/livez", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /livez status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD /livez body length = %d, want 0", rec.Body.Len())
	}
}
