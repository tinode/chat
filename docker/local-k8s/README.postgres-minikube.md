# Tinode on Minikube with PostgreSQL

This is a local-testing deployment path for validating Tinode's
Kubernetes-based cluster discovery against the current workspace code. It is
not production guidance.

It differs from the stock `docker/tinode/Dockerfile` in one important way:
the Minikube image is built from the local branch instead of downloading a
released Tinode tarball.

## Prerequisites

- Minikube is installed
- `kubectl` is configured to talk to Minikube
- the sibling webapp directory exists at `../webapp` relative to this repo
- build context directory is the parent that contains both `chat/` and
  `webapp/`

## 1. Start Minikube

```bash
minikube start
```

## 2. Build the local Tinode image

From the parent directory that contains both `chat/` and `webapp/`:

```bash
minikube image build \
  -f chat/Dockerfile.minikube-postgres \
  -t tinode/tinode:k8s-postgres-local \
  .
```

The image contains:

- the current branch's `tinode` server built with `-tags postgres`
- `init-db` built from `tinode-db`
- the bundled webapp assets under `/opt/tinode/static`

## 3. Apply manifests

Apply in this order:

```bash
kubectl apply -f chat/docker/local-k8s/15-postgres.yaml
kubectl rollout status deployment/postgres

kubectl apply -f chat/docker/k8s/00-rbac.yaml
kubectl apply -f chat/docker/k8s/10-service.yaml
kubectl apply -f chat/docker/local-k8s/25-configmap.postgres.yaml
kubectl apply -f chat/docker/local-k8s/35-statefulset.postgres-local.yaml
kubectl rollout status statefulset/tinode
```

If the StatefulSet was already created with the older image, refresh the pods
after rebuilding:

```bash
kubectl rollout restart statefulset/tinode
kubectl rollout status statefulset/tinode
```

The local-testing StatefulSet uses `imagePullPolicy: Never` so Kubernetes
cannot silently pull or reuse a different image tag. If the image is missing on
the Minikube node, the pod will stay in `ImagePullBackOff`, which is easier to
debug than running an older local build by accident.

## 4. Verify cluster health

```bash
kubectl get pods -o wide
kubectl get endpointslices
kubectl exec tinode-0 -- curl -s localhost:6060/debug/vars | jq '{ClusterDiscoveryMode, TotalClusterNodes, LiveClusterNodes}'
```

Expected steady state:

- `ClusterDiscoveryMode = 1`
- `TotalClusterNodes = 3`
- `LiveClusterNodes = 2`

Check logs:

```bash
kubectl logs tinode-0
kubectl logs tinode-1
kubectl logs tinode-2
```

You should see cluster connections forming between peers.

## 5. Port-forward the web UI

Run these in separate terminals:

```bash
kubectl port-forward pod/tinode-0 6060:6060
kubectl port-forward pod/tinode-1 6061:6060
kubectl port-forward pod/tinode-2 6062:6060
```

Then open:

- `http://127.0.0.1:6060/`
- `http://127.0.0.1:6061/`
- `http://127.0.0.1:6062/`

Suggested sample users:

- `alice / alice123`
- `bob / bob123`
- `carol / carol123`

Use different users on different forwarded nodes and verify cross-node chat
delivery.

## 6. Validate membership changes

Scale up:

```bash
kubectl scale statefulset/tinode --replicas=5
kubectl rollout status statefulset/tinode
kubectl exec tinode-0 -- curl -s localhost:6060/debug/vars | jq '.TotalClusterNodes'
```

Scale down:

```bash
kubectl scale statefulset/tinode --replicas=2
kubectl rollout status statefulset/tinode
```

Restart one pod:

```bash
kubectl delete pod tinode-1
kubectl rollout status statefulset/tinode
```

## Notes

- PostgreSQL is intentionally ephemeral in this setup.
- The mounted config file is activated via `EXT_CONFIG=/opt/tinode/tinode.conf`.
- The StatefulSet remains `OrderedReady` because worker ID stability depends
  on pod ordinals.
