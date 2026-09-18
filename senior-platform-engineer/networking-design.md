# Cluster networking design

## Address plan

I would keep the existing primary VPC CIDRs for nodes, load balancer front ends, and VPN attachment infrastructure:

| Purpose | Cluster 1 | Cluster 2 |
|---|---|---|
| Primary VPC CIDR | `10.0.1.0/24` | `10.0.2.0/24` |
| Secondary VPC CIDR for Pods | `100.64.0.0/16` | `100.65.0.0/16` |

The Pod ranges come from RFC 6598 rather than consuming more of `10.0.0.0/8`. I would first confirm that neither corporate networking nor another connected VPC already uses those ranges. Each `/16` is split into one dedicated Pod subnet per Availability Zone, with enough unfragmented space for VPC CNI prefix allocation.

The primary `/24` remains a constraint. I would reserve it for resources that need addresses in the corporate-routable space and monitor its available-address count. Nodes can stay in the primary subnets, while Pods consume the much larger secondary range. If HTTP endpoints are numerous, I would share an internal ALB or Gateway and route by host/path instead of allocating one load balancer per endpoint.

There is an important prerequisite: the corporate network must not actively use `10.0.1.0/24` or `10.0.2.0/24`. Although they are inside the stated corporate `10.0.0.0/8` aggregate, those two more-specific ranges must be reserved for the VPCs and routed toward the VPN. If identical addresses already exist on premises, ordinary routing cannot distinguish them. I would stop and resolve that with renumbering or an explicit private-NAT design rather than claim that a route-table change fixes overlapping addresses.

## Making the VPC CNI use the secondary range

Associating a secondary CIDR with a VPC is not enough. The Amazon VPC CNI must be configured for custom networking.

For each VPC I would:

1. Associate its RFC 6598 secondary CIDR and create a Pod subnet in every worker-node Availability Zone.
2. Create an `ENIConfig` resource per AZ. Each resource references that AZ's Pod subnet and the security groups to attach to Pod ENIs.
3. Configure the `aws-node` DaemonSet with:

   ```text
   AWS_VPC_K8S_CNI_CUSTOM_NETWORK_CFG=true
   ENI_CONFIG_LABEL_DEF=topology.kubernetes.io/zone
   ENABLE_PREFIX_DELEGATION=true
   ```

4. Name each `ENIConfig` after its Availability Zone, for example:

   ```yaml
   apiVersion: crd.k8s.amazonaws.com/v1alpha1
   kind: ENIConfig
   metadata:
     name: us-east-1a
   spec:
     subnet: subnet-cluster1-pods-us-east-1a
     securityGroups:
       - sg-cluster1-pods
   ```

5. Roll the node groups after the CNI and `ENIConfig` resources exist. Existing nodes do not acquire custom networking automatically. I would cordon and drain them only after replacement capacity is ready.
6. Set kubelet `maxPods` to a value supported by the instance type and prefix-delegation configuration. Prefix delegation gives the CNI `/28` prefixes on secondary ENIs, which increases Pod density and reduces individual EC2 API allocation calls. Subnet CIDR reservations can protect contiguous `/28` blocks from fragmentation.

IPv4 prefix delegation requires Amazon VPC CNI `1.9.0` or later and Nitro-based
EC2 worker nodes; I would pin and verify the supported add-on version before
rolling nodes rather than assuming the cluster default supports it. Mixed node
groups must be checked individually because a legacy non-Nitro instance type can
invalidate the capacity model.

Host-network Pods continue to use node addresses from the primary CIDR. Ordinary workload Pods receive addresses from the AZ-specific secondary subnet.

## Cluster 1 to Cluster 2 without the VPN

I would create a VPC peering connection between the two VPCs. The VPCs have distinct primary and secondary CIDRs, so peering is valid. The VPN is not part of this data path.

Connecting the VPCs alone does not install the needed routes. I would add symmetric routes to every route table used by nodes, Pod subnets, and endpoint subnets:

- Cluster 1 route tables: `10.0.2.0/24` and `100.65.0.0/16` via the peering connection.
- Cluster 2 route tables: `10.0.1.0/24` and `100.64.0.0/16` via the peering connection.

A simplified Cluster 1 route table therefore includes:

| Destination | Target |
|---|---|
| `10.0.1.0/24` | local |
| `100.64.0.0/16` | local |
| `10.0.2.0/24` | VPC peer |
| `100.65.0.0/16` | VPC peer |
| `10.0.0.0/8` | corporate VPN |

Cluster 2 has the mirror image. AWS selects the longest prefix, so Cluster 1 traffic for `10.0.2.0/24` takes the `/24` peering route instead of the broader `/8` VPN route. The explicit reverse `/24` and `/16` routes keep the return path on the peer as well. VPC association automatically supplies the local primary and secondary CIDR routes; the peer and VPN routes are the entries I add.

I would then update security groups and network ACLs in both directions for the required ports. If Cluster 1 talks directly to Cluster 2 Pod IPs and source identity must be preserved, I would exempt the peer CIDRs from the VPC CNI's node SNAT using `AWS_VPC_K8S_CNI_EXCLUDE_SNAT_CIDRS`; the reverse routes above are then mandatory. This setting must be tested alongside internet egress because broad SNAT changes can break unrelated outbound traffic.

Applications should use private DNS names rather than Pod IPs. For normal service consumption, Cluster 1 resolves a private name to an internal load balancer in Cluster 2. I would associate the private hosted zone with both VPCs, or use Route 53 Resolver forwarding where corporate DNS also needs the name. Peering DNS-resolution options must be enabled if the selected name-resolution path depends on them.

Each cluster's Kubernetes Service CIDR must be checked against both VPC CIDRs,
both Pod CIDRs, corporate/on-premises routes, and any future peered or transit
ranges before cluster creation. ClusterIP addresses are virtual and are not
routed across the VPC peer in this design; cross-cluster consumers use the
Cluster 2 load balancer or another explicit multi-cluster service mechanism.
Therefore sharing a non-overlapping-with-the-network Service CIDR between the
clusters can work, but I would allocate distinct Service CIDRs to avoid ambiguity
and preserve the option of a future service-routing solution.

VPC peering is intentionally non-transitive: neither VPC can use the other's VPN through the peer. That is acceptable because the design gives each VPC its own corporate VPN and uses peering only for direct VPC-to-VPC traffic. If the environment grew to many VPCs, I would evaluate a Transit Gateway, but two VPCs do not justify the added routing and cost by themselves.

## Exposing Cluster 2 endpoints to corporate users

For TCP or independently managed services, I would use the AWS Load Balancer Controller to create internal Network Load Balancers. For HTTP/S services, I would prefer a shared internal ALB or Kubernetes Gateway with host/path routing to conserve addresses in the primary `/24`.

The load balancers are placed in private Cluster 2 subnets and are not internet-facing. Kubernetes Services or Ingress/Gateway resources restrict source ranges and security groups to the real corporate networks. With NLB IP targets, the controller can send traffic directly to Pod IPs in the secondary subnets; health-check and Pod security-group rules must allow that traffic.

Corporate routers advertise the VPC-specific `10.0.2.0/24` route over Cluster 2's VPN rather than treating it as an on-premises subnet. Return routes for the actual corporate prefixes point to the VPN attachment. Corporate DNS forwards the private service zone to Route 53 Resolver inbound endpoints, or receives equivalent private records through the organization's existing DNS integration.

This produces the following paths:

```text
Corporate client -> Cluster 2 VPN -> internal NLB/ALB -> Cluster 2 Pod
Cluster 1 Pod -> VPC peering -> Cluster 2 internal NLB/ALB (or Pod IP)
```

## Failure modes and operational checks

- **CIDR collision:** RFC 6598 ranges or the two primary `/24`s may already be used elsewhere. I would validate the full routing domain before association.
- **Asymmetric routing:** Missing return routes on one Pod or endpoint subnet cause timeouts even when the forward route is correct. Flow Logs on both VPCs help verify the path.
- **SNAT surprises:** Direct Pod routing behaves differently depending on CNI SNAT settings. I would test source addresses and security-group evaluation from both clusters.
- **Prefix fragmentation:** Prefix attachment requires contiguous `/28`s. Dedicated Pod subnets and CIDR reservations reduce allocation failures.
- **AZ failure:** Pod subnets, node groups, and load balancer subnets span at least two AZs. Capacity planning must leave enough room to absorb one AZ's workloads.
- **CNI or IP pressure:** I would alert on `ipamd` allocation errors, available subnet addresses, ENI/prefix utilization, and unschedulable Pods.
- **VPN availability:** Corporate access still depends on Cluster 2's VPN. The Cluster 1-to-Cluster 2 path does not, and can be tested separately.

References: [AWS EKS custom networking](https://docs.aws.amazon.com/eks/latest/best-practices/custom-networking.html) and [custom network interface tutorial](https://docs.aws.amazon.com/eks/latest/userguide/cni-custom-network-tutorial.html).
