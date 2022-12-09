package main

import (
	"testing"
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
		t.Errorf("%s: expected %+v, got %+v", name, expected, gotten)
	}
}

func TestStringSliceDelta(t *testing.T) {
	// Case format:
	// - inputs: old, new
	// - expected outputs: added, removed, intersection
	cases := [][5][]string{
		[5][]string{
			[]string{"abc", "def", "fff"}, []string{},
			[]string{}, []string{"abc", "def", "fff"}, []string{},
		},
		[5][]string{
			[]string{}, []string{}, []string{}, []string{}, []string{},
		},
		[5][]string{
			[]string{"aa", "xx", "bb", "aa", "bb"}, []string{"yy", "aa"},
			[]string{"yy"}, []string{"aa", "bb", "bb", "xx"}, []string{"aa"},
		},
		[5][]string{
			[]string{"bb", "aa", "bb"}, []string{"yy", "aa", "bb", "zzz", "zzz", "cc"},
			[]string{"cc", "yy", "zzz", "zzz"}, []string{"bb"}, []string{"aa", "bb"},
		},
		[5][]string{
			[]string{"aa", "aa", "aa"}, []string{"aa", "aa", "aa"},
			[]string{}, []string{}, []string{"aa", "aa", "aa"},
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
