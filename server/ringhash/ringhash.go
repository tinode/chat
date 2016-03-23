// Package ringhash implementations a consistent ring hash:
// https://en.wikipedia.org/wiki/Consistent_hashing
package ringhash

import (
	"hash/crc32"
	"log"
	"sort"
	"strconv"
)

type Hash func(data []byte) uint32

type elem struct {
	key  string
	hash int
}

type sortable []elem

func (k sortable) Len() int      { return len(k) }
func (k sortable) Swap(i, j int) { k[i], k[j] = k[j], k[i] }
func (k sortable) Less(i, j int) bool {
	// Weak hash function may cause collisions.
	if k[i].hash < k[j].hash {
		return true
	} else if k[i].hash == k[j].hash {
		return k[i].key < k[j].key
	} else {
		return false
	}
}

type Ring struct {
	keys []elem // Sorted list of keys.

	replicas int
	hashfunc Hash
}

func New(replicas int, fn Hash) *Ring {
	ring := &Ring{
		replicas: replicas,
		hashfunc: fn,
	}
	if ring.hashfunc == nil {
		ring.hashfunc = crc32.ChecksumIEEE
	}
	return ring
}

// Returns the number of keys in the ring.
func (ring *Ring) Len() int {
	return len(ring.keys)
}

// Adds keys to the ring.
func (ring *Ring) Add(keys ...string) {
	for _, key := range keys {
		for i := 0; i < ring.replicas; i++ {
			ring.keys = append(ring.keys, elem{
				hash: int(ring.hashfunc([]byte(strconv.Itoa(i) + key))),
				key:  key})
		}
	}
	sort.Sort(sortable(ring.keys))
}

// Get returns the closest item in the ring to the provided key.
func (ring *Ring) Get(key string) string {

	if ring.Len() == 0 {
		return ""
	}

	hash := int(ring.hashfunc([]byte(key)))

	// Binary search for appropriate replica.
	idx := sort.Search(len(ring.keys), func(i int) bool {
		el := ring.keys[i]
		return (el.hash > hash) || (el.hash == hash && el.key >= key)
		//return (ring.keys[i].hash > hash) || (ring.keys[i].hash == hash && ring.keys[i].key >= key)
	})

	// Means we have cycled back to the first replica.
	if idx == len(ring.keys) {
		idx = 0
	}

	return ring.keys[idx].key
}

func (ring *Ring) dump() {
	for _, e := range ring.keys {
		log.Printf("key: '%s', hash=%d", e.key, e.hash)
	}
}
