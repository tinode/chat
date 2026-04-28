package store

import (
	"errors"
	"testing"

	adapter "github.com/tinode/chat/server/db"
)

type testHealthAdapter struct {
	adapter.Adapter
	isOpen         bool
	checkHealthErr error
}

func (a *testHealthAdapter) IsOpen() bool {
	return a.isOpen
}

func (a *testHealthAdapter) CheckHealth() error {
	return a.checkHealthErr
}

func TestStoreCheckHealthReturnsErrorWhenAdapterIsNil(t *testing.T) {
	prevAdapter := adp
	defer func() {
		adp = prevAdapter
	}()

	adp = nil

	err := Store.CheckHealth()
	if err == nil {
		t.Fatal("CheckHealth() error = nil, want non-nil")
	}
}

func TestStoreCheckHealthReturnsErrorWhenAdapterIsClosed(t *testing.T) {
	prevAdapter := adp
	defer func() {
		adp = prevAdapter
	}()

	adp = &testHealthAdapter{
		isOpen:         false,
		checkHealthErr: nil,
	}

	err := Store.CheckHealth()
	if err == nil {
		t.Fatal("CheckHealth() error = nil, want non-nil")
	}
}

func TestStoreCheckHealthDelegatesToAdapter(t *testing.T) {
	prevAdapter := adp
	defer func() {
		adp = prevAdapter
	}()

	adp = &testHealthAdapter{
		isOpen:         true,
		checkHealthErr: nil,
	}

	err := Store.CheckHealth()
	if err != nil {
		t.Fatalf("CheckHealth() error = %v, want nil", err)
	}
}

func TestStoreCheckHealthReturnsAdapterError(t *testing.T) {
	prevAdapter := adp
	defer func() {
		adp = prevAdapter
	}()

	expectedErr := errors.New("db unavailable")
	adp = &testHealthAdapter{
		isOpen:         true,
		checkHealthErr: expectedErr,
	}

	err := Store.CheckHealth()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("CheckHealth() error = %v, want %v", err, expectedErr)
	}
}
