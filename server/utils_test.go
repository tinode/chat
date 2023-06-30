package main

import (
	"fmt"
	"net"
	"testing"
	"time"

	sf "github.com/tinode/snowflake"
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

func TestIp(t *testing.T) {
	_, block, _ := net.ParseCIDR("192.168.0.0/24")
	ip := net.ParseIP("192.168.1.1")
	fmt.Printf("block.Contains(ip): %v\n", block.Contains(ip))
}

func TestTim(t *testing.T) {
	killTimer1 := time.NewTimer(time.Second * 6)
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	for {
		select {
		case <-killTimer1.C:
			killTimer.Reset(time.Second * 2)
			fmt.Println("6s")
		case <-killTimer.C:
			fmt.Println("被激活")
			return
		}
	}
}

func TestNext(t *testing.T) {
	sf, err := sf.NewSnowFlake(1)
	if err != nil {
		t.Error(err)
	}

	id, err := sf.Next()
	if err != nil {
		t.Error(err)
	}

	id2, err := sf.Next()
	if err != nil {
		t.Error(err)
	}
	id3, err := sf.Next()
	if err != nil {
		t.Error(err)
	}
	fmt.Println(id, id2, id3)
	if id >= id2 {
		t.Errorf("id %v is smaller or equal to previous one %v", id2, id)
	}
}
