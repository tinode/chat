# Tinode on Kubernetes

Reference manifests for running Tinode as a StatefulSet with
Kubernetes-native peer discovery. The cluster's member list is sourced from
`EndpointSlice` events on the headless Service rather than a static
configuration file, and leader election / heartbeats are bypassed entirely.

See `K8S_SERVICE_DISCOVERY.md` at the repository root for the design
background and the list of currently-unresolved operational challenges.

## Files

| File | Purpose |
|---|---|
| `00-rbac.yaml` | ServiceAccount + Role + RoleBinding. Grants the pod `get/list/watch` on `endpointslices.discovery.k8s.io` in its own namespace. |
| `10-service.yaml` | Headless Service (`clusterIP: None`) exposing ports 6060 (WS), 16060 (gRPC), and 12001 (cluster RPC). |
| `20-configmap.yaml` | Tinode `tinode.conf` with `cluster_config.discovery.mode = kubernetes`. |
| `30-statefulset.yaml` | StatefulSet with 3 replicas, `podManagementPolicy: OrderedReady`, downward-API `POD_NAME`, and an entrypoint wrapper that derives `TINODE_WORKER_ID` from the pod ordinal. |

## Quick start

Change the namespace stanzas if you deploy anywhere other than `default`,
then:

```bash
kubectl apply -f docker/k8s/
kubectl rollout status statefulset/tinode
kubectl exec tinode-0 -- curl -s localhost:6060/stats | jq .TotalClusterNodes
# 3
```

## Constraints

**StatefulSet-only.** The manifests assume StatefulSet pod naming
(`<name>-<ordinal>`). Attempting to run this image under a Deployment will
fail at startup with a fatal error because random ReplicaSet suffixes do not
yield a stable worker ID. This is by design — see `§6.11` of the design
doc.

**Replica cap: 1023.** The snowflake ID generator uses a 10-bit worker
field, so the ordinal+1 derivation caps at 1024 pods. `clusterInit` will
fatally abort if asked to produce a worker ID ≥ 1024. If you genuinely need
more replicas, shard into multiple clusters with different DBs.

**Same database.** All pods must connect to the same Tinode DB. Running two
independent StatefulSets against the same DB will cause worker-ID collisions
(both would assign ordinal 0 → worker ID 1) and silent data corruption.

## Scaling

```bash
kubectl scale statefulset/tinode --replicas=5   # grow
kubectl scale statefulset/tinode --replicas=2   # shrink
```

New pods join the ring within ~1s of passing their readiness probe. During
scale-down the departing pod's `preStop` hook gives peers ~5s of propagation
time before SIGTERM drains in-flight proxy requests.

## Rolling updates

```bash
kubectl set image statefulset/tinode tinode=<new-image>
```

OrderedReady ensures one pod at a time restarts. Clients connected through
the restarting pod drop their WebSocket, reconnect via the Service LB, and
land on a surviving pod. Topics whose master was on the restarting pod
re-home to a new master as the ring reconverges; messages published during
the transient are persisted to the DB and replayed when clients resubscribe.

## Observability

Prometheus metrics exposed on the HTTP port:

| Metric | Meaning in K8s mode |
|---|---|
| `TotalClusterNodes` | Current count of pods in the ring (including self). Updated on every discovery snapshot. |
| `LiveClusterNodes` | Ready peers this pod is successfully RPC-connected to. |
| `ClusterLeader` | Pinned to 0. Leader election is disabled in K8s mode. Alert rules based on this gauge should be removed. |
| `ClusterDiscoveryMode` | `0` = static, `1` = kubernetes. |

## Worker-ID escape hatch

If the ordinal-from-pod-name derivation does not suit your deployment, set
`TINODE_WORKER_ID` explicitly via the downward API or a fixed env var:

```yaml
- name: TINODE_WORKER_ID
  value: "17"
```

The explicit value must be an integer in `1..1023`. `clusterInit` will
`log.Fatal` otherwise. Whoever sets this env var owns the uniqueness
invariant — the server does not cross-check against other pods.

## Known limitations

See `K8S_SERVICE_DISCOVERY.md §6` for the full list. Highlights:

- **Transient ring disagreement during rollouts** (<1s). Messages are
  persisted to the DB; clients backfill on resubscribe.
- **No safeguard against post-bootstrap empty snapshots.** If the API
  server partitions the informer and it returns an empty list, the ring
  will shrink to zero.
- **No CI integration test against a real K8s cluster yet.** Unit tests
  use `kubernetes/fake.NewSimpleClientset`; reconnect semantics and
  in-cluster auth are exercised only by manual `kind` runs.
