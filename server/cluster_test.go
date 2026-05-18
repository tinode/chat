package main

import (
	"net"
	"net/rpc"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tinode/chat/server/concurrency"
)

// freshCluster returns a minimal Cluster suitable for add/remove helper tests.
// It also installs the instance into globals.cluster so ClusterNode.reconnect()
// (which dereferences globals.cluster for the ping) does not NPE.
// Caller must invoke the returned cleanup func to restore prior globals state.
func freshCluster(t *testing.T, self string) (*Cluster, func()) {
	t.Helper()
	prev := globals.cluster
	c := &Cluster{
		thisNodeName:    self,
		fingerprint:     1,
		nodes:           make(map[string]*ClusterNode),
		proxyEventQueue: concurrency.NewGoRoutinePool(8),
	}
	globals.cluster = c
	return c, func() {
		c.proxyEventQueue.Stop()
		globals.cluster = prev
	}
}

// unroutableAddr is a TCP address that reliably fails to connect within
// clusterNetworkTimeout. 127.0.0.1:1 is reserved and refuses connections fast.
const unroutableAddr = "127.0.0.1:1"

func TestAddClusterNode_PopulatesMap(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	node := c.addClusterNode("peer1", unroutableAddr)
	if node == nil {
		t.Fatal("addClusterNode returned nil")
	}
	if node.name != "peer1" {
		t.Errorf("node.name = %q, want %q", node.name, "peer1")
	}
	if node.address != unroutableAddr {
		t.Errorf("node.address = %q, want %q", node.address, unroutableAddr)
	}

	c.nodesLock.RLock()
	got, ok := c.nodes["peer1"]
	c.nodesLock.RUnlock()
	if !ok {
		t.Fatal("peer1 missing from c.nodes after addClusterNode")
	}
	if got != node {
		t.Error("map entry is not the returned node")
	}

	c.removeClusterNode("peer1")
}

func TestAddClusterNode_IgnoresSelf(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	node := c.addClusterNode("self", unroutableAddr)
	if node != nil {
		t.Error("addClusterNode should return nil when name == thisNodeName")
	}

	c.nodesLock.RLock()
	_, found := c.nodes["self"]
	c.nodesLock.RUnlock()
	if found {
		t.Error("self should not be inserted into c.nodes")
	}
}

func TestTopicOwnerReturnsOwnerAndRemoteFromSameSnapshot(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	c.nodesLock.Lock()
	c.nodes["peer1"] = &ClusterNode{name: "peer1", address: unroutableAddr}
	c.ring = buildRing([]string{"self", "peer1"})
	c.nodesLock.Unlock()

	var topic string
	for i := 0; i < 1000; i++ {
		candidate := "topic-owner-test-" + strconv.Itoa(i)
		c.nodesLock.RLock()
		expectedOwner := c.ring.Get(candidate)
		c.nodesLock.RUnlock()
		if expectedOwner == "peer1" {
			topic = candidate
			break
		}
	}
	if topic == "" {
		t.Fatal("failed to find a topic owned by peer1")
	}

	owner, remote := c.topicOwner(topic)
	if owner != "peer1" {
		t.Errorf("topicOwner owner = %q, want peer1", owner)
	}
	if !remote {
		t.Error("topicOwner remote = false, want true for peer-owned topic")
	}
}

func TestAddClusterNode_IdempotentOnDuplicate(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	first := c.addClusterNode("peer1", unroutableAddr)
	second := c.addClusterNode("peer1", unroutableAddr)
	if first != second {
		t.Error("second addClusterNode for the same name should return the existing node, not a new one")
	}

	c.nodesLock.RLock()
	count := len(c.nodes)
	c.nodesLock.RUnlock()
	if count != 1 {
		t.Errorf("len(c.nodes) = %d after duplicate add, want 1", count)
	}

	c.removeClusterNode("peer1")
}

func TestRemoveClusterNode_DeletesFromMapAndStopsGoroutines(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	node := c.addClusterNode("peer1", unroutableAddr)
	if node == nil {
		t.Fatal("addClusterNode returned nil")
	}

	c.removeClusterNode("peer1")

	c.nodesLock.RLock()
	_, present := c.nodes["peer1"]
	c.nodesLock.RUnlock()
	if present {
		t.Error("peer1 still in c.nodes after removeClusterNode")
	}

	select {
	case <-node.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("removed peer done signal not closed within 500ms")
	}

	node.lock.Lock()
	connected := node.connected
	node.lock.Unlock()
	if connected {
		t.Error("removed peer should be disconnected")
	}

	if err := node.proxyToMasterAsync(&ClusterReq{}); err == nil {
		t.Error("proxyToMasterAsync on stopped peer returned nil error")
	}

	var unused bool
	if call := node.callAsync("Cluster.TopicProxy", &ClusterResp{}, &unused, nil); call.Error == nil {
		t.Error("callAsync on stopped peer returned nil error")
	}
}

func TestRemoveClusterNode_NoopOnUnknownName(t *testing.T) {
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	// Must not panic and must not affect other state.
	c.removeClusterNode("does-not-exist")

	c.nodesLock.RLock()
	count := len(c.nodes)
	c.nodesLock.RUnlock()
	if count != 0 {
		t.Errorf("len(c.nodes) = %d after no-op remove, want 0", count)
	}
}

func TestAsyncRpcLoop_DrainsRpcDoneAfterShutdown(t *testing.T) {
	node := &ClusterNode{
		done:    make(chan bool),
		rpcDone: make(chan *rpc.Call, 1),
	}

	loopExited := make(chan struct{})
	go func() {
		node.asyncRpcLoop()
		close(loopExited)
	}()

	close(node.done)

	firstCall := &rpc.Call{ServiceMethod: "Cluster.TopicProxy"}
	select {
	case node.rpcDone <- firstCall:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("first rpcDone send blocked unexpectedly")
	}

	secondCall := &rpc.Call{ServiceMethod: "Cluster.TopicProxy"}
	select {
	case node.rpcDone <- secondCall:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("asyncRpcLoop did not drain rpcDone after shutdown")
	}

	select {
	case <-loopExited:
		t.Fatal("asyncRpcLoop exited before idle drain timeout elapsed")
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case <-loopExited:
	case <-time.After(2 * time.Second):
		t.Fatal("asyncRpcLoop did not exit after rpcDone went idle")
	}
}

func TestAsyncRpcLoop_ShutdownClearsEndpointState(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	node := &ClusterNode{
		done:         make(chan bool),
		rpcDone:      make(chan *rpc.Call, 1),
		endpoint:     rpc.NewClient(clientConn),
		connected:    true,
		reconnecting: true,
	}

	loopExited := make(chan struct{})
	go func() {
		node.asyncRpcLoop()
		close(loopExited)
	}()

	close(node.done)

	select {
	case <-loopExited:
	case <-time.After(2 * time.Second):
		t.Fatal("asyncRpcLoop did not exit after shutdown")
	}

	node.lock.Lock()
	endpoint := node.endpoint
	connected := node.connected
	reconnecting := node.reconnecting
	node.lock.Unlock()

	if endpoint != nil {
		t.Error("asyncRpcLoop should clear endpoint on shutdown")
	}
	if connected {
		t.Error("asyncRpcLoop should mark node disconnected on shutdown")
	}
	if reconnecting {
		t.Error("asyncRpcLoop should clear reconnecting flag on shutdown")
	}
}

func TestStopClusterNode_DrainsLateRpcDoneCompletions(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	node := &ClusterNode{
		done:      make(chan bool),
		rpcDone:   make(chan *rpc.Call, 1),
		endpoint:  rpc.NewClient(clientConn),
		connected: true,
	}

	loopExited := make(chan struct{})
	go func() {
		node.asyncRpcLoop()
		close(loopExited)
	}()

	stopClusterNode(node)

	node.lock.Lock()
	endpoint := node.endpoint
	connected := node.connected
	node.lock.Unlock()

	if endpoint != nil {
		t.Fatal("stopClusterNode should close and clear endpoint before late completions arrive")
	}
	if connected {
		t.Fatal("stopClusterNode should mark node disconnected before late completions arrive")
	}

	firstLateCall := &rpc.Call{ServiceMethod: "Cluster.TopicProxy"}
	select {
	case node.rpcDone <- firstLateCall:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("first late rpcDone completion blocked after stopClusterNode")
	}

	secondLateCall := &rpc.Call{ServiceMethod: "Cluster.TopicProxy"}
	select {
	case node.rpcDone <- secondLateCall:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("asyncRpcLoop did not keep draining rpcDone after stopClusterNode")
	}

	select {
	case <-loopExited:
		t.Fatal("asyncRpcLoop exited before the retirement drain window elapsed")
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case <-loopExited:
	case <-time.After(2 * time.Second):
		t.Fatal("asyncRpcLoop did not exit after rpcDone went idle following stopClusterNode")
	}
}

// -----------------------------------------------------------------------------
// Phase D: applySnapshot diff semantics.
// -----------------------------------------------------------------------------

// fakeDiscovery implements Discovery with a manually-driven subscriber for
// unit-testing the cluster reconciler without client-go.
type fakeDiscovery struct {
	snapshot []NodeEndpoint
	sub      chan []NodeEndpoint
}

func (f *fakeDiscovery) Snapshot() []NodeEndpoint         { return f.snapshot }
func (f *fakeDiscovery) Subscribe() <-chan []NodeEndpoint { return f.sub }
func (f *fakeDiscovery) Stop()                            {}

// freshClusterWithHub extends freshCluster with a minimal hub so
// applySnapshot's calls into invalidateProxySubs / gcProxySessions don't
// nil-deref on globals.hub or globals.sessionStore.
func freshClusterWithHub(t *testing.T, self string) (*Cluster, func()) {
	t.Helper()
	c, cleanupCluster := freshCluster(t, self)
	prevHub := globals.hub
	globals.hub = &Hub{
		rehash: make(chan bool, 1),
		// Hub.topics is a *sync.Map; zero-value is a typed nil pointer and
		// Range() would panic. Provide a real empty map.
		topics: &sync.Map{},
	}
	return c, func() {
		globals.hub = prevHub
		cleanupCluster()
	}
}

func TestApplySnapshot_AddsAndRemovesPeers(t *testing.T) {
	c, cleanup := freshClusterWithHub(t, "self")
	defer cleanup()

	// Start with two peers via applySnapshot.
	c.applySnapshot([]NodeEndpoint{
		{Name: "self", Addr: "10.0.0.1:12001"},
		{Name: "peer1", Addr: "10.0.0.2:12001"},
		{Name: "peer2", Addr: "10.0.0.3:12001"},
	})

	c.nodesLock.RLock()
	peer1 := c.nodes["peer1"]
	_, has1 := c.nodes["peer1"]
	_, has2 := c.nodes["peer2"]
	_, hasSelf := c.nodes["self"]
	count := len(c.nodes)
	c.nodesLock.RUnlock()
	if !has1 || !has2 {
		t.Errorf("expected peer1 and peer2 in c.nodes, got %d entries", count)
	}
	if hasSelf {
		t.Errorf("self must not be added to c.nodes")
	}

	// Next snapshot drops peer1 and adds peer3.
	c.applySnapshot([]NodeEndpoint{
		{Name: "self", Addr: "10.0.0.1:12001"},
		{Name: "peer2", Addr: "10.0.0.3:12001"},
		{Name: "peer3", Addr: "10.0.0.4:12001"},
	})

	c.nodesLock.RLock()
	_, has1 = c.nodes["peer1"]
	_, has2 = c.nodes["peer2"]
	_, has3 := c.nodes["peer3"]
	count = len(c.nodes)
	c.nodesLock.RUnlock()
	if has1 {
		t.Error("peer1 should have been removed by applySnapshot")
	}
	if !has2 {
		t.Error("peer2 should have survived applySnapshot")
	}
	if !has3 {
		t.Error("peer3 should have been added by applySnapshot")
	}
	if count != 2 {
		t.Errorf("len(c.nodes) = %d after second snapshot, want 2", count)
	}
	select {
	case <-peer1.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("removed peer1 done signal not closed within 500ms")
	}
	if err := peer1.proxyToMasterAsync(&ClusterReq{}); err == nil {
		t.Error("proxyToMasterAsync on stopped peer1 returned nil error")
	}
	var unused bool
	if call := peer1.callAsync("Cluster.TopicProxy", &ClusterResp{}, &unused, nil); call.Error == nil {
		t.Error("callAsync on stopped peer1 returned nil error")
	}
	// Cleanup all remaining peers.
	c.removeClusterNode("peer2")
	c.removeClusterNode("peer3")
}

func TestApplySnapshot_ReaddsOnAddressChange(t *testing.T) {
	c, cleanup := freshClusterWithHub(t, "self")
	defer cleanup()

	c.applySnapshot([]NodeEndpoint{
		{Name: "self", Addr: "10.0.0.1:12001"},
		{Name: "peer1", Addr: "10.0.0.2:12001"},
	})

	c.nodesLock.RLock()
	originalPtr := c.nodes["peer1"]
	originalAddr := originalPtr.address
	c.nodesLock.RUnlock()
	if originalAddr != "10.0.0.2:12001" {
		t.Fatalf("initial peer1 address = %q", originalAddr)
	}

	// Same name, different address — should tear down and re-add.
	c.applySnapshot([]NodeEndpoint{
		{Name: "self", Addr: "10.0.0.1:12001"},
		{Name: "peer1", Addr: "10.0.0.99:12001"},
	})

	c.nodesLock.RLock()
	newPtr := c.nodes["peer1"]
	newAddr := newPtr.address
	c.nodesLock.RUnlock()
	if newPtr == originalPtr {
		t.Error("address change must produce a new ClusterNode pointer")
	}
	if newAddr != "10.0.0.99:12001" {
		t.Errorf("peer1 address = %q, want %q", newAddr, "10.0.0.99:12001")
	}
	select {
	case <-originalPtr.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("replaced peer1 done signal not closed within 500ms")
	}
	if err := originalPtr.proxyToMasterAsync(&ClusterReq{}); err == nil {
		t.Error("proxyToMasterAsync on replaced peer1 returned nil error")
	}
	var unused bool
	if call := originalPtr.callAsync("Cluster.TopicProxy", &ClusterResp{}, &unused, nil); call.Error == nil {
		t.Error("callAsync on replaced peer1 returned nil error")
	}
	c.removeClusterNode("peer1")
}

// -----------------------------------------------------------------------------
// Phase D: worker ID derivation.
// -----------------------------------------------------------------------------

func TestParsePodOrdinal(t *testing.T) {
	cases := []struct {
		name    string
		podName string
		want    int
		ok      bool
	}{
		{"statefulset style", "tinode-0", 0, true},
		{"double digit", "tinode-42", 42, true},
		{"multi-dash base", "my-app-prod-7", 7, true},
		{"zero-padded is fine", "tinode-007", 7, true},
		{"missing dash", "tinode", 0, false},
		{"trailing dash", "tinode-", 0, false},
		{"non-numeric suffix", "tinode-abc", 0, false},
		{"deployment style", "tinode-597f8f99cb-rq6l4", 0, false},
		// Consecutive dashes land on the last dash only, per the regex
		// `^.*-([0-9]+)$`. "tinode--1" parses as ordinal 1 — malformed in
		// practice but accepted by the parser.
		{"empty mid-segment", "tinode--1", 1, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseWorkerID(tc.podName)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ordinal = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPortOnlyFromAddr(t *testing.T) {
	if got := portOnlyFromAddr("10.0.0.1:12001"); got != "12001" {
		t.Errorf("portOnlyFromAddr ipv4 = %q, want 12001", got)
	}
	if got := portOnlyFromAddr("localhost:12001"); got != "12001" {
		t.Errorf("portOnlyFromAddr hostname = %q, want 12001", got)
	}
	if got := portOnlyFromAddr("no-colon"); got != "no-colon" {
		t.Errorf("portOnlyFromAddr no colon = %q, want passthrough", got)
	}
}

func TestKubernetesListenAddrUsesConfiguredSelfPort(t *testing.T) {
	if got := k8sListenAddr(13001); got != ":13001" {
		t.Errorf("k8sListenAddr = %q, want :13001", got)
	}
}

func TestAddRemoveClusterNode_Concurrent(t *testing.T) {
	// Smoke test the RWMutex: many concurrent adds and removes of the same
	// name should converge to an empty map with no data race (run with -race).
	c, cleanup := freshCluster(t, "self")
	defer cleanup()

	const iters = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			c.addClusterNode("peer1", unroutableAddr)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			c.removeClusterNode("peer1")
		}
	}()
	wg.Wait()

	// Final cleanup regardless of which goroutine won the race.
	c.removeClusterNode("peer1")

	c.nodesLock.RLock()
	count := len(c.nodes)
	c.nodesLock.RUnlock()
	if count != 0 {
		t.Errorf("len(c.nodes) = %d after concurrent churn, want 0", count)
	}
}
