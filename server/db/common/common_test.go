package common

import (
	"encoding/json"
	"reflect"
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
		for i := range subs {
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

func TestSelectLatestTime(t *testing.T) {
	t1 := time.Date(2021, time.June, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2021, time.June, 2, 10, 0, 0, 0, time.UTC)

	// t1 is before t2, should return t2
	result := SelectLatestTime(t1, t2)
	if !result.Equal(t2) {
		t.Errorf("Expected %v, got %v", t2, result)
	}

	// t2 is after t1, should return t2
	result = SelectLatestTime(t2, t1)
	if !result.Equal(t2) {
		t.Errorf("Expected %v, got %v", t2, result)
	}

	// Equal times, should return either one (in this case t1)
	result = SelectLatestTime(t1, t1)
	if !result.Equal(t1) {
		t.Errorf("Expected %v, got %v", t1, result)
	}
}

func TestRangesToSql(t *testing.T) {
	// Test single range with Hi = 0 (IN clause)
	ranges := []types.Range{{Low: 5, Hi: 0}}
	sql, args := RangesToSql(ranges)
	expectedSql := "IN (?)"
	expectedArgs := []any{5}
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}

	// Test single range with Hi > 0 (BETWEEN clause)
	ranges = []types.Range{{Low: 5, Hi: 8}}
	sql, args = RangesToSql(ranges)
	expectedSql = "BETWEEN ? AND ?"
	expectedArgs = []any{5, 7} // Hi-1 for BETWEEN
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}

	// Test multiple ranges (IN clause)
	ranges = []types.Range{{Low: 1, Hi: 3}, {Low: 5, Hi: 0}, {Low: 8, Hi: 10}}
	sql, args = RangesToSql(ranges)
	expectedSql = "IN (?,?,?,?,?)"
	expectedArgs = []any{1, 2, 5, 8, 9}
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}
}

func TestDisjunctionSql(t *testing.T) {
	// Test single disjunction
	req := [][]string{{"tag1", "tag2", "tag3"}}
	sql, args := DisjunctionSql(req, "tagname")
	expectedSql := "HAVING COUNT(tagname IN (?,?,?) OR NULL)>=1 "
	expectedArgs := []any{"tag1", "tag2", "tag3"}
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}

	// Test multiple disjunctions
	req = [][]string{{"tag1", "tag2"}, {"tag3"}, {"tag4", "tag5"}}
	sql, args = DisjunctionSql(req, "fieldname")
	expectedSql = "HAVING COUNT(fieldname IN (?,?) OR NULL)>=1 AND COUNT(fieldname IN (?) OR NULL)>=1 AND COUNT(fieldname IN (?,?) OR NULL)>=1 "
	expectedArgs = []any{"tag1", "tag2", "tag3", "tag4", "tag5"}
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}

	// Test empty disjunctions (should be skipped)
	req = [][]string{{"tag1"}, {}, {"tag2"}}
	sql, args = DisjunctionSql(req, "fieldname")
	expectedSql = "HAVING COUNT(fieldname IN (?) OR NULL)>=1 AND COUNT(fieldname IN (?) OR NULL)>=1 "
	expectedArgs = []any{"tag1", "tag2"}
	if sql != expectedSql {
		t.Errorf("Expected SQL '%s', got '%s'", expectedSql, sql)
	}
	if !reflect.DeepEqual(args, expectedArgs) {
		t.Errorf("Expected args %v, got %v", expectedArgs, args)
	}
}

func TestFilterFoundTags(t *testing.T) {
	setTags := types.StringSlice{"tag1", "tag2", "tag3", "tag4", "tag5"}
	index := map[string]struct{}{
		"tag1": {},
		"tag3": {},
		"tag5": {},
		"tag6": {}, // Not in setTags
	}

	result := FilterFoundTags(setTags, index)
	expected := []string{"tag1", "tag3", "tag5"}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}

	// Test with empty index
	emptyIndex := map[string]struct{}{}
	result = FilterFoundTags(setTags, emptyIndex)
	expected = []string{}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}

	// Test with empty setTags
	emptyTags := types.StringSlice{}
	result = FilterFoundTags(emptyTags, index)
	expected = []string{}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}
}

func TestToJSON(t *testing.T) {
	// Test with nil
	result := ToJSON(nil)
	if result != nil {
		t.Errorf("Expected nil, got %v", result)
	}

	// Test with string
	input := "test string"
	result = ToJSON(input)
	expected := []byte(`"test string"`)
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Expected %v, got %v", expected, result)
	}

	// Test with map
	input2 := map[string]any{"key": "value", "number": 42}
	result = ToJSON(input2)
	// Parse back to verify
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Errorf("Failed to unmarshal result: %v", err)
	}
	if parsed["key"] != "value" || parsed["number"] != float64(42) {
		t.Errorf("JSON conversion failed, got %v", parsed)
	}
}

func TestFromJSON(t *testing.T) {
	// Test with nil
	result := FromJSON(nil)
	if result != nil {
		t.Errorf("Expected nil, got %v", result)
	}

	// Test with valid JSON bytes
	input := []byte(`{"key": "value", "number": 42}`)
	result = FromJSON(input)
	if resultMap, ok := result.(map[string]any); ok {
		if resultMap["key"] != "value" || resultMap["number"] != float64(42) {
			t.Errorf("JSON deserialization failed, got %v", resultMap)
		}
	} else {
		t.Errorf("Expected map[string]any, got %T", result)
	}

	// Test with invalid JSON bytes
	invalidInput := []byte(`{invalid json}`)
	result = FromJSON(invalidInput)
	if result != nil {
		t.Errorf("Expected nil for invalid JSON, got %v", result)
	}

	// Test with non-byte slice
	stringInput := "not bytes"
	result = FromJSON(stringInput)
	if result != nil {
		t.Errorf("Expected nil for non-byte input, got %v", result)
	}
}

func TestUpdateByMap(t *testing.T) {
	update := map[string]any{
		"Name":      "John Doe",
		"Age":       30,
		"Public":    map[string]string{"avatar": "url"},
		"Private":   map[string]string{"email": "john@example.com"},
		"Trusted":   map[string]bool{"verified": true},
		"UpdatedAt": time.Now(),
	}

	cols, args := UpdateByMap(update)

	// Check that we have the right number of columns and args
	if len(cols) != len(args) || len(cols) != len(update) {
		t.Errorf("Expected %d columns and args, got %d cols and %d args", len(update), len(cols), len(args))
	}

	// Verify column format
	for _, col := range cols {
		if !strings.Contains(col, "=?") {
			t.Errorf("Column should contain '=?', got %s", col)
		}
	}

	// Check that JSON fields are properly handled
	foundPublic := false
	foundPrivate := false
	foundTrusted := false
	for i, col := range cols {
		if strings.HasPrefix(col, "public=?") {
			foundPublic = true
			// Should be JSON bytes
			if _, ok := args[i].([]byte); !ok {
				t.Errorf("Public field should be []byte, got %T", args[i])
			}
		}
		if strings.HasPrefix(col, "private=?") {
			foundPrivate = true
			if _, ok := args[i].([]byte); !ok {
				t.Errorf("Private field should be []byte, got %T", args[i])
			}
		}
		if strings.HasPrefix(col, "trusted=?") {
			foundTrusted = true
			if _, ok := args[i].([]byte); !ok {
				t.Errorf("Trusted field should be []byte, got %T", args[i])
			}
		}
	}

	if !foundPublic || !foundPrivate || !foundTrusted {
		t.Error("Missing JSON fields in output")
	}
}

func TestExtractTags(t *testing.T) {
	// Test with Tags field present
	update := map[string]any{
		"Name": "John",
		"Tags": types.StringSlice{"tag1", "tag2", "tag3"},
		"Age":  30,
	}
	tags := ExtractTags(update)
	expected := []string{"tag1", "tag2", "tag3"}
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Expected %v, got %v", expected, tags)
	}

	// Test with no Tags field
	update = map[string]any{
		"Name": "John",
		"Age":  30,
	}
	tags = ExtractTags(update)
	expected = nil
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Expected %+v, got %+v", expected, tags)
	}

	// Test with nil Tags field
	update = map[string]any{
		"Name": "John",
		"Tags": nil,
		"Age":  30,
	}
	tags = ExtractTags(update)
	expected = nil
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Expected %+v, got %+v", expected, tags)
	}

	// Test with wrong type for Tags field
	update = map[string]any{
		"Name": "John",
		"Tags": "not a slice",
		"Age":  30,
	}
	tags = ExtractTags(update)
	expected = nil
	if !reflect.DeepEqual(tags, expected) {
		t.Errorf("Expected %+v, got %+v", expected, tags)
	}
}
