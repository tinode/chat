package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tinode/chat/server/store/types"
)

func slicesEqual(expected, gotten []string) bool {
	if len(expected) != len(gotten) {
		return false
	}
	for i, v := range expected {
		if v != gotten[i] {
			return false
		}
	}
	return true
}

func expectSlicesEqual(t *testing.T, name string, expected, gotten []string) {
	if !slicesEqual(expected, gotten) {
		e := "'" + strings.Join(expected, "','") + "'"
		g := "'" + strings.Join(gotten, "','") + "'"
		t.Errorf("%s: expected %+v, got %+v", name, e, g)
	}
}

func TestStringSliceDelta(t *testing.T) {
	// Case format:
	// - inputs: old, new
	// - expected outputs: added, removed, intersection
	cases := [][5][]string{
		{
			{"abc", "def", "fff"}, {},
			{}, {"abc", "def", "fff"}, {},
		},
		{
			{}, {}, {}, {}, {},
		},
		{
			{"aa", "xx", "bb", "aa", "bb"}, {"yy", "aa"},
			{"yy"}, {"aa", "bb", "bb", "xx"}, {"aa"},
		},
		{
			{"bb", "aa", "bb"}, {"yy", "aa", "bb", "zzz", "zzz", "cc"},
			{"cc", "yy", "zzz", "zzz"}, {"bb"}, {"aa", "bb"},
		},
		{
			{"aa", "aa", "aa"}, {"aa", "aa", "aa"},
			{}, {}, {"aa", "aa", "aa"},
		},
	}

	for _, tc := range cases {
		added, removed, both := stringSliceDelta(
			tc[0], tc[1],
		)
		expectSlicesEqual(t, "added", tc[2], added)
		expectSlicesEqual(t, "removed", tc[3], removed)
		expectSlicesEqual(t, "both", tc[4], both)

	}
}

func TestParseSearchQuery(t *testing.T) {
	cases := []struct {
		query       string
		expectedAnd []string
		expectedOr  []string
		expectError bool
	}{
		{
			query:       `tag1 tag2 tag3`,
			expectedAnd: []string{"tag1", "tag2", "tag3"},
			expectedOr:  []string{},
			expectError: false,
		},
		{
			query:       `tag1,tag2,tag3`,
			expectedAnd: []string{},
			expectedOr:  []string{"tag1", "tag2", "tag3"},
			expectError: false,
		},
		{
			query:       `tag1 tag2,tag3`,
			expectedAnd: []string{"tag1"},
			expectedOr:  []string{"tag2", "tag3"},
			expectError: false,
		},
		{
			query:       `tag1 tag2,tag3 "tag4 tag5"`,
			expectedAnd: []string{"tag1", "tag4 tag5"},
			expectedOr:  []string{"tag2", "tag3"},
			expectError: false,
		},
		{
			query:       `tag1,tag2 tag3`,
			expectedAnd: []string{"tag3"},
			expectedOr:  []string{"tag1", "tag2"},
			expectError: false,
		},
		{
			query:       `"tag1 tag2" tag3,tag4`,
			expectedAnd: []string{"tag1 tag2"},
			expectedOr:  []string{"tag3", "tag4"},
			expectError: false,
		},
		{
			query:       `tag1 "tag2 tag3"`,
			expectedAnd: []string{"tag1", "tag2 tag3"},
			expectedOr:  []string{},
			expectError: false,
		},
		{
			query:       `tag1, tag2, tag3`,
			expectedAnd: []string{},
			expectedOr:  []string{"tag1", "tag2", "tag3"},
			expectError: false,
		},
		{
			query:       `tag1 , tag2 ,tag3`,
			expectedAnd: []string{},
			expectedOr:  []string{"tag1", "tag2", "tag3"},
			expectError: false,
		},
		{
			query:       `tag1     ,    tag2     tag3`,
			expectedAnd: []string{"tag3"},
			expectedOr:  []string{"tag1", "tag2"},
			expectError: false,
		},
		{
			query:       `tag1 "unterminated quote`,
			expectedAnd: nil,
			expectedOr:  nil,
			expectError: true,
		},
		{
			query:       `tag1,,tag2`,
			expectedAnd: nil,
			expectedOr:  nil,
			expectError: true,
		},
		{
			query:       `tag1 "tag2" tag3`,
			expectedAnd: []string{"tag1", "tag2", "tag3"},
			expectedOr:  []string{},
			expectError: false,
		},
		{
			query:       `tag1"tag2" quote in the middle`,
			expectedAnd: nil,
			expectedOr:  nil,
			expectError: true,
		},
	}

	for _, tc := range cases {
		and, or, err := parseSearchQuery(tc.query)
		if tc.expectError {
			if err == nil {
				t.Errorf("expected error for query '%s', got none", tc.query)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for query '%s': %v", tc.query, err)
			} else {
				expectSlicesEqual(t, tc.query+" AND", tc.expectedAnd, and)
				expectSlicesEqual(t, tc.query+" OR", tc.expectedOr, or)
			}
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	cases := []struct {
		input    []string
		expected types.StringSlice
	}{
		{
			input:    []string{"  Tag1  ", "tag2", "TAG3", "tag1"},
			expected: types.StringSlice{"tag1", "tag2", "tag3"},
		},
		{
			input:    []string{"  ", "tag2", "TAG3", "tag1"},
			expected: types.StringSlice{"tag1", "tag2", "tag3"},
		},
		{
			input:    []string{"tag1"},
			expected: types.StringSlice{"tag1"},
		},
		{
			input:    []string{"tag1", nullValue},
			expected: []string{},
		},
		{
			input:    []string{},
			expected: nil,
		},
	}

	for _, tc := range cases {
		got := normalizeTags(tc.input, 16)
		expectSlicesEqual(t, "normalizeTags", tc.expected, got)
	}
}

func TestRestrictedTagsEqual(t *testing.T) {
	cases := []struct {
		oldTags    []string
		newTags    []string
		namespaces map[string]bool
		expected   bool
	}{
		{
			oldTags:    []string{"ns1:tag1", "ns2:tag2"},
			newTags:    []string{"ns1:tag1", "ns2:tag2"},
			namespaces: map[string]bool{"ns1": true, "ns2": true},
			expected:   true,
		},
		{
			oldTags:    []string{"ns1:tag1", "ns2:tag2"},
			newTags:    []string{"ns1:tag1", "ns2:tag3"},
			namespaces: map[string]bool{"ns1": true, "ns2": true},
			expected:   false,
		},
		{
			oldTags:    []string{"ns1:tag1", "ns2:tag2"},
			newTags:    []string{"ns1:tag1"},
			namespaces: map[string]bool{"ns1": true, "ns2": true},
			expected:   false,
		},
	}

	for _, tc := range cases {
		got := restrictedTagsEqual(tc.oldTags, tc.newTags, tc.namespaces)
		if got != tc.expected {
			t.Errorf("restrictedTagsEqual: expected %v, got %v", tc.expected, got)
		}
	}
}

func TestIsNullValue(t *testing.T) {
	cases := []struct {
		input    any
		expected bool
	}{
		{
			input:    nullValue,
			expected: true,
		},
		{
			input:    "some string",
			expected: false,
		},
		{
			input:    123,
			expected: false,
		},
		{
			input:    nil,
			expected: false,
		},
	}

	for _, tc := range cases {
		got := isNullValue(tc.input)
		if got != tc.expected {
			t.Errorf("isNullValue: expected %v, got %v", tc.expected, got)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected int
	}{
		{
			input:    "1.2.3",
			expected: (1 << 16) | (2 << 8) | 3,
		},
		{
			input:    "1.2",
			expected: (1 << 16) | (2 << 8),
		},
		{
			input:    "1",
			expected: (1 << 16),
		},
		{
			input:    "v1.2.3",
			expected: (1 << 16) | (2 << 8) | 3,
		},
		{
			input:    "v1.2",
			expected: (1 << 16) | (2 << 8),
		},
		{
			input:    "v1",
			expected: (1 << 16),
		},
		{
			input:    "1.2.3-rc1",
			expected: (1 << 16) | (2 << 8) | 3,
		},
		{
			input:    "v1.2.3-rc1",
			expected: (1 << 16) | (2 << 8) | 3,
		},
		{
			input:    "0.0.0",
			expected: 0,
		},
		{
			input:    "v0.0.0",
			expected: 0,
		},
		{
			input:    "1.2.8192", // 8192 is greater than 0x1fff, should be ignored
			expected: (1 << 16) | (2 << 8),
		},
		{
			input:    "1.8192.3", // 8192 is greater than 0x1fff, should be ignored
			expected: (1 << 16) | 3,
		},
		{
			input:    "8192.2.3", // 8192 is greater than 0x1fff, should be ignored
			expected: (2 << 8) | 3,
		},
		{
			input:    "",
			expected: 0,
		},
	}

	for _, tc := range cases {
		got := parseVersion(tc.input)
		if got != tc.expected {
			t.Errorf("parseVersion(%q): expected %d, got %d", tc.input, tc.expected, got)
		}
	}
}

func TestMergeMaps(t *testing.T) {
	cases := []struct {
		dst      map[string]any
		src      map[string]any
		expected map[string]any
		changed  bool
	}{
		{
			dst:      map[string]any{"a": 1, "b": 2},
			src:      map[string]any{"b": 3, "c": 4},
			expected: map[string]any{"a": 1, "b": 3, "c": 4},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1, "b": map[string]any{"x": 1}},
			src:      map[string]any{"b": map[string]any{"y": 2}},
			expected: map[string]any{"a": 1, "b": map[string]any{"x": 1, "y": 2}},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1, "b": map[string]any{"x": 1}},
			src:      map[string]any{"b": map[string]any{"x": nullValue}},
			expected: map[string]any{"a": 1, "b": map[string]any{}},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1, "b": 2},
			src:      map[string]any{},
			expected: map[string]any{"a": 1, "b": 2},
			changed:  false,
		},
		{
			dst:      nil,
			src:      map[string]any{"a": 1},
			expected: map[string]any{"a": 1},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1},
			src:      map[string]any{"a": nullValue},
			expected: map[string]any{},
			changed:  true,
		},
	}

	for _, tc := range cases {
		got, changed := mergeMaps(tc.dst, tc.src)
		if !reflect.DeepEqual(got, tc.expected) || changed != tc.changed {
			t.Errorf("mergeMaps(%v, %v): expected (%v, %v), got (%v, %v)", tc.dst, tc.src, tc.expected, tc.changed, got, changed)
		}
	}
}
func TestMergeInterfaces(t *testing.T) {
	cases := []struct {
		dst      any
		src      any
		expected any
		changed  bool
	}{
		{
			dst:      map[string]any{"a": 1, "b": map[string]any{"x": 1}},
			src:      map[string]any{"b": map[string]any{"y": 2}},
			expected: map[string]any{"a": 1, "b": map[string]any{"x": 1, "y": 2}},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1, "b": map[string]any{"x": 1}},
			src:      map[string]any{"b": map[string]any{"x": nullValue}},
			expected: map[string]any{"a": 1, "b": map[string]any{}},
			changed:  true,
		},
		{
			dst:      map[string]any{"a": 1},
			src:      nullValue,
			expected: nil,
			changed:  true,
		},
		{
			dst:      "old string",
			src:      "new string",
			expected: "new string",
			changed:  true,
		},
		{
			dst:      "old string",
			src:      12345,
			expected: 12345,
			changed:  true,
		},
		{
			dst:      "old string",
			src:      nullValue,
			expected: nil,
			changed:  true,
		},
		{
			dst:      123,
			src:      456,
			expected: 456,
			changed:  true,
		},
		{
			dst:      123,
			src:      nil,
			expected: 123,
			changed:  false,
		},
		{
			dst:      123,
			src:      true,
			expected: true,
			changed:  true,
		},
		{
			dst:      []string{"a", "b", "c"},
			src:      []string{"d", "e", "f"},
			expected: []string{"d", "e", "f"},
			changed:  true,
		},
	}

	for _, tc := range cases {
		got, changed := mergeInterfaces(tc.dst, tc.src)
		if !reflect.DeepEqual(got, tc.expected) || changed != tc.changed {
			t.Errorf("mergeInterfaces(%v, %v): expected (%v, %v), got (%v, %v)", tc.dst, tc.src, tc.expected, tc.changed, got, changed)
		}
	}
}

func TestFilterTags(t *testing.T) {
	cases := []struct {
		tags     []string
		ns       map[string]bool
		expected []string
	}{
		{
			tags:     []string{"ns1:tag1", "ns2:tag3", "nons", "inval::tag", ":tag3", "tag4:", "tag5: "},
			ns:       map[string]bool{"ns1": true, "ns2": false, "xtra": true},
			expected: []string{"ns1:tag1"},
		},
		{
			tags:     []string{"ns1:tag1", "ns2:tag3", "nons", "inval::tag", ":tag3", "tag4:", "tag5: "},
			ns:       map[string]bool{},
			expected: nil,
		},
	}

	for _, tc := range cases {
		got := filterTags(tc.tags, tc.ns)
		if !reflect.DeepEqual(got, tc.expected) {
			t.Errorf("filterTags(%v, %v): expected (%v), got (%v)", tc.tags, tc.ns, tc.expected, got)
		}
	}
}

func TestHasDuplicateNamespaceTags(t *testing.T) {
	cases := []struct {
		tags     []string
		ns       string
		expected bool
	}{
		{
			tags:     []string{"ns1:tag1", "ns2:tag3", "nons", "inval::tag", ":tag3", "tag4:", "tag5: "},
			ns:       "ns1",
			expected: false,
		},
		{
			tags:     []string{"ns1:tag1", "ns2:tag3", "nons", "inval::tag", ":tag3", "tag4:", "tag5: ", "ns1:tag2"},
			ns:       "ns1",
			expected: true,
		},
		{
			tags:     []string{"ns1:tag1", "ns2:tag3", "nons", "inval::tag", ":tag3", "tag4:", "tag5: "},
			ns:       "",
			expected: false,
		},
	}

	for _, tc := range cases {
		got := hasDuplicateNamespaceTags(tc.tags, tc.ns)
		if !reflect.DeepEqual(got, tc.expected) {
			t.Errorf("filterTags(%v, %v): expected (%v), got (%v)", tc.tags, tc.ns, tc.expected, got)
		}
	}
}
