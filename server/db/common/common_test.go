package common

import (
	"testing"
	"time"

	"github.com/tinode/chat/server/store/types"
)

func TestSelectEarliestUpdatedSubs(t *testing.T) {
	var testData = []types.Subscription{
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 3, 1, 11, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 2, 2, 12, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 1, 3, 13, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 2, 4, 14, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 1, 5, 15, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 2, 6, 16, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 1, 7, 17, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 2, 8, 18, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 1, 9, 19, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{UpdatedAt: time.Date(2021, time.June, 3, 10, 20, 0, 0, time.Local)}},
	}

	subs := SelectEarliestUpdatedSubs(testData, nil, 100)
	if len(subs) != len(testData) {
		t.Error("Blank query did not return the full set", len(subs))
	}

	subs = SelectEarliestUpdatedSubs(testData, nil, 9)
	if len(subs) != 9 {
		t.Error("Limited query should have returned 9 results but got", len(subs))
	}

	subs = SelectEarliestUpdatedSubs(testData, &types.QueryOpt{Limit: 20}, 9)
	if len(subs) != 9 {
		t.Error("Limited query should have returned 9 results but got", len(subs))
	}
	subs = SelectEarliestUpdatedSubs(testData, &types.QueryOpt{Limit: 9}, 20)
	if len(subs) != 9 {
		t.Error("Limited query should have returned 9 results but got", len(subs))
	}

	ims := time.Date(2021, time.June, 2, 6, 16, 15, 0, time.Local)
	subs = SelectEarliestUpdatedSubs(testData, &types.QueryOpt{Limit: 6, IfModifiedSince: &ims}, 20)
	if len(subs) != 3 {
		t.Error("Limited query should have returned 3 results but got", len(subs))
	}
	subs = SelectEarliestUpdatedSubs(testData, &types.QueryOpt{Limit: 2, IfModifiedSince: &ims}, 20)
	if len(subs) != 2 {
		t.Error("Limited query should have returned 2 results but got", len(subs))
	}
}

func TestSelectEarliestUpdatedAt(t *testing.T) {
	t1 := time.Date(2021, time.June, 3, 10, 20, 0, 0, time.Local)
	t2 := time.Date(2021, time.June, 3, 11, 20, 0, 0, time.Local)
	imsZero := time.Time{}
	// IMS older that t1
	imsOld := time.Date(2021, time.June, 3, 10, 10, 0, 0, time.Local)

	// IMS newer than t1
	imsNew := time.Date(2021, time.June, 3, 10, 30, 0, 0, time.Local)

	ts := SelectEarliestUpdatedAt(t1, t2, imsZero)
	if ts != t2 {
		t.Error("Should return newer time when IMS is zero, got older")
	}
	ts = SelectEarliestUpdatedAt(t2, t1, imsZero)
	if ts != t2 {
		t.Error("Should return newer time when IMS is zero, got older (2)")
	}
	ts = SelectEarliestUpdatedAt(t1, t2, imsOld)
	if ts != t1 {
		t.Error("Should return older time when IMS old, got newer")
	}
	ts = SelectEarliestUpdatedAt(t2, t1, imsOld)
	if ts != t1 {
		t.Error("Should return older time when IMS old, got newer (2)")
	}
	ts = SelectEarliestUpdatedAt(t1, t2, imsNew)
	if ts != t2 {
		t.Error("Should return newer time when IMS is between dates, got older")
	}
}
