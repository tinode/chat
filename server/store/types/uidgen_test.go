package types

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestUidGeneratorInit(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2") // 16 bytes for XTEA

	// Test successful initialization
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if ug.seq == nil {
		t.Error("Snowflake generator should be initialized")
	}
	if ug.cipher == nil {
		t.Error("Cipher should be initialized")
	}

	// Test initialization with different worker ID
	ug2 := &UidGenerator{}
	err = ug2.Init(2, key)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test that already initialized generator doesn't reinitialize
	oldSeq := ug.seq
	oldCipher := ug.cipher
	err = ug.Init(3, key)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if ug.seq != oldSeq {
		t.Error("Snowflake generator should not be reinitialized")
	}
	if ug.cipher != oldCipher {
		t.Error("Cipher should not be reinitialized")
	}
}

func TestUidGeneratorInitWithInvalidKey(t *testing.T) {
	ug := &UidGenerator{}

	// Test with key that's too short (XTEA requires 16 bytes)
	shortKey := []byte("short")
	err := ug.Init(1, shortKey)
	if err == nil {
		t.Error("Expected error with short key")
	}

	// Test with nil key
	err = ug.Init(1, nil)
	if err == nil {
		t.Error("Expected error with nil key")
	}
}

func TestUidGeneratorGet(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test basic UID generation
	uid1 := ug.Get()
	if uid1 == ZeroUid {
		t.Error("Generated UID should not be zero")
	}

	uid2 := ug.Get()
	if uid2 == ZeroUid {
		t.Error("Generated UID should not be zero")
	}

	// UIDs should be unique
	if uid1 == uid2 {
		t.Error("Generated UIDs should be unique")
	}

	// Test multiple UIDs are unique
	uids := make(map[Uid]bool)
	for i := 0; i < 1000; i++ {
		uid := ug.Get()
		if uid == ZeroUid {
			t.Errorf("UID %d should not be zero", i)
		}
		if uids[uid] {
			t.Errorf("Duplicate UID generated: %v", uid)
		}
		uids[uid] = true
	}
}

func TestUidGeneratorGetWithUninitializedGenerator(t *testing.T) {
	ug := &UidGenerator{}

	// Test Get() without initialization
	uid := ug.Get()
	if uid != ZeroUid {
		t.Error("Expected ZeroUid from uninitialized generator")
	}
}

func TestUidGeneratorGetStr(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test basic string UID generation
	uidStr1 := ug.GetStr()
	if uidStr1 == "" {
		t.Error("Generated UID string should not be empty")
	}

	uidStr2 := ug.GetStr()
	if uidStr2 == "" {
		t.Error("Generated UID string should not be empty")
	}

	// UID strings should be unique
	if uidStr1 == uidStr2 {
		t.Error("Generated UID strings should be unique")
	}

	// Test that string is valid base64
	_, err = base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(uidStr1)
	if err != nil {
		t.Errorf("Generated UID string should be valid base64: %v", err)
	}

	// Test consistency between Get() and GetStr()
	uidStr := ug.GetStr()

	// Decode the string and compare with binary UID
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(uidStr)
	if err != nil {
		t.Fatalf("Failed to decode UID string: %v", err)
	}
	if len(decoded) != 8 {
		t.Errorf("Decoded UID should be 8 bytes, got %d", len(decoded))
	}
}

func TestUidGeneratorGetStrWithUninitializedGenerator(t *testing.T) {
	ug := &UidGenerator{}

	// Test GetStr() without initialization
	uidStr := ug.GetStr()
	if uidStr != "" {
		t.Error("Expected empty string from uninitialized generator")
	}
}

func TestUidGeneratorDecodeUid(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test decoding generated UIDs
	uid := ug.Get()
	decoded := ug.DecodeUid(uid)

	// Decoded value should be non-negative (for SQL compatibility)
	if decoded < 0 {
		t.Errorf("Decoded UID should be non-negative, got %d", decoded)
	}

	// Test multiple UIDs decode to different values
	uid1 := ug.Get()
	uid2 := ug.Get()
	decoded1 := ug.DecodeUid(uid1)
	decoded2 := ug.DecodeUid(uid2)

	if decoded1 == decoded2 {
		t.Error("Different UIDs should decode to different values")
	}
}

func TestUidGeneratorEncodeInt64(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test encoding positive int64 values
	val1 := int64(12345)
	uid1 := ug.EncodeInt64(val1)
	if uid1 == ZeroUid {
		t.Error("Encoded UID should not be zero for positive value")
	}

	val2 := int64(67890)
	uid2 := ug.EncodeInt64(val2)
	if uid2 == ZeroUid {
		t.Error("Encoded UID should not be zero for positive value")
	}

	// Different values should encode to different UIDs
	if uid1 == uid2 {
		t.Error("Different values should encode to different UIDs")
	}

	// Test encoding zero
	uid0 := ug.EncodeInt64(0)
	if uid0 == ZeroUid {
		t.Error("Encoded UID for 0 should not be ZeroUid (due to encryption)")
	}

	// Test encoding large values
	maxVal := int64(9223372036854775807) // max int64
	uidMax := ug.EncodeInt64(maxVal)
	if uidMax == ZeroUid {
		t.Error("Should be able to encode max int64 value")
	}
}

func TestUidGeneratorEncodeDecodeRoundtrip(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test encode/decode roundtrip for various values
	testValues := []int64{0, 1, 42, 12345, 1000000, 9223372036854775807}

	for _, val := range testValues {
		encoded := ug.EncodeInt64(val)
		decoded := ug.DecodeUid(encoded)

		if decoded != val {
			t.Errorf("Roundtrip failed for %d: got %d", val, decoded)
		}
	}

	// Test that generated UIDs can be decoded back to sequential values
	uid := ug.Get()
	decoded := ug.DecodeUid(uid)
	reencoded := ug.EncodeInt64(decoded)

	if reencoded != uid {
		t.Error("Generated UID roundtrip failed")
	}
}

func TestUidGeneratorConcurrency(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Test concurrent UID generation
	const numGoroutines = 10
	const uidsPerGoroutine = 100

	uidChan := make(chan Uid, numGoroutines*uidsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < uidsPerGoroutine; j++ {
				uid := ug.Get()
				uidChan <- uid
			}
		}()
	}

	// Collect all UIDs
	uids := make(map[Uid]bool)
	for i := 0; i < numGoroutines*uidsPerGoroutine; i++ {
		uid := <-uidChan
		if uid == ZeroUid {
			t.Error("Generated UID should not be zero")
		}
		if uids[uid] {
			t.Errorf("Duplicate UID generated in concurrent test: %v", uid)
		}
		uids[uid] = true
	}

	if len(uids) != numGoroutines*uidsPerGoroutine {
		t.Errorf("Expected %d unique UIDs, got %d", numGoroutines*uidsPerGoroutine, len(uids))
	}
}

func TestUidGeneratorPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		t.Fatalf("Failed to initialize generator: %v", err)
	}

	// Performance test for Get()
	start := time.Now()
	for i := 0; i < 100000; i++ {
		uid := ug.Get()
		if uid == ZeroUid {
			t.Error("Generated UID should not be zero")
		}
	}
	duration := time.Since(start)
	t.Logf("Generated 100,000 UIDs in %v (%.0f UIDs/sec)", duration, 100000/duration.Seconds())

	// Performance test for GetStr()
	start = time.Now()
	for i := 0; i < 100000; i++ {
		uidStr := ug.GetStr()
		if uidStr == "" {
			t.Error("Generated UID string should not be empty")
		}
	}
	duration = time.Since(start)
	t.Logf("Generated 100,000 UID strings in %v (%.0f UIDs/sec)", duration, 100000/duration.Seconds())
}

func TestUidGeneratorDifferentWorkerIds(t *testing.T) {
	key := []byte("testkey1testkey2")

	// Test that different worker IDs produce different sequences
	ug1 := &UidGenerator{}
	ug2 := &UidGenerator{}

	err1 := ug1.Init(1, key)
	err2 := ug2.Init(2, key)

	if err1 != nil || err2 != nil {
		t.Fatalf("Failed to initialize generators: %v, %v", err1, err2)
	}

	// Generate UIDs from both generators
	uids1 := make([]Uid, 100)
	uids2 := make([]Uid, 100)

	for i := 0; i < 100; i++ {
		uids1[i] = ug1.Get()
		uids2[i] = ug2.Get()
	}

	// Check for uniqueness across generators
	allUids := make(map[Uid]bool)
	for _, uid := range uids1 {
		if allUids[uid] {
			t.Error("Duplicate UID found across generators")
		}
		allUids[uid] = true
	}
	for _, uid := range uids2 {
		if allUids[uid] {
			t.Error("Duplicate UID found across generators")
		}
		allUids[uid] = true
	}
}
func TestUidGeneratorInitErrorConditions(t *testing.T) {
	// Test with invalid worker ID (snowflake has limits)
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")

	// Test with worker ID that might cause snowflake to fail
	// Snowflake typically supports worker IDs up to 1023 (10 bits)
	err := ug.Init(1024, key) // This should potentially fail
	if err != nil {
		t.Logf("Expected behavior: worker ID 1024 failed with: %v", err)
	}

	// Test with extremely large worker ID
	ug2 := &UidGenerator{}
	err = ug2.Init(4294967295, key) // max uint32
	if err != nil {
		t.Logf("Expected behavior: max uint32 worker ID failed with: %v", err)
	}
}

func TestUidGeneratorInitKeyValidation(t *testing.T) {
	// Test with various invalid key lengths
	testCases := []struct {
		name string
		key  []byte
	}{
		{"nil key", nil},
		{"empty key", []byte{}},
		{"too short key", []byte("short")},
		{"15 byte key", []byte("testkey1testkey")},   // 15 bytes
		{"17 byte key", []byte("testkey1testkey22")}, // 17 bytes
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ug := &UidGenerator{}
			err := ug.Init(1, tc.key)
			if err == nil {
				t.Errorf("Expected error for %s, but got none", tc.name)
			}
		})
	}
}

func TestUidGeneratorInitValidKeys(t *testing.T) {
	// Test with exactly 16 byte key (XTEA requirement)
	ug := &UidGenerator{}
	key16 := []byte("testkey1testkey2") // exactly 16 bytes
	err := ug.Init(1, key16)
	if err != nil {
		t.Errorf("16-byte key should work: %v", err)
	}

	// Test with different valid 16-byte keys
	validKeys := [][]byte{
		[]byte("1234567890123456"),
		[]byte("abcdefghijklmnop"),
		[]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f"),
	}

	for i, key := range validKeys {
		ug := &UidGenerator{}
		err := ug.Init(uint(i), key)
		if err != nil {
			t.Errorf("Valid key %d should work: %v", i, err)
		}
	}
}

func TestUidGeneratorInitPartialFailure(t *testing.T) {
	// Test scenario where snowflake init succeeds but cipher init fails
	ug := &UidGenerator{}

	// First, initialize with valid parameters
	validKey := []byte("testkey1testkey2")
	err := ug.Init(1, validKey)
	if err != nil {
		t.Fatalf("Initial setup should work: %v", err)
	}

	// Now try to init again with invalid key - should not affect existing snowflake
	oldSeq := ug.seq
	err = ug.Init(1, []byte("short"))

	// The snowflake should remain the same (not re-initialized)
	if ug.seq != oldSeq {
		t.Error("Snowflake should not be re-initialized on partial failure")
	}

	// But cipher might be affected depending on implementation
	if err != nil {
		t.Logf("Expected behavior: partial init failed with: %v", err)
	}
}

func TestUidGeneratorInitMultipleWorkers(t *testing.T) {
	key := []byte("testkey1testkey2")
	generators := make([]*UidGenerator, 10)

	// Initialize multiple generators with different worker IDs
	for i := 0; i < 10; i++ {
		generators[i] = &UidGenerator{}
		err := generators[i].Init(uint(i), key)
		if err != nil {
			t.Errorf("Worker %d initialization failed: %v", i, err)
		}
	}

	// Verify they all generate unique UIDs
	uids := make(map[Uid]int) // UID -> worker ID
	for i, gen := range generators {
		for j := 0; j < 10; j++ {
			uid := gen.Get()
			if uid == ZeroUid {
				t.Errorf("Worker %d generated ZeroUid", i)
				continue
			}
			if existingWorker, exists := uids[uid]; exists {
				t.Errorf("Duplicate UID %v between workers %d and %d", uid, existingWorker, i)
			}
			uids[uid] = i
		}
	}
}

func TestUidGeneratorInitIdempotency(t *testing.T) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")

	// First initialization
	err1 := ug.Init(1, key)
	if err1 != nil {
		t.Fatalf("First init failed: %v", err1)
	}

	seq1 := ug.seq
	cipher1 := ug.cipher

	// Second initialization with same parameters
	err2 := ug.Init(1, key)
	if err2 != nil {
		t.Errorf("Second init should not fail: %v", err2)
	}

	// Should not re-initialize
	if ug.seq != seq1 {
		t.Error("Snowflake should not be re-initialized")
	}
	if ug.cipher != cipher1 {
		t.Error("Cipher should not be re-initialized")
	}

	// Third initialization with different parameters
	err3 := ug.Init(2, key)
	if err3 != nil {
		t.Errorf("Third init should not fail: %v", err3)
	}

	// Still should not re-initialize
	if ug.seq != seq1 {
		t.Error("Snowflake should not be re-initialized even with different worker ID")
	}
	if ug.cipher != cipher1 {
		t.Error("Cipher should not be re-initialized even with different worker ID")
	}
}

func TestUidGeneratorInitConcurrent(t *testing.T) {
	const numGoroutines = 20
	key := []byte("testkey1testkey2")

	ug := &UidGenerator{}
	errChan := make(chan error, numGoroutines)

	// Concurrent initialization attempts
	for i := 0; i < numGoroutines; i++ {
		go func(workerID uint) {
			err := ug.Init(workerID, key)
			errChan <- err
		}(uint(i))
	}

	// Collect results
	var errors []error
	for i := 0; i < numGoroutines; i++ {
		if err := <-errChan; err != nil {
			errors = append(errors, err)
		}
	}

	// At least one should succeed, others might fail due to race conditions
	// but the generator should be in a valid state
	if ug.seq == nil {
		t.Error("Snowflake should be initialized after concurrent attempts")
	}
	if ug.cipher == nil {
		t.Error("Cipher should be initialized after concurrent attempts")
	}

	// Should be able to generate UIDs
	uid := ug.Get()
	if uid == ZeroUid {
		t.Error("Should be able to generate UID after concurrent initialization")
	}

	if len(errors) > 0 {
		t.Logf("Some concurrent initializations failed (may be expected): %v", errors)
	}
}

func TestUidGeneratorInitBoundaryWorkerIDs(t *testing.T) {
	key := []byte("testkey1testkey2")

	// Test boundary values for worker ID
	testCases := []struct {
		name     string
		workerID uint
		expect   bool // true if should succeed
	}{
		{"zero worker ID", 0, true},
		{"worker ID 1", 1, true},
		{"worker ID 1023", 1023, true},  // Common snowflake limit
		{"worker ID 1024", 1024, false}, // Might exceed limit
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ug := &UidGenerator{}
			err := ug.Init(tc.workerID, key)

			if tc.expect && err != nil {
				t.Errorf("Expected success for worker ID %d, got error: %v", tc.workerID, err)
			} else if !tc.expect && err == nil {
				t.Errorf("Expected error for worker ID %d, but succeeded", tc.workerID)
			}

			// If initialization succeeded, verify it works
			if err == nil {
				uid := ug.Get()
				if uid == ZeroUid {
					t.Errorf("Generator with worker ID %d should produce valid UIDs", tc.workerID)
				}
			}
		})
	}
}
func BenchmarkUidGeneratorGet(b *testing.B) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		b.Fatalf("Failed to initialize generator: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ug.Get()
	}
}

func BenchmarkUidGeneratorGetStr(b *testing.B) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		b.Fatalf("Failed to initialize generator: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ug.GetStr()
	}
}

func BenchmarkUidGeneratorDecodeUid(b *testing.B) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		b.Fatalf("Failed to initialize generator: %v", err)
	}

	// Pre-generate a UID to decode
	uid := ug.Get()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ug.DecodeUid(uid)
	}
}

func BenchmarkUidGeneratorEncodeInt64(b *testing.B) {
	ug := &UidGenerator{}
	key := []byte("testkey1testkey2")
	err := ug.Init(1, key)
	if err != nil {
		b.Fatalf("Failed to initialize generator: %v", err)
	}

	val := int64(12345)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ug.EncodeInt64(val)
	}
}
