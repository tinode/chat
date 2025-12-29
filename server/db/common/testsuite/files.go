package testsuite

import (
	"testing"
	"time"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	"github.com/tinode/chat/server/store/types"
)

// RunFileGet runs the shared FileGet tests used by adapters.
func RunFileGet(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	// Test not found
	got, err := adp.FileGet("dummyfileid")
	if err != nil {
		if got != nil {
			t.Error("File found but shouldn't:", got)
		}
	}
}

// RunFileStartUpload runs the shared FileStartUpload tests used by adapters.
func RunFileStartUpload(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	for _, f := range td.Files {
		err := adp.FileStartUpload(f)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// RunFileFinishUpload runs the shared FileFinishUpload tests used by adapters.
func RunFileFinishUpload(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	got, err := adp.FileFinishUpload(td.Files[0], true, 22222)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != types.UploadCompleted {
		t.Errorf("Status mismatch: got %v want %v", got.Status, types.UploadCompleted)
	}
	if got.Size != 22222 {
		t.Errorf("Size mismatch: got %v want %v", got.Size, 22222)
	}
}

// RunFileDeleteUnused runs the shared FileDeleteUnused tests used by adapters.
func RunFileDeleteUnused(t *testing.T, adp adapter.Adapter) {
	t.Helper()

	locs, err := adp.FileDeleteUnused(time.Now().Add(1*time.Minute), 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Errorf("Locations length mismatch: got %v want %v", len(locs), 2)
	}
}
