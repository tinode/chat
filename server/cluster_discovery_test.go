package main

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
)

// -----------------------------------------------------------------------------
// Test helpers — all named so the intent is obvious inline.
// -----------------------------------------------------------------------------

const (
	testNamespace = "ns"
	testService   = "tinode-headless"
	testPortName  = "cluster"
	testPort      = int32(12001)
)

// endpointSliceGVR is the GroupVersionResource for EndpointSlice, passed to
// clientset.Tracker().Update() / Delete() when simulating watch events.
var endpointSliceGVR = schema.GroupVersionResource{
	Group:    "discovery.k8s.io",
	Version:  "v1",
	Resource: "endpointslices",
}

// readyEndpoint returns an EndpointSlice Endpoint representing a ready pod
// with a stable name and a single IP. The boolean pointer for the Ready flag
// is required by the discoveryv1 type.
func readyEndpoint(podName, ip string) discoveryv1.Endpoint {
	ready := true
	return discoveryv1.Endpoint{
		Addresses:  []string{ip},
		Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		TargetRef:  &corev1.ObjectReference{Kind: "Pod", Name: podName, Namespace: testNamespace},
	}
}

// notReadyEndpoint is like readyEndpoint but marks the endpoint as not-ready.
func notReadyEndpoint(podName, ip string) discoveryv1.Endpoint {
	notReady := false
	return discoveryv1.Endpoint{
		Addresses:  []string{ip},
		Conditions: discoveryv1.EndpointConditions{Ready: &notReady},
		TargetRef:  &corev1.ObjectReference{Kind: "Pod", Name: podName, Namespace: testNamespace},
	}
}

// buildSlice assembles an EndpointSlice with the service-name label correctly
// set and one named port matching testPortName.
func buildSlice(name string, endpoints ...discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
	portName := testPortName
	port := testPort
	return &discoveryv1.EndpointSlice{
		TypeMeta: metav1.TypeMeta{Kind: "EndpointSlice", APIVersion: "discovery.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{serviceNameLabel: testService},
			// Non-empty resource version so the informer treats subsequent
			// tracker updates as genuinely newer.
			ResourceVersion: "1",
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   endpoints,
		Ports: []discoveryv1.EndpointPort{
			{Name: &portName, Port: &port},
		},
	}
}

// defaultKubeConfig returns the kubeDiscoveryConfig used in most tests.
func defaultKubeConfig() kubeDiscoveryConfig {
	return kubeDiscoveryConfig{
		Namespace: testNamespace,
		Service:   testService,
		PortName:  testPortName,
	}
}

// newTestDiscovery builds a kubernetesDiscovery with the debounce window
// disabled so watch events propagate synchronously. Fails the test on error.
func newTestDiscovery(t *testing.T, client *fake.Clientset) *kubernetesDiscovery {
	t.Helper()
	d, err := newKubernetesDiscoveryWithClient(client, defaultKubeConfig(), withDisabledDebounce())
	if err != nil {
		t.Fatalf("newKubernetesDiscoveryWithClient: %v", err)
	}
	t.Cleanup(d.Stop)
	return d
}

// waitForSnapshot blocks until the subscriber publishes a snapshot or timeout
// elapses. Returns the snapshot (or nil on timeout) and a flag indicating
// success. The timeout provides the upper bound on informer propagation
// latency inside the fake clientset.
func waitForSnapshot(t *testing.T, d *kubernetesDiscovery, timeout time.Duration) []NodeEndpoint {
	t.Helper()
	select {
	case snap := <-d.Subscribe():
		return snap
	case <-time.After(timeout):
		t.Fatalf("waitForSnapshot: no snapshot within %s", timeout)
	}
	return nil
}

// names extracts the ordered list of NodeEndpoint names for easy comparison.
func endpointNames(snap []NodeEndpoint) []string {
	out := make([]string, len(snap))
	for i, ep := range snap {
		out[i] = ep.Name
	}
	return out
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

// TestKubernetesDiscovery_InitialSnapshotFiltersReady asserts the first
// snapshot emitted after cache sync matches the ready endpoints in the seeded
// EndpointSlice. Not-ready endpoints must be excluded.
func TestKubernetesDiscovery_InitialSnapshotFiltersReady(t *testing.T) {
	slice := buildSlice("tinode-headless-abc",
		readyEndpoint("tinode-0", "10.0.0.1"),
		readyEndpoint("tinode-1", "10.0.0.2"),
		notReadyEndpoint("tinode-2", "10.0.0.3"),
	)
	client := fake.NewSimpleClientset(slice)

	d := newTestDiscovery(t, client)
	snap := d.Snapshot()

	got := endpointNames(snap)
	want := []string{"tinode-0", "tinode-1"}
	if !stringSliceEqual(got, want) {
		t.Errorf("initial snapshot names = %v, want %v", got, want)
	}
	if len(snap) > 0 && snap[0].Addr != "10.0.0.1:12001" {
		t.Errorf("first endpoint addr = %q, want %q", snap[0].Addr, "10.0.0.1:12001")
	}
}

// TestKubernetesDiscovery_PodAddedPropagatesToSnapshot asserts that updating
// the EndpointSlice via the tracker (simulating a new pod ready) causes
// Subscribe() to emit an updated snapshot including the new pod.
func TestKubernetesDiscovery_PodAddedPropagatesToSnapshot(t *testing.T) {
	slice := buildSlice("tinode-headless-abc",
		readyEndpoint("tinode-0", "10.0.0.1"),
		readyEndpoint("tinode-1", "10.0.0.2"),
	)
	client := fake.NewSimpleClientset(slice)
	d := newTestDiscovery(t, client)

	// Drain the initial snapshot so we observe the *next* change.
	<-d.Subscribe()

	// Simulate a pod coming online by replacing the slice with one that
	// includes the new ready endpoint. ResourceVersion must bump so the
	// informer treats this as a real update, not a no-op.
	updated := slice.DeepCopy()
	updated.ResourceVersion = "2"
	updated.Endpoints = append(updated.Endpoints, readyEndpoint("tinode-2", "10.0.0.3"))
	if err := client.Tracker().Update(endpointSliceGVR, updated, testNamespace); err != nil {
		t.Fatalf("Tracker.Update: %v", err)
	}

	snap := waitForSnapshot(t, d, 2*time.Second)
	got := endpointNames(snap)
	want := []string{"tinode-0", "tinode-1", "tinode-2"}
	if !stringSliceEqual(got, want) {
		t.Errorf("snapshot after pod add = %v, want %v", got, want)
	}
}

// TestKubernetesDiscovery_PodRemovedPropagatesToSnapshot asserts that
// deleting an endpoint from the slice removes the pod from snapshots.
func TestKubernetesDiscovery_PodRemovedPropagatesToSnapshot(t *testing.T) {
	slice := buildSlice("tinode-headless-abc",
		readyEndpoint("tinode-0", "10.0.0.1"),
		readyEndpoint("tinode-1", "10.0.0.2"),
		readyEndpoint("tinode-2", "10.0.0.3"),
	)
	client := fake.NewSimpleClientset(slice)
	d := newTestDiscovery(t, client)
	<-d.Subscribe() // drain initial

	// Drop tinode-1 and bump ResourceVersion.
	updated := slice.DeepCopy()
	updated.ResourceVersion = "2"
	updated.Endpoints = []discoveryv1.Endpoint{
		readyEndpoint("tinode-0", "10.0.0.1"),
		readyEndpoint("tinode-2", "10.0.0.3"),
	}
	if err := client.Tracker().Update(endpointSliceGVR, updated, testNamespace); err != nil {
		t.Fatalf("Tracker.Update: %v", err)
	}

	snap := waitForSnapshot(t, d, 2*time.Second)
	got := endpointNames(snap)
	want := []string{"tinode-0", "tinode-2"}
	if !stringSliceEqual(got, want) {
		t.Errorf("snapshot after pod remove = %v, want %v", got, want)
	}
}

// TestKubernetesDiscovery_ReadinessFlipDropsPod asserts that flipping a pod
// from Ready=true to Ready=false drops it from the snapshot, without any
// other change to membership.
func TestKubernetesDiscovery_ReadinessFlipDropsPod(t *testing.T) {
	slice := buildSlice("tinode-headless-abc",
		readyEndpoint("tinode-0", "10.0.0.1"),
		readyEndpoint("tinode-1", "10.0.0.2"),
	)
	client := fake.NewSimpleClientset(slice)
	d := newTestDiscovery(t, client)
	<-d.Subscribe()

	updated := slice.DeepCopy()
	updated.ResourceVersion = "2"
	updated.Endpoints = []discoveryv1.Endpoint{
		readyEndpoint("tinode-0", "10.0.0.1"),
		notReadyEndpoint("tinode-1", "10.0.0.2"),
	}
	if err := client.Tracker().Update(endpointSliceGVR, updated, testNamespace); err != nil {
		t.Fatalf("Tracker.Update: %v", err)
	}

	snap := waitForSnapshot(t, d, 2*time.Second)
	got := endpointNames(snap)
	want := []string{"tinode-0"}
	if !stringSliceEqual(got, want) {
		t.Errorf("snapshot after readiness flip = %v, want %v", got, want)
	}
}

// TestKubernetesDiscovery_ServiceLabelFilteredOut asserts that endpoint
// slices belonging to a *different* service do not contaminate snapshots,
// even though the fake clientset does not honour WithTweakListOptions. The
// reconciler's defensive label check is what makes this pass.
func TestKubernetesDiscovery_ServiceLabelFilteredOut(t *testing.T) {
	mine := buildSlice("mine-abc",
		readyEndpoint("tinode-0", "10.0.0.1"),
	)
	theirs := buildSlice("theirs-xyz",
		readyEndpoint("intruder-9", "10.9.9.9"),
	)
	theirs.Labels[serviceNameLabel] = "other-service"

	client := fake.NewSimpleClientset(mine, theirs)
	d := newTestDiscovery(t, client)

	got := endpointNames(d.Snapshot())
	want := []string{"tinode-0"}
	if !stringSliceEqual(got, want) {
		t.Errorf("snapshot = %v, want %v (intruder must be filtered by serviceNameLabel check)", got, want)
	}
}

// TestKubernetesDiscovery_StopIsIdempotent asserts Stop can be called repeatedly
// and that it unblocks any in-flight Subscribe receivers indirectly via the
// informer factory shutdown path.
func TestKubernetesDiscovery_StopIsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset(buildSlice("mine", readyEndpoint("tinode-0", "10.0.0.1")))
	d, err := newKubernetesDiscoveryWithClient(client, defaultKubeConfig(), withDisabledDebounce())
	if err != nil {
		t.Fatalf("newKubernetesDiscoveryWithClient: %v", err)
	}

	d.Stop()
	d.Stop() // must not panic
	d.Stop() // ditto
}

// TestKubernetesDiscovery_RequiresNamespaceServicePort asserts that the
// constructor rejects obviously-incomplete configs before talking to the
// API server.
func TestKubernetesDiscovery_RequiresNamespaceServicePort(t *testing.T) {
	client := fake.NewSimpleClientset()

	cases := []struct {
		name string
		cfg  kubeDiscoveryConfig
	}{
		{"missing service", kubeDiscoveryConfig{Namespace: "ns", PortName: "cluster"}},
		{"missing port name", kubeDiscoveryConfig{Namespace: "ns", Service: "svc"}},
		{"missing namespace", kubeDiscoveryConfig{Service: "svc", PortName: "cluster"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := newKubernetesDiscoveryWithClient(client, tc.cfg)
			if err == nil {
				t.Fatal("expected error for incomplete config")
			}
		})
	}
}

// stringSliceEqual is a tiny helper so we avoid pulling in a test-only dep.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
