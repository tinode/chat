package ringhash_test

import (
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"testing"

	"github.com/tinode/chat/server/ringhash"
)

func TestHashing(t *testing.T) {

	// Ring with 3 elements hashed by crc32.ChecksumIEEE
	ring := ringhash.New(3, crc32.ChecksumIEEE)
	ring.Add("A", "B", "C")

	// The ring contains:
	// B0 =  105710768 -> B
	// B1 =  525743601 -> B
	// B2 =  880502322 -> B
	// C2 = 1132222116 -> C
	// C1 = 1750140263 -> C
	// C0 = 1900688422 -> C
	// A1 = 2254398539 -> A
	// A0 = 2672055562 -> A
	// A2 = 2909943688 -> A

	// Key=A, Hash=3554254475 -> B0
	// Key=B, Hash=1255198513 -> C1
	// Key=C, Hash=1037565863 -> C2
	// Key=D, Hash=2746444292 -> A2
	// Key=E, Hash=3568589458 -> B0
	// Key=F, Hash=1304234792 -> C1

	testHashes := map[string]string{
		"A": "B",
		"B": "C",
		"C": "C",
		"D": "A",
		"E": "B",
		"F": "C",
	}

	for k, v := range testHashes {
		n := ring.Get(k)
		if n != v {
			t.Errorf("Key '%s', expecting '%s', got '%s'", k, v, n)
		}
	}

	ring.Add("X")
	// Adding
	// X0 = 4214226378
	// X1 = 3795111051
	// X2 = 3373899592

	// Changes to mapping:
	testHashes["A"] = "X"
	testHashes["E"] = "X"

	for k, v := range testHashes {
		n := ring.Get(k)
		if n != v {
			t.Errorf("Key '%s, expecting %s, got %s", k, v, n)
		}
	}
}

func TestConsistency(t *testing.T) {
	ring1 := ringhash.New(3, nil)
	ring2 := ringhash.New(3, nil)

	ring1.Add("owl", "crow", "sparrow")
	ring2.Add("sparrow", "owl", "crow")

	if ring1.Get("duck") != ring2.Get("duck") {
		t.Errorf("'duck' should map to 'sparrow' in both cases")
	}

	// Collision test: these strings generate CRC32 collisions
	// Google's implementation fails this test.
	// 0VXGD 0BGABAA
	// 0VXGG 0BGABAB
	// 0VXGF 0BGABAC
	// 0VXGA 0BGABAD
	// 0VXGC 0BGABAF
	// 0VXGB 0BGABAG
	// 0VXGM 0BGABAH
	// 0VXGL 0BGABAI
	// 0VXGO 0BGABAJ
	// 0VXGN 0BGABAK
	// 0VXGI 0BGABAL

	ring1 = ringhash.New(1, crc32.ChecksumIEEE)
	ring2 = ringhash.New(1, crc32.ChecksumIEEE)

	ring1.Add("VXGD", "BGABAA", "VXGG", "BGABAB", "VXGF", "BGABAC")
	ring2.Add("BGABAA", "VXGD", "BGABAB", "VXGG", "BGABAC", "VXGF")

	str := []string{
		"datsam", "kGmVht", "dSPmEr", "RloWQr", "WFkAkG", "gLBNPX", "twEwll", "RnRdaf",
		"ruEMuJ", "ZvXJsJ", "xjQzKD", "CKfSFg", "BMKMvM", "PSzYdC", "CsxqTR", "IbzdXz",
		"xdnZGj", "VdHcVp", "iVgIvH", "bZsTIX", "CyRBUO", "ylgEGS", "vOTwJD", "JZbyFU",
		"Hayrly", "jQQkOV", "NEVjlJ", "SkJfie", "HrdJuL", "ASwkXH", "UwJOmo", "nfbrxA",
	}
	for _, key := range str {
		if ring1.Get(key) != ring2.Get(key) {
			t.Errorf("'%s' should map to the same bin in both cases", key)
		}
	}
}

func TestSignature(t *testing.T) {
	ring1 := ringhash.New(4, nil)
	ring2 := ringhash.New(4, nil)

	ring1.Add("owl", "crow", "sparrow")
	ring2.Add("sparrow", "owl", "crow")

	if ring1.Signature() != ring2.Signature() {
		t.Errorf("Signatures must be identical")
	}

	ring1 = ringhash.New(4, nil)
	ring2 = ringhash.New(5, nil)

	ring1.Add("owl", "crow", "sparrow")
	ring2.Add("owl", "crow", "sparrow")

	if ring1.Signature() == ring2.Signature() {
		t.Errorf("Signatures must be different - different count of replicas")
	}

	ring1 = ringhash.New(4, nil)
	ring2 = ringhash.New(4, nil)

	ring1.Add("owl", "crow", "sparrow")
	ring2.Add("owl", "crow", "sparrow", "crane")

	if ring1.Signature() == ring2.Signature() {
		t.Errorf("Signatures must be different - different keys")
	}

	fnvHashfunc := func(data []byte) uint32 {
		hash := fnv.New32a()
		hash.Write(data)
		return hash.Sum32()
	}

	ring1 = ringhash.New(4, nil)
	ring2 = ringhash.New(4, fnvHashfunc)

	ring1.Add("owl", "crow", "sparrow")
	ring2.Add("owl", "crow", "sparrow")

	if ring1.Signature() == ring2.Signature() {
		t.Errorf("Signatures must be different - different hash functions")
	}
}

func BenchmarkGet8(b *testing.B)   { benchmarkGet(b, 8) }
func BenchmarkGet32(b *testing.B)  { benchmarkGet(b, 32) }
func BenchmarkGet128(b *testing.B) { benchmarkGet(b, 128) }
func BenchmarkGet512(b *testing.B) { benchmarkGet(b, 512) }

func benchmarkGet(b *testing.B, keycount int) {

	ring := ringhash.New(53, nil)

	var ids []string
	for i := 0; i < keycount; i++ {
		ids = append(ids, fmt.Sprintf("id=%d", i))
	}

	ring.Add(ids...)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ring.Get(ids[i&(keycount-1)])
	}
}
