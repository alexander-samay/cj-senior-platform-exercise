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
kubectl apply --server-side -f crd/cjpods.yaml
kubectl wait --for=condition=Established crd/cjpods.interview.cj.dev --timeout=60s
kubectl apply --server-side -k .
kubectl apply -f sample.yaml
```

Server-side apply is intentional: the generated `PodTemplateSpec` OpenAPI
schema is large enough that client-side apply can exceed Kubernetes' annotation
size limit when it stores the full manifest in `last-applied-configuration`.
Installing and establishing the CRD before the Deployment also prevents a
startup discovery race in the controller manager.

The controller exposes metrics on port `8080` and liveness/readiness endpoints
on port `8081`. Leader election is enabled in the Deployment so only one of the
two replicas actively reconciles resources.
