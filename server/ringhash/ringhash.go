// Package ringhash implementats a consistent ring hash:
// https://en.wikipedia.org/wiki/Consistent_hashing
package ringhash

import (
	"encoding/ascii85"
	"hash/crc32"
	"hash/fnv"
	"sort"
	"strconv"

	"github.com/tinode/chat/server/logs"
)

// Hash is a signature of a hash function used by the package.
type Hash func(data []byte) uint32

type elem struct {
	key  string
	hash uint32
}

type sortable []elem

func (k sortable) Len() int      { return len(k) }
func (k sortable) Swap(i, j int) { k[i], k[j] = k[j], k[i] }
func (k sortable) Less(i, j int) bool {
	// Weak hash function may cause collisions.
	if k[i].hash < k[j].hash {
		return true
	}
	if k[i].hash == k[j].hash {
		return k[i].key < k[j].key
	}
	return false
}

// Ring is the definition of the ringhash.
type Ring struct {
	keys []elem // Sorted list of keys.

	signature string
	replicas  int
	hashfunc  Hash
}

// New initializes an empty ringhash with the given number of replicas and a hash function.
// If the hash function is nil, crc32.NewIEEE() is used.
func New(replicas int, fn Hash) *Ring {
	ring := &Ring{
		replicas: replicas,
		hashfunc: fn,
	}
	if ring.hashfunc == nil {
		ring.hashfunc = func(data []byte) uint32 {
			hash := crc32.NewIEEE()
			hash.Write(data)
			return hash.Sum32()
		}
	}
	return ring
}

// Len returns the number of keys in the ring.
func (ring *Ring) Len() int {
	return len(ring.keys)
}

// Add adds keys to the ring.
func (ring *Ring) Add(keys ...string) {
	for _, key := range keys {
		for i := 0; i < ring.replicas; i++ {
			ring.keys = append(ring.keys, elem{
				hash: ring.hashfunc([]byte(strconv.Itoa(i) + key)),
				key:  key})
		}
	}
	sort.Sort(sortable(ring.keys))

	// Calculate signature
	hash := fnv.New128a()
	b := make([]byte, 4)
	for _, key := range ring.keys {
		b[0] = byte(key.hash)
		b[1] = byte(key.hash >> 8)
		b[2] = byte(key.hash >> 16)
		b[3] = byte(key.hash >> 24)
		hash.Write(b)
		hash.Write([]byte(key.key))
	}

	b = []byte{}
	b = hash.Sum(b)
	dst := make([]byte, ascii85.MaxEncodedLen(len(b)))
	ascii85.Encode(dst, b)
	ring.signature = string(dst)
}

// Get returns the closest item in the ring to the provided key.
func (ring *Ring) Get(key string) string {

	if ring.Len() == 0 {
		return ""
	}

	hash := ring.hashfunc([]byte(key))

	// Binary search for appropriate replica.
	idx := sort.Search(len(ring.keys), func(i int) bool {
		el := ring.keys[i]
		return (el.hash > hash) || (el.hash == hash && el.key >= key)
	})

	// Means we have cycled back to the first replica.
	if idx == len(ring.keys) {
		idx = 0
	}

	return ring.keys[idx].key
}

// Signature returns the ring's hash signature. Two identical ringhashes
// will have the same signature. Two hashes with different
// number of keys or replicas or hash functions will have different
// signatures.
func (ring *Ring) Signature() string {
	return ring.signature
}

func (ring *Ring) dump() {
	for _, e := range ring.keys {
		logs.Info.Printf("key: '%s', hash=%d", e.key, e.hash)
	}
}
