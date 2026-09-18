# CjPod operator design notes

## Reconciliation model

The controller uses the CjPod name and namespace as the Pod key. It renders `spec.template` into a Pod, forces the Pod name and namespace to match the CjPod, and sets a controller owner reference. An existing Pod with that key is never adopted. If it is not controlled by the current CjPod UID, reconciliation returns an error and leaves it untouched.

The controller is level-driven rather than dependent on an in-memory timer. It stores the created Pod UID, start time, and lifecycle phase in the CjPod status. The Pod's API-server `creationTimestamp` is the authoritative timer anchor. If the process exits before the first status update, the owner reference lets the next reconcile identify the Pod and recover the timestamp and UID.

The status also publishes `observedGeneration` and standard Kubernetes
conditions. `Ready` communicates progress or completion, while `Failed`
contains a machine-readable reason and human-readable message for the latest
reconciliation error. Normal and warning Kubernetes Events expose important
lifecycle transitions without requiring access to controller logs.

The phases are:

- `Running`: the owned Pod exists and its three-minute minimum lifetime is in progress.
- `Deleting`: the minimum lifetime elapsed and deletion intent was persisted.
- `Completed`: the Pod is gone and the controller must not create another one.

Persisting `Deleting` before the delete call closes an important crash window. If the process stops after deleting the Pod but before recording completion, the restarted controller sees `Deleting` plus a missing Pod and marks the resource complete. It does not accidentally create a new Pod.

If an external actor deletes the Pod while the resource is still `Running`, the controller creates a replacement and starts a new three-minute lifetime for that new Pod. Once the controller begins deletion, it uses a UID precondition so a different same-name Pod cannot be deleted after a race.

`Completed` is intentionally terminal. A CjPod represents one bounded Pod run,
not a continuously converging Deployment. The CRD uses CEL to make `spec`
immutable, so changing the template after creation is rejected with an admission
error instead of being silently ignored. A caller creates a new CjPod with a new
name for another run.

## Timing

The reconciler returns `RequeueAfter` for the remaining lifetime. Every reconcile recalculates the deadline from persisted API state, so operator restarts do not reset the clock and cannot cause an early controller-initiated deletion.

In a distributed system, "exactly three minutes" means **not before three minutes, then as soon as the controller is scheduled after the deadline**. API latency, controller work queues, and control-plane outages can make deletion later than the deadline. A strict wall-clock guarantee is not possible for an ordinary Kubernetes controller.

## Recovery and ownership

- The controller watches CjPods and owned Pods, so Pod changes enqueue the parent resource.
- A missing CjPod is ignored. Kubernetes garbage collection handles an owned Pod if the CjPod itself is deleted.
- An unowned same-name Pod is a collision, not something the controller may delete or overwrite.
- A stored Pod UID mismatch is treated as a safety error.
- Status is a CRD subresource so lifecycle writes do not overwrite a concurrent spec change.

## Tests

The unit tests use controller-runtime's fake client, an injected clock, and
fault-injecting client wrappers. They verify template rendering and ownership,
the no-early-delete boundary, timer recovery with a fresh reconciler instance,
recovery from a persisted deletion intent, deletion and completion, refusal to
touch an unowned Pod or a different Pod UID, and one-shot behavior after
completion. Failure-path cases prove recovery when Pod creation succeeds but
the first status write fails, deletion fails after intent is persisted, and the
final completion status write conflicts. They also cover a temporary API read
failure, an admission failure exposed through conditions, a Pod held in
`Terminating` by a finalizer, and a parent that disappears during reconciliation.

The integration-tagged envtest starts a real Kubernetes API server, installs
the CRD, and starts the controller manager. It verifies admission rejects an
empty Pod template and exercises create, watch delivery, a full manager restart,
deadline recovery, deletion, and completed status. `make test-integration`
installs the matching envtest binaries and runs this test.

The CRD is generated from the Go types with `controller-gen`, so the complete
upstream `PodTemplateSpec` OpenAPI schema is present rather than a permissive
unknown-fields escape hatch. Additional CEL validation requires non-empty
container names and images, makes the one-shot spec immutable, and constrains
status phases to the controller's three known values. `make generate-crd`
reproduces the checked-in manifest. Unknown-field preservation is limited to
the nested Kubernetes metadata object so labels and annotations remain usable;
the Pod spec itself retains its generated structural schema.

## Runtime packaging

Although the exercise only requires reconciler code, the repository also
includes a runnable manager with signal handling, metrics, health probes and
optional leader election. A non-root distroless image, ServiceAccount, RBAC,
two-replica leader-elected Deployment, Kustomize configuration, sample resource,
and installation instructions are included. See `INSTALL.md`.

For a larger production system I would publish domain-specific metrics for
reconciliation failures and deletion lag, and add an end-to-end test on the
oldest and newest supported Kubernetes versions.
