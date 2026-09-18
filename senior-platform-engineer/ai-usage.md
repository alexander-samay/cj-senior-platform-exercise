# AI usage

## Tool

I used OpenAI Codex as a design and implementation assistant.

## Prompt summaries

- Read the assignment and identify the required deliverables and hidden failure cases.
- Design EKS networking for small primary VPC CIDRs, thousands of Pods, corporate VPN access, and direct inter-VPC traffic.
- Design a restart-safe CjPod reconciler that cannot delete an unrelated same-name Pod.
- Trace the supplied Alertmanager label set through the route tree and review the inhibition rule.
- Draft unit tests and review the written explanations for unsupported assumptions.
- Audit the submission against senior-level production concerns, including runtime packaging, CRD validation, status diagnostics, and adversarial failure recovery.

## Suggestions accepted

- Use distinct RFC 6598 secondary CIDRs with Amazon VPC CNI custom networking, AZ-specific `ENIConfig` resources, and prefix delegation.
- Treat routing, return routing, security controls, DNS, and CNI SNAT behavior as separate requirements beyond creating a VPC peering connection.
- Persist CjPod lifecycle state and use the Pod's creation timestamp instead of an in-process timer.
- Persist `Deleting` before issuing the delete request and use a Pod UID precondition.
- Explain Alertmanager's first-matching-child behavior and test routing with representative label sets.
- Add fault-injection tests and an envtest lifecycle test instead of relying only on happy-path fake-client coverage.
- Add a runnable manager, leader-elected deployment, RBAC, health probes, container image, sample resource, and installation instructions.
- Publish conditions, observed generation, and Kubernetes Events, and tighten CRD admission for required container fields and lifecycle phase values.
- Restrict template metadata to labels and annotations, report external child
  deletion as a terminal lifecycle failure, and separate `Ready`, `Completed`,
  and `Failed` condition semantics.
- Pin build-stage images by digest and add cross-node anti-affinity, topology
  spreading, a PodDisruptionBudget, a default NetworkPolicy, and seccomp.

## Suggestions rejected or changed

- I rejected an in-memory three-minute timer because a process restart would lose it.
- I rejected deleting a Pod by name alone because a different Pod could be created under the same key during a race.
- I rejected solving Pod exhaustion by allocating more addresses from `10.0.0.0/8`, which violates the assignment constraint.
- I rejected describing VPC peering as sufficient by itself; subnet route tables, return paths, security controls, DNS, and possible SNAT exclusions are also required.
- I rejected merely moving the PagerDuty route above the Slack route without deciding whether critical alerts should also continue to Slack.
- I rejected treating every transient reconciliation error as terminal; failure conditions are diagnostic and the controller continues to retry.
- I rejected hand-copying the upstream Pod OpenAPI schema because it would drift from the Kubernetes version; `controller-gen` now derives the complete schema from the pinned Kubernetes Go types.

## Independent verification

- I read the assignment and traced every stated requirement to a section or test.
- I checked the custom-networking mechanism and environment variables against the AWS EKS documentation.
- I checked route traversal and the default `continue: false` behavior against the Prometheus Alertmanager documentation.
- I validated the original and proposed Alertmanager configurations with `amtool check-config` and exercised four representative label sets with `amtool config routes test`.
- I reviewed the controller's crash windows and ownership checks as a state machine rather than relying only on generated code.
- I formatted the code, reviewed the resulting diff, and ran `go vet ./...`, `go test ./...`, and `go test -race ./...`. All checks passed.
- I built the runnable manager and ran the integration-tagged lifecycle test against envtest, including CRD admission and a complete manager restart.
- I rendered the Kustomize manifests, built and started the non-root container image, and repeated the shuffled unit suite 100 times.
- I installed the generated CRD, RBAC, and two-replica Deployment in a clean Minikube cluster; verified leader election and failover, the live three-minute boundary, Conditions and Events, immutable admission, and owner-reference garbage collection; then removed the temporary cluster.
- After the final hardening pass, I repeated installation on a clean two-node
  Minikube cluster. I verified the replicas landed on different nodes, the PDB,
  NetworkPolicy, seccomp profile and narrowed RBAC were active, unsafe template
  metadata was rejected, external Pod deletion did not create a replacement,
  the lifecycle completed at the 180-second boundary, and leader failover
  succeeded.
