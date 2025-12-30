package fcm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIidBatchManage_Success(t *testing.T) {
	// Mock IID batch endpoint returning two successful results
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{}, {}}})
	}))
	defer srv.Close()

	old := iidBaseURL
	iidBaseURL = srv.URL + "/iid/v1"
	defer func() { iidBaseURL = old }()

	// Avoid performing real OAuth token exchange in tests
	oldEnv := os.Getenv("PUSHGW_TEST_NO_OAUTH")
	_ = os.Setenv("PUSHGW_TEST_NO_OAUTH", "1")
	defer os.Setenv("PUSHGW_TEST_NO_OAUTH", oldEnv)

	handler.creds = []byte(`{"type":"service_account","project_id":"test-project"}`)

	res, err := iidBatchManage("movies", []string{"t1", "t2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if _, ok := res[0]["error"]; ok {
		t.Fatalf("unexpected error in result 0: %v", res[0])
	}
}

func TestIidBatchManage_PartialFailure(t *testing.T) {
	// Mock IID batch endpoint returning first item with an error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"error": "NOT_FOUND"}, {}}})
	}))
	defer srv.Close()

	old := iidBaseURL
	iidBaseURL = srv.URL + "/iid/v1"
	defer func() { iidBaseURL = old }()

	oldEnv := os.Getenv("PUSHGW_TEST_NO_OAUTH")
	_ = os.Setenv("PUSHGW_TEST_NO_OAUTH", "1")
	defer os.Setenv("PUSHGW_TEST_NO_OAUTH", oldEnv)

	handler.creds = []byte(`{"type":"service_account","project_id":"test-project"}`)

	res, err := iidBatchManage("movies", []string{"t1", "t2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if errStr, ok := res[0]["error"].(string); !ok || errStr == "" {
		t.Fatalf("expected error in first result, got %#v", res[0])
	}
	if _, ok := res[1]["error"]; ok {
		t.Fatalf("unexpected error in second result: %v", res[1])
	}
}

func TestIidBatchManage_MissingCredentials(t *testing.T) {
	oldEnv := os.Getenv("PUSHGW_TEST_NO_OAUTH")
	_ = os.Setenv("PUSHGW_TEST_NO_OAUTH", "1")
	defer os.Setenv("PUSHGW_TEST_NO_OAUTH", oldEnv)

	// Ensure creds are nil
	handler.creds = nil

	_, err := iidBatchManage("movies", []string{"t1"}, true)
	if err == nil {
		t.Fatalf("expected error when credentials are missing")
	}
}
