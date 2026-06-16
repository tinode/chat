# Tinode on Minikube with PostgreSQL

This is the local Minikube flow for validating Tinode's Kubernetes-based
cluster discovery against the current workspace code. It is not production
guidance.

It differs from the stock `docker/tinode/Dockerfile` in one important way:
the Minikube image is built from the local branch instead of downloading a
released Tinode tarball.

## Prerequisites

- Minikube is installed
- `kubectl` is configured to talk to Minikube
- the sibling webapp directory exists at `../webapp` relative to this repo
- build context directory is the parent that contains both `chat/` and
  `webapp/`

Directory layout expected by the build:

```text
<parent>/
  chat/
  webapp/
```

## 1. Start Minikube

```bash
minikube start
kubectl config current-context
minikube status
```

The current `kubectl` context should point to Minikube before you continue.

## 2. Build the local Tinode image

Run this from the parent directory that contains both `chat/` and `webapp/`:

```bash
minikube image build \
  -f chat/docker/tinode/Dockerfile.k8s \
  -t tinode/tinode:k8s \
  .
```

Optional sanity check:

```bash
minikube image ls | grep 'tinode/tinode'
```

The image contains:

- the current branch's `tinode` server built with `-tags postgres`
- `init-db` built from `tinode-db`
- the Kubernetes-specific `/opt/tinode/entrypoint-k8s.sh`
- the bundled webapp assets under `/opt/tinode/static`

## 3. Apply manifests

Apply in this order:

```bash
kubectl apply -f chat/docker/local-k8s/15-postgres.yaml
kubectl rollout status deployment/postgres

kubectl apply -f chat/docker/k8s/00-rbac.yaml
kubectl apply -f chat/docker/k8s/10-service.yaml
kubectl apply -f chat/docker/k8s/20-configmap.yaml
kubectl apply -f chat/docker/k8s/30-statefulset.yaml
kubectl rollout status statefulset/tinode
```

If you rebuild the local image later, restart the StatefulSet so the pods pick
up the new image:

```bash
kubectl rollout restart statefulset/tinode
kubectl rollout status statefulset/tinode
```

The shared StatefulSet uses `imagePullPolicy: IfNotPresent`, so Minikube can
use the locally built `tinode/tinode:k8s` image without trying to pull it on
every start.

## 4. Verify cluster health

```bash
kubectl get pods -o wide
kubectl get svc tinode-headless postgres
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

If a pod is not starting, inspect it directly:

```bash
kubectl describe pod tinode-0
```

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

You can stop the port-forwards with `Ctrl+C` in each terminal.

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

Recheck cluster membership after each operation:

```bash
kubectl exec tinode-0 -- curl -s localhost:6060/debug/vars | jq '.TotalClusterNodes'
```

## Notes

- PostgreSQL is intentionally ephemeral in this setup.
- The mounted config file is activated via `EXT_CONFIG=/opt/tinode/tinode.conf`.
- The StatefulSet remains `OrderedReady` because worker ID stability depends
  on pod ordinals.
