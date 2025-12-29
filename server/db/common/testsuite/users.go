package testsuite

import (
	"fmt"
	"reflect"
	"testing"

	adapter "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/db/common/test_data"
	types "github.com/tinode/chat/server/store/types"
)

func RunUserGet(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	// Test not found
	got, err := adp.UserGet(types.Uid(12345))
	if err == nil && got != nil {
		t.Error("user should be nil.")
	}

	got, err = adp.UserGet(types.ParseUid(td.Users[0].Id))
	if err != nil {
		t.Fatal(err)
	}

	// User agent is not stored when creating a user. Make sure it's the same.
	got.UserAgent = td.Users[0].UserAgent
	got.CreatedAt = td.Users[0].CreatedAt
	got.UpdatedAt = td.Users[0].UpdatedAt

	if !reflect.DeepEqual(got, td.Users[0]) {
		t.Errorf("User mismatch:\nGot  = %+v\nWant = %+v", got, td.Users[0])
	}
}

func RunUserGetAll(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	// Test not found (dummy UIDs).
	got, err := adp.UserGetAll(types.Uid(12345), types.Uid(54321))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 0 {
		t.Error("result users should be zero length, got", len(got))
	}

	got, err = adp.UserGetAll(types.ParseUid(td.Users[0].Id), types.ParseUid(td.Users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("resultUsers length mismatch: got %v want %v", len(got), 2)
	}
	for i, usr := range got {
		// User agent is not compared.
		usr.UserAgent = td.Users[i].UserAgent
		usr.CreatedAt = td.Users[i].CreatedAt
		usr.UpdatedAt = td.Users[i].UpdatedAt
		if !reflect.DeepEqual(&usr, td.Users[i]) {
			t.Errorf("User mismatch:\nGot  = %+v\nWant = %+v", &usr, td.Users[i])
		}
	}
}

func RunUserGetByCred(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	// Test not found
	got, err := adp.UserGetByCred("foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != types.ZeroUid {
		t.Error("result uid should be ZeroUid")
	}

	got, _ = adp.UserGetByCred(td.Creds[0].Method, td.Creds[0].Value)
	if got != types.ParseUid(td.Creds[0].User) {
		t.Errorf("Uid mismatch: got %v want %v", got, types.ParseUid(td.Creds[0].User))
	}
}

func RunCredUpsert(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	// Test just inserts:
	for i := 0; i < 2; i++ {
		inserted, err := adp.CredUpsert(td.Creds[i])
		if err != nil {
			t.Fatal(err)
		}
		if !inserted {
			t.Error("Should be inserted, but updated")
		}
	}

	// Test duplicate:
	_, err := adp.CredUpsert(td.Creds[1])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}
	_, err = adp.CredUpsert(td.Creds[2])
	if err != types.ErrDuplicate {
		t.Error("Should return duplicate error but got", err)
	}

	// Test add new unvalidated credentials
	inserted, err := adp.CredUpsert(td.Creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Error("Should be inserted, but updated")
	}
	inserted, err = adp.CredUpsert(td.Creds[3])
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Error("Should be updated, but inserted")
	}

	// Just insert other creds (used in other tests)
	for _, cred := range td.Creds[4:] {
		_, err = adp.CredUpsert(cred)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func RunAuthAddRecord(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	for _, rec := range td.Recs {
		err := adp.AuthAddRecord(types.ParseUid(rec.UserId), rec.Scheme, rec.Unique,
			rec.AuthLvl, rec.Secret, rec.Expires)
		if err != nil {
			t.Fatal(err)
		}
	}
	//Test duplicate
	err := adp.AuthAddRecord(types.ParseUid(td.Users[0].Id), td.Recs[0].Scheme,
		td.Recs[0].Unique, td.Recs[0].AuthLvl, td.Recs[0].Secret, td.Recs[0].Expires)
	if err != types.ErrDuplicate {
		t.Fatal("Should be duplicate error but got", err)
	}
}

func RunCredGetActive(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	got, err := adp.CredGetActive(types.ParseUserId("usr"+td.Users[2].Id), "tel")
	if err != nil {
		t.Error(err)
	}
	if got == nil {
		t.Error("result should not be nil")
	} else {
		// Compare timestamps using time.Equal because some backends may encode
		// timezone/location names differently (e.g. "+00:00" vs "UTC").
		if !got.CreatedAt.Equal(td.Creds[3].CreatedAt) || !got.UpdatedAt.Equal(td.Creds[3].UpdatedAt) {
			t.Errorf("Credential time mismatch: got %+v want %+v", got, td.Creds[3])
		}
		// Normalize times to expected values and then compare remaining fields.
		got.CreatedAt = td.Creds[3].CreatedAt
		got.UpdatedAt = td.Creds[3].UpdatedAt
		if !reflect.DeepEqual(got, td.Creds[3]) {
			t.Errorf("Credential mismatch: got %+v want %+v", got, td.Creds[3])
		}
	}

	// Test not found
	got, err = adp.CredGetActive(types.ParseUserId("dummyusrid"), "")
	if err != nil {
		t.Error(err)
	}
	if got != nil {
		t.Error("result should be nil, got", got)
	}
}

func RunCredGetAll(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	got, err := adp.CredGetAll(types.ParseUserId("usr"+td.Users[2].Id), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("Credentials length mismatch: got %v want %v", len(got), 3)
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+td.Users[2].Id), "tel", false)
	if len(got) != 2 {
		t.Errorf("Credentials length mismatch: got %v want %v", len(got), 2)
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+td.Users[2].Id), "", true)
	if len(got) != 1 {
		t.Errorf("Credentials length mismatch: got %v want %v", len(got), 1)
	}

	got, _ = adp.CredGetAll(types.ParseUserId("usr"+td.Users[2].Id), "tel", true)
	if len(got) != 1 {
		t.Errorf("Credentials length mismatch: got %v want %v", len(got), 1)
	}
}

func RunAuthGetUniqueRecord(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	uid, authLvl, secret, expires, err := adp.AuthGetUniqueRecord("basic:alice")
	if err != nil {
		t.Fatal(err)
	}
	if uid != types.ParseUserId("usr"+td.Recs[0].UserId) || authLvl != td.Recs[0].AuthLvl || !reflect.DeepEqual(secret, td.Recs[0].Secret) || expires != td.Recs[0].Expires {
		got := fmt.Sprintf("%v %v %v %v", uid, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", td.Recs[0].UserId, td.Recs[0].AuthLvl, td.Recs[0].Secret, td.Recs[0].Expires)
		t.Error("Auth record mismatch:", got, want)
	}

	// Test not found
	uid, _, _, _, err = adp.AuthGetUniqueRecord("qwert:asdfg")
	if err == nil && !uid.IsZero() {
		t.Error("Auth record found but shouldn't. Uid:", uid.String())
	}
}

func RunAuthGetRecord(t *testing.T, adp adapter.Adapter, td *test_data.TestData) {
	t.Helper()

	recId, authLvl, secret, expires, err := adp.AuthGetRecord(types.ParseUserId("usr"+td.Recs[0].UserId), "basic")
	if err != nil {
		t.Fatal(err)
	}
	if recId != td.Recs[0].Unique || authLvl != td.Recs[0].AuthLvl || !reflect.DeepEqual(secret, td.Recs[0].Secret) || expires != td.Recs[0].Expires {
		got := fmt.Sprintf("%v %v %v %v", recId, authLvl, secret, expires)
		want := fmt.Sprintf("%v %v %v %v", td.Recs[0].Unique, td.Recs[0].AuthLvl, td.Recs[0].Secret, td.Recs[0].Expires)
		t.Error("Auth record mismatch:", got, want)
	}

	// Test not found
	recId, _, _, _, err = adp.AuthGetRecord(types.ParseUserId("dummyuserid"), "scheme")
	if err != types.ErrNotFound {
		t.Error("Auth record found but shouldn't. recId:", recId)
	}
}
