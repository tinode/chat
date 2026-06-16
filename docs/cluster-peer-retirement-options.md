# Cluster Peer Retirement Options

## Problem

Kubernetes discovery can remove or replace a peer while other goroutines still
hold a `*ClusterNode` returned by routing. The current retirement path closes
`rpcDone` and `p2mSender` after removing the peer from `Cluster.nodes`.

That is unsafe during live membership churn: a goroutine can keep the old
pointer, then call `proxyToMasterAsync` and send to `p2mSender` after the
channel has been closed. The result is a `send on closed channel` panic during
normal scale-down, readiness loss, or pod IP replacement. In-flight async RPC
callbacks can hit the same class of bug with `rpcDone`.

## Options

### Minimal Retired-State Retirement

Keep runtime peer channels open, mark retired peers as closed/draining, close
the TCP endpoint, and have senders return a routing/connection error when the
peer is no longer active. Goroutines exit through explicit done signals or
context cancellation rather than channel closure.

Pros:
- Smallest change to the current cluster model.
- Avoids panics from stale `*ClusterNode` references.
- Keeps routing behavior easy to reason about during EndpointSlice churn.

Cons:
- Requires careful checks on all send paths.
- Retired peer goroutines need a reliable non-channel-close exit path.

### Refcounted Retirement

Track in-flight users of each `ClusterNode`. Removing a peer marks it retired,
prevents new references, waits for current references to drain, then closes
channels.

Pros:
- Preserves the existing channel-close cleanup pattern.
- Gives deterministic cleanup after the last in-flight operation completes.

Cons:
- More invasive: every routing path must acquire and release references.
- Easy to leak references or deadlock if a path returns early.

### Immutable Membership Snapshot

Publish a single immutable routing snapshot containing the ring and peer
transport references. Each request uses one snapshot for routing and sends.
Retired snapshots remain valid until no request can reference them.

Pros:
- Strong consistency between ring ownership and peer transport state.
- Makes membership swaps explicit and easier to test.

Cons:
- Larger refactor than the immediate review fix.
- Still needs a lifecycle policy for old snapshots and peer connections.

### Uniform Transport Routing

Move closer to a transport-owned resolver/balancer model where local and remote
ownership share one dispatch path, and the application stops managing
per-peer routing state directly.

Pros:
- Reduces application-level membership lifecycle complexity.
- Avoids many special cases around `self` versus remote peers.

Cons:
- Architectural change beyond the current Kubernetes discovery work.
- Adds loopback transport overhead for locally owned work unless optimized.

## Recommended Next Step

Use the minimal retired-state retirement path for the current Kubernetes
discovery implementation. It directly addresses the panic risk with the least
blast radius. Revisit immutable snapshots if future work touches broader
cluster routing or failover behavior.
