# CjPod operator design notes

## Reconciliation model

The controller uses the CjPod name and namespace as the Pod key. It renders `spec.template` into a Pod, forces the Pod name and namespace to match the CjPod, and sets a controller owner reference. An existing Pod with that key is never adopted. If it is not controlled by the current CjPod UID, reconciliation returns an error and leaves it untouched.

The controller is level-driven rather than dependent on an in-memory timer. It stores the created Pod UID, start time, and lifecycle phase in the CjPod status. The Pod's API-server `creationTimestamp` is the authoritative timer anchor. If the process exits before the first status update, the owner reference lets the next reconcile identify the Pod and recover the timestamp and UID.

The phases are:

- `Running`: the owned Pod exists and its three-minute minimum lifetime is in progress.
- `Deleting`: the minimum lifetime elapsed and deletion intent was persisted.
- `Completed`: the Pod is gone and the controller must not create another one.

Persisting `Deleting` before the delete call closes an important crash window. If the process stops after deleting the Pod but before recording completion, the restarted controller sees `Deleting` plus a missing Pod and marks the resource complete. It does not accidentally create a new Pod.

If an external actor deletes the Pod while the resource is still `Running`, the controller creates a replacement and starts a new three-minute lifetime for that new Pod. Once the controller begins deletion, it uses a UID precondition so a different same-name Pod cannot be deleted after a race.

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

The unit tests use controller-runtime's fake client and an injected clock. They verify template rendering and ownership, the no-early-delete boundary, recovery with a fresh reconciler instance, deletion and completion, refusal to touch an unowned Pod, and one-shot behavior after completion.

For production I would add envtest coverage against a real API server, leader election for multiple replicas, metrics for reconciliation failures and deletion lag, and a validating admission policy for unusable Pod templates.
