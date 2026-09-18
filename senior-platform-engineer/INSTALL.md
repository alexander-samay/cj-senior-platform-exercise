# Build and run

The repository includes a runnable controller manager, least-privilege RBAC, a
two-replica leader-elected Deployment, health probes, a container build, and a
sample `CjPod`.

## Local verification

```sh
make verify
make test-integration
```

`test-integration` downloads matching `envtest` control-plane binaries and runs
the full lifecycle against a real API server. It verifies CRD admission,
manager startup and watches, Pod creation, manager restart, deadline recovery,
Pod deletion, and the final status update.

## Install on a cluster

Build and publish the image, then point the Kustomize image override at it:

```sh
docker build -t REGISTRY/cjpod-controller:TAG .
docker push REGISTRY/cjpod-controller:TAG
cd config
kustomize edit set image cjpod-controller=REGISTRY/cjpod-controller:TAG
kubectl apply -k .
kubectl apply -f sample.yaml
```

The controller exposes metrics on port `8080` and liveness/readiness endpoints
on port `8081`. Leader election is enabled in the Deployment so only one of the
two replicas actively reconciles resources.
