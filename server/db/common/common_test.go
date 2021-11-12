package common

import (
	"strings"
	"testing"
	"time"

	"github.com/tinode/chat/server/store/types"
)

func genTestData() []types.Subscription {
	var testData = []types.Subscription{
		{ObjHeader: types.ObjHeader{Id: "1", UpdatedAt: time.Date(2021, time.June, 1, 1, 11, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "2", UpdatedAt: time.Date(2021, time.June, 2, 2, 12, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "3", UpdatedAt: time.Date(2021, time.June, 3, 3, 13, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "4", UpdatedAt: time.Date(2021, time.June, 4, 4, 14, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "5", UpdatedAt: time.Date(2021, time.June, 5, 5, 15, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "6", UpdatedAt: time.Date(2021, time.June, 6, 6, 16, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "7", UpdatedAt: time.Date(2021, time.June, 7, 7, 17, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "8", UpdatedAt: time.Date(2021, time.June, 8, 8, 18, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "9", UpdatedAt: time.Date(2021, time.June, 9, 9, 19, 0, 0, time.Local)}},
		{ObjHeader: types.ObjHeader{Id: "10", UpdatedAt: time.Date(2021, time.June, 10, 10, 20, 0, 0, time.Local)}},
	}

	// TouchedAt is either greater or equal to UpdatedAt.
	testData[0].SetTouchedAt(time.Date(2021, time.June, 1, 1, 11, 0, 0, time.Local))   // 1
	testData[1].SetTouchedAt(time.Date(2021, time.June, 4, 4, 12, 0, 0, time.Local))   // 3
	testData[2].SetTouchedAt(time.Date(2021, time.June, 4, 2, 13, 0, 0, time.Local))   // 2
	testData[3].SetTouchedAt(time.Date(2021, time.June, 4, 4, 14, 0, 0, time.Local))   // 4
	testData[4].SetTouchedAt(time.Date(2021, time.June, 7, 5, 15, 0, 0, time.Local))   // 6
	testData[5].SetTouchedAt(time.Date(2021, time.June, 6, 6, 16, 0, 0, time.Local))   // 5
	testData[6].SetTouchedAt(time.Date(2021, time.June, 7, 7, 17, 0, 0, time.Local))   // 7
	testData[7].SetTouchedAt(time.Date(2021, time.June, 9, 8, 18, 0, 0, time.Local))   // 8
	testData[8].SetTouchedAt(time.Date(2021, time.June, 10, 11, 19, 0, 0, time.Local)) // 10
	testData[9].SetTouchedAt(time.Date(2021, time.June, 10, 10, 20, 0, 0, time.Local)) // 9

	return testData
}

func TestSelectEarliestUpdatedSubs(t *testing.T) {

	getOrder := func(subs []types.Subscription) string {
		var order []string
		for i := 0; i < len(subs); i++ {
			order = append(order, subs[i].Id)
		}
		return strings.Join(order, ",")
	}

	subs := SelectEarliestUpdatedSubs(genTestData(), nil, 100)

	// No sorting when returning the full set.
	expectedOrder := "1,2,3,4,5,6,7,8,9,10"
	sortOrder := getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Wrong results returned. Expected:", expectedOrder, "; Got:", sortOrder)
	}

	// Sorted, oldest 9 results.
	subs = SelectEarliestUpdatedSubs(genTestData(), nil, 9)
	expectedOrder = "1,3,2,4,6,5,7,8,10"
	sortOrder = getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Limited query returned wrong results. Expected:", expectedOrder, "; Got:", sortOrder)
	}

	// Sorted, oldest 9 results.
	subs = SelectEarliestUpdatedSubs(genTestData(), &types.QueryOpt{Limit: 20}, 9)
	expectedOrder = "1,3,2,4,6,5,7,8,10"
	sortOrder = getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Limited query (2) returned wrong results. Expected:", expectedOrder, "; Got:", sortOrder)
	}

	// Sorted, oldest 9 results.
	subs = SelectEarliestUpdatedSubs(genTestData(), &types.QueryOpt{Limit: 9}, 20)
	expectedOrder = "1,3,2,4,6,5,7,8,10"
	sortOrder = getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Limited query (3) returned wrong results. Expected:", expectedOrder, "; Got:", sortOrder)
	}

	ims := time.Date(2021, time.June, 7, 8, 16, 15, 0, time.Local)
	subs = SelectEarliestUpdatedSubs(genTestData(), &types.QueryOpt{Limit: 6, IfModifiedSince: &ims}, 20)
	expectedOrder = "8,10,9"
	sortOrder = getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Date & count limited query returned wrong results. Expected:", expectedOrder, "; Got:", sortOrder)
	}

	ims = time.Date(2021, time.June, 4, 4, 13, 15, 0, time.Local)
	subs = SelectEarliestUpdatedSubs(genTestData(), &types.QueryOpt{Limit: 3, IfModifiedSince: &ims}, 20)
	expectedOrder = "4,6,5"
	sortOrder = getOrder(subs)
	if sortOrder != expectedOrder {
		t.Error("Count & date limited query returned wrong results. Expected:", expectedOrder, "; Got:", sortOrder)
	}
}
