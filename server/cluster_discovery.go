// Package main.
// Cluster membership discovery abstraction.
//
// Two backends are provided:
//   - staticDiscovery: emits a single snapshot from the config file. Used when
//     `cluster_config.discovery.mode` is "static" (the default) or the discovery
//     block is absent. Preserves legacy behavior bit-for-bit.
//   - kubernetesDiscovery: watches EndpointSlices of a headless Service via
//     k8s.io/client-go. Used when `cluster_config.discovery.mode` is
//     "kubernetes". Requires a StatefulSet deployment. Added in Phase C.
//
// The Discovery interface decouples clusterInit from the source of truth for
// peer membership so the runtime reconciler can feed ring updates from either
// a static list or a live Kubernetes informer.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type NodeEndpoint struct {
	Name string
	Addr string
}

type Discovery interface {
	Snapshot() []NodeEndpoint
	Subscribe() <-chan []NodeEndpoint
	Stop()
}

// discoveryConfig selects and configures the discovery backend.
//
// A missing block in tinode.conf (Discovery == nil in the parsed config)
// defaults to mode "static" with the legacy `nodes` list. Existing deployments
// without a `discovery` block behave exactly as before this refactor.
type discoveryConfig struct {
	Mode       string               `json:"mode"`
	Kubernetes *kubeDiscoveryConfig `json:"kubernetes,omitempty"`
}

type kubeDiscoveryConfig struct {
	Namespace     string `json:"namespace"`
	Service       string `json:"service"`
	PortName      string `json:"port_name"`
	LabelSelector string `json:"label_selector"`
}

// staticDiscovery emits a single snapshot built from the static config and
// never changes it. Subscribe() returns a pre-closed channel so a receiver
// that ranges over it exits immediately.
type staticDiscovery struct {
	endpoints []NodeEndpoint
	closed    chan []NodeEndpoint
}

// newStaticDiscovery builds a staticDiscovery from the legacy cluster_config.nodes list.
func newStaticDiscovery(nodes []clusterNodeConfig) *staticDiscovery {
	ch := make(chan []NodeEndpoint)
	close(ch)
	eps := make([]NodeEndpoint, 0, len(nodes))
	for _, n := range nodes {
		eps = append(eps, NodeEndpoint{Name: n.Name, Addr: n.Addr})
	}
	return &staticDiscovery{endpoints: eps, closed: ch}
}

func (s *staticDiscovery) Snapshot() []NodeEndpoint {
	return s.endpoints
}

func (s *staticDiscovery) Subscribe() <-chan []NodeEndpoint {
	return s.closed
}

func (s *staticDiscovery) Stop() {}

// -----------------------------------------------------------------------------
// kubernetesDiscovery: EndpointSlice-watching backend.
// -----------------------------------------------------------------------------

// kubeNamespaceFile is the path inside the pod from which the pod's own
// namespace can be read if kubeDiscoveryConfig.Namespace is left empty.
const kubeNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// kubeDiscoveryCacheResyncInterval is the full-list resync period for the
// shared informer factory. 5 minutes matches upstream client-go defaults.
const kubeDiscoveryCacheResyncInterval = 5 * time.Minute

// kubeDiscoveryDebounceInterval is the trailing-edge coalesce window for
// rapid watch events.
const kubeDiscoveryDebounceInterval = 250 * time.Millisecond

// kubeDiscoveryCacheSyncTimeout bounds how long newKubernetesDiscovery waits
// for the informer's initial LIST to populate the cache.
const kubeDiscoveryCacheSyncTimeout = 10 * time.Second

// serviceNameLabel is the Kubernetes-defined label every EndpointSlice carries
// pointing back at its owning Service. Used both in the informer's label
// selector (upstream-side filter) and in a defensive in-code filter because
// the fake clientset used in tests ignores WithTweakListOptions.
const serviceNameLabel = "kubernetes.io/service-name"

type kubernetesDiscovery struct {
	client  kubernetes.Interface
	factory informers.SharedInformerFactory
	cfg     kubeDiscoveryConfig

	mu            sync.Mutex
	lastSnapshot  []NodeEndpoint
	snapshotReady bool
	debounceTimer *time.Timer

	// subscriber is buffered size 1 with latest-wins semantics: if a reader
	// has not drained the previous snapshot when a new one is published, the
	// old value is discarded and replaced with the new one. This keeps the
	// reconciler always operating on the freshest view without blocking the
	// informer loop.
	subscriber chan []NodeEndpoint
	stopCh     chan struct{}
	stopOnce   sync.Once

	// disableDebounce, when true, bypasses the 250 ms coalesce window and
	// publishes synchronously on every event. Used by tests to make timing
	// deterministic; not exposed via tinode.conf.
	disableDebounce bool
}

// kubeDiscoveryOption configures a kubernetesDiscovery at construction.
type kubeDiscoveryOption func(*kubernetesDiscovery)

// withDisabledDebounce bypasses the trailing-edge coalesce window. For tests.
func withDisabledDebounce() kubeDiscoveryOption {
	return func(d *kubernetesDiscovery) { d.disableDebounce = true }
}

// newKubernetesDiscovery builds the discovery from a config, dialling the
// API server using in-cluster credentials.
func newKubernetesDiscovery(cfg kubeDiscoveryConfig) (*kubernetesDiscovery, error) {
	if cfg.Namespace == "" {
		ns, err := readPodNamespace()
		if err != nil {
			return nil, fmt.Errorf("kubernetes discovery: namespace autodetect failed: %w", err)
		}
		cfg.Namespace = ns
	}

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Out-of-cluster fallback: rely on $KUBECONFIG for local dev.
		if kc := os.Getenv("KUBECONFIG"); kc != "" {
			restCfg, err = clientcmd.BuildConfigFromFlags("", kc)
		}
		if err != nil {
			return nil, fmt.Errorf("kubernetes discovery: no usable kubeconfig: %w", err)
		}
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes discovery: client construction failed: %w", err)
	}
	return newKubernetesDiscoveryWithClient(client, cfg)
}

func readPodNamespace() (string, error) {
	data, err := os.ReadFile(kubeNamespaceFile)
	if err != nil {
		return "", err
	}
	ns := string(data)
	if ns == "" {
		return "", errors.New("namespace file was empty")
	}
	return ns, nil
}

// newKubernetesDiscoveryWithClient constructs the discovery around a caller-
// provided Kubernetes client. Tests pass a `fake.NewSimpleClientset(...)`;
// production code goes through newKubernetesDiscovery which builds a real
// client from the pod's ServiceAccount.
func newKubernetesDiscoveryWithClient(client kubernetes.Interface, cfg kubeDiscoveryConfig, opts ...kubeDiscoveryOption) (*kubernetesDiscovery, error) {
	if cfg.Service == "" {
		return nil, errors.New("kubernetes discovery: service is required")
	}
	if cfg.PortName == "" {
		return nil, errors.New("kubernetes discovery: port_name is required")
	}
	if cfg.Namespace == "" {
		return nil, errors.New("kubernetes discovery: namespace is required")
	}

	d := &kubernetesDiscovery{
		client:     client,
		cfg:        cfg,
		subscriber: make(chan []NodeEndpoint, 1),
		stopCh:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(d)
	}

	// Build the informer factory scoped to the configured namespace and
	// filtered to EndpointSlices owned by the target Service via its
	// well-known `kubernetes.io/service-name` label. The label selector is
	// applied on the API server in production; the fake clientset ignores
	// WithTweakListOptions entirely, so our reconcile() re-applies the label
	// filter defensively. Tests that pass here will pass in prod (the
	// production behavior is a strict subset of the unfiltered fake flow).
	tweakOptions := func(lo *metav1.ListOptions) {
		selector := serviceNameLabel + "=" + cfg.Service
		if cfg.LabelSelector != "" {
			selector += "," + cfg.LabelSelector
		}
		lo.LabelSelector = selector
	}
	d.factory = informers.NewSharedInformerFactoryWithOptions(
		client,
		kubeDiscoveryCacheResyncInterval,
		informers.WithNamespace(cfg.Namespace),
		informers.WithTweakListOptions(tweakOptions),
	)

	informer := d.factory.Discovery().V1().EndpointSlices().Informer()
	// TODO use informer.AddEventHandlerWithOptions for logging support
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ any) { d.refreshEndpointsWithDebouncer() },
		UpdateFunc: func(_, _ any) { d.refreshEndpointsWithDebouncer() },
		DeleteFunc: func(_ any) { d.refreshEndpointsWithDebouncer() },
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes discovery: register event handler: %w", err)
	}

	d.factory.Start(d.stopCh)

	syncCtx, cancel := context.WithTimeout(context.Background(), kubeDiscoveryCacheSyncTimeout)
	defer cancel()
	syncedResourcesByType := d.factory.WaitForCacheSync(syncCtx.Done())
	epSlicesType := reflect.TypeOf(&discoveryv1.EndpointSlice{})
	if !syncedResourcesByType[epSlicesType] {
		// Clean up the goroutines we may have started before returning.
		d.Stop()
		return nil, errors.New("kubernetes discovery: informer cache failed to sync within " + kubeDiscoveryCacheSyncTimeout.String())
	}

	// Publish an initial snapshot so Subscribe() consumers always see at
	// least one value even if the cluster is currently empty.
	d.refreshEndpoints()

	return d, nil
}

// Snapshot returns the most recently published membership view. Safe to call
// before the first event, in which case it returns whatever reconcile emitted
// during construction (possibly an empty slice).
func (d *kubernetesDiscovery) Snapshot() []NodeEndpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]NodeEndpoint, len(d.lastSnapshot))
	copy(out, d.lastSnapshot)
	return out
}

// Subscribe returns the channel on which new snapshots are published. The
// channel is buffered size 1 with latest-wins: readers that fall behind see
// only the newest view.
func (d *kubernetesDiscovery) Subscribe() <-chan []NodeEndpoint {
	return d.subscriber
}

// Stop tears down the informer factory and closes the subscribe channel.
// Idempotent; safe to call multiple times.
func (d *kubernetesDiscovery) Stop() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		if d.debounceTimer != nil {
			d.debounceTimer.Stop()
			d.debounceTimer = nil
		}
		d.mu.Unlock()
		close(d.stopCh)
	})
}

// scheduleReconcile arms a trailing-edge debouncer. Rapid watch events
// collapse into a single reconcile when the debounce interval expires. With
// disableDebounce set, reconcile runs synchronously.
func (d *kubernetesDiscovery) refreshEndpointsWithDebouncer() {
	if d.disableDebounce {
		d.refreshEndpoints()
		return
	}
	d.mu.Lock()
	if d.debounceTimer != nil {
		d.debounceTimer.Stop()
	}
	d.debounceTimer = time.AfterFunc(kubeDiscoveryDebounceInterval, d.refreshEndpoints)
	d.mu.Unlock()
}

func (d *kubernetesDiscovery) refreshEndpoints() {
	snap := d.buildSnapshot()

	d.mu.Lock()
	changed := !d.snapshotReady || !nodeEndpointsEqual(snap, d.lastSnapshot)
	d.lastSnapshot = snap
	d.snapshotReady = true
	d.mu.Unlock()

	if !changed {
		return
	}
	// Non-blocking publish with latest-wins semantics.
	// if the previous snapshot is not consumed, drop it
	// this is done to avoid blocking the informer loop
	select {
	case <-d.subscriber:
		// consume the previous snapshot
	default:
		// no previous snapshot to consume
	}
	select {
	case d.subscriber <- snap:
	default:
		// Buffer re-filled between drain and send; drop rather than block.
		// A subsequent reconcile will re-publish.
	}
}

// buildSnapshot assembles the current membership from the informer cache.
func (d *kubernetesDiscovery) buildSnapshot() []NodeEndpoint {
	slices, err := d.factory.Discovery().V1().EndpointSlices().Lister().EndpointSlices(d.cfg.Namespace).List(labels.Everything())
	if err != nil {
		// listing from the cache should never error, if errors, ignore and continue
		return nil
	}

	out := make([]NodeEndpoint, 0, len(slices))
	for _, slice := range slices {
		if slice.Labels[serviceNameLabel] != d.cfg.Service {
			continue
		}

		port := portFromSlice(slice, d.cfg.PortName)
		if port == 0 {
			continue
		}

		for _, ep := range slice.Endpoints {
			if ep.Conditions.Ready != nil && !*ep.Conditions.Ready {
				continue
			}

			if ep.TargetRef == nil || ep.TargetRef.Name == "" {
				// No pod ref (could be a Node or external endpoint) — skip.
				continue
			}
			if len(ep.Addresses) == 0 {
				continue
			}

			out = append(out, NodeEndpoint{
				Name: ep.TargetRef.Name,
				Addr: net.JoinHostPort(ep.Addresses[0], strconv.Itoa(int(port))),
			})
		}
	}
	// Sort by name for deterministic snapshot comparison.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// portFromSlice returns the port value for the named port on the slice, or 0
// if no such port exists.
func portFromSlice(slice *discoveryv1.EndpointSlice, portName string) int32 {
	for _, p := range slice.Ports {
		if p.Name != nil && *p.Name == portName && p.Port != nil {
			return *p.Port
		}
	}
	return 0
}

// nodeEndpointsEqual compares two snapshot slices assumed to be sorted by Name.
func nodeEndpointsEqual(a, b []NodeEndpoint) bool {
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
