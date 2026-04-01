# EKS IP Exhaustion Fix — Runbook

## Problem

EKS cluster running on 3 private subnets with `/24` CIDR blocks:

| Subnet | CIDR | Usable IPs |
|---|---|---|
| Private AZ-1 | 10.38.4.0/24 | 251 |
| Private AZ-2 | 10.38.5.0/24 | 251 |
| Private AZ-3 | 10.38.6.0/24 | 251 |
| **Total** | | **753 IPs** |

In EKS with the VPC CNI, every pod consumes one IP directly from the subnet. With Karpenter scaling nodes aggressively, 753 IPs across all nodes and pods drains quickly, causing `FailedCreatePodSandBox` errors and pending pods.

---

## Root Cause

The VPC CIDR is `10.38.0.0/16` (65,536 IPs total). Only 768 IPs have been carved out so far — leaving ~64,768 IPs completely unused in the same VPC. No secondary CIDR or IPv6 is needed.

---

## Solution Overview

Create 3 new large private subnets (`/19` = 8,190 IPs each) from the free space in `10.38.0.0/16`, then configure EKS Custom Networking so pods use the new subnets while nodes stay on the existing `/24` subnets.

**No EKS cluster recreation required.** The control plane, deployments, services, IAM roles, and Karpenter config all remain intact. Only worker nodes are replaced (Karpenter handles this automatically).

### What changes vs what stays the same

| No change needed | Needs update |
|---|---|
| EKS control plane | VPC CNI config (2 env vars) |
| Cluster name & API endpoint | ENIConfig CRDs (one per AZ) |
| All deployments & services | Worker nodes (drain + delete) |
| IAM roles & OIDC | Karpenter subnet tags |
| Security groups | |
| kubeconfig | |

---

## New Subnet Plan

These start at `10.38.8.0`, safely clear of the existing `10.38.4–6.x` subnets.

| Subnet | CIDR | Usable IPs | AZ | Purpose |
|---|---|---|---|---|
| eks-pod-subnet-az1 | 10.38.8.0/19 | 8,190 | AZ-1 | Pod IPs |
| eks-pod-subnet-az2 | 10.38.40.0/19 | 8,190 | AZ-2 | Pod IPs |
| eks-pod-subnet-az3 | 10.38.72.0/19 | 8,190 | AZ-3 | Pod IPs |
| **Total new** | | **24,570 IPs** | | |

After the fix, ~40,000 IPs remain free in `10.38.0.0/16` for future use.

---

## Step-by-Step Fix

### Step 1 — Create the new subnets

```bash
VPC_ID="vpc-xxxxxxxx"
AZ1="ap-southeast-1a"   # replace with your actual AZs
AZ2="ap-southeast-1b"
AZ3="ap-southeast-1c"

# AZ-1
aws ec2 create-subnet \
  --vpc-id $VPC_ID \
  --cidr-block 10.38.8.0/19 \
  --availability-zone $AZ1 \
  --tag-specifications 'ResourceType=subnet,Tags=[{Key=Name,Value=eks-pod-subnet-az1}]'

# AZ-2
aws ec2 create-subnet \
  --vpc-id $VPC_ID \
  --cidr-block 10.38.40.0/19 \
  --availability-zone $AZ2 \
  --tag-specifications 'ResourceType=subnet,Tags=[{Key=Name,Value=eks-pod-subnet-az2}]'

# AZ-3
aws ec2 create-subnet \
  --vpc-id $VPC_ID \
  --cidr-block 10.38.72.0/19 \
  --availability-zone $AZ3 \
  --tag-specifications 'ResourceType=subnet,Tags=[{Key=Name,Value=eks-pod-subnet-az3}]'
```

### Step 2 — Associate new subnets with the private route table

```bash
# Find your existing private route table
aws ec2 describe-route-tables \
  --filters "Name=vpc-id,Values=$VPC_ID" \
  --query 'RouteTables[*].[RouteTableId,Tags]'

# Associate each new subnet
aws ec2 associate-route-table \
  --route-table-id rtb-xxxxxxxx \
  --subnet-id subnet-<new-az1-id>

aws ec2 associate-route-table \
  --route-table-id rtb-xxxxxxxx \
  --subnet-id subnet-<new-az2-id>

aws ec2 associate-route-table \
  --route-table-id rtb-xxxxxxxx \
  --subnet-id subnet-<new-az3-id>
```

### Step 3 — Tag new subnets for Karpenter discovery

Without these tags Karpenter won't discover the new subnets and will keep using the old `/24` ones.

```bash
aws ec2 create-tags \
  --resources subnet-<new-az1-id> subnet-<new-az2-id> subnet-<new-az3-id> \
  --tags \
    Key=kubernetes.io/cluster/<your-cluster-name>,Value=shared \
    Key=kubernetes.io/role/internal-elb,Value=1 \
    Key=karpenter.sh/discovery,Value=<your-cluster-name>
```

### Step 4 — Enable Custom Networking on the VPC CNI

This tells the CNI to place pod secondary ENIs (and therefore pod IPs) on the new subnets, while node primary IPs stay on the old `/24` subnets.

```bash
kubectl set env daemonset aws-node -n kube-system \
  AWS_VPC_K8S_CNI_CUSTOM_NETWORK_CFG=true \
  ENI_CONFIG_LABEL_DEF=topology.kubernetes.io/zone
```

### Step 5 — Create ENIConfig per AZ

The `name` field must exactly match the AZ label value on the node (e.g. `ap-southeast-1a`).

```bash
# AZ-1
cat <<EOF | kubectl apply -f -
apiVersion: crd.k8s.amazonaws.com/v1alpha1
kind: ENIConfig
metadata:
  name: ap-southeast-1a
spec:
  subnet: subnet-<new-az1-subnet-id>
  securityGroups:
    - sg-xxxxxxxx   # reuse the same SG your nodes already use
EOF

# AZ-2
cat <<EOF | kubectl apply -f -
apiVersion: crd.k8s.amazonaws.com/v1alpha1
kind: ENIConfig
metadata:
  name: ap-southeast-1b
spec:
  subnet: subnet-<new-az2-subnet-id>
  securityGroups:
    - sg-xxxxxxxx
EOF

# AZ-3
cat <<EOF | kubectl apply -f -
apiVersion: crd.k8s.amazonaws.com/v1alpha1
kind: ENIConfig
metadata:
  name: ap-southeast-1c
spec:
  subnet: subnet-<new-az3-subnet-id>
  securityGroups:
    - sg-xxxxxxxx
EOF
```

### Step 6 — Replace worker nodes

Existing nodes won't pick up the new ENIConfig until they are replaced. Drain and delete them — Karpenter automatically provisions replacements on the new subnets.

> **Tip:** Do this in batches and ensure PodDisruptionBudgets are set on critical workloads to avoid downtime.

```bash
# Drain one node at a time
kubectl drain <node-name> \
  --ignore-daemonsets \
  --delete-emptydir-data

# Delete after drain completes
kubectl delete node <node-name>

# Karpenter will immediately reprovision a replacement
# Verify new node IPs come from the 10.38.8.x / 10.38.40.x / 10.38.72.x ranges
kubectl get nodes -o wide
```

---

## Optional — Enable Prefix Delegation (additional multiplier)

Stack this on top of Custom Networking for even more pod IPs. Each ENI slot gets a `/28` prefix (16 IPs) instead of 1 IP.

```bash
kubectl set env daemonset aws-node -n kube-system \
  ENABLE_PREFIX_DELEGATION=true \
  WARM_PREFIX_TARGET=1
```

Requires Nitro-based instances (m5, m6i, c5, r5, etc.). After enabling, replace nodes again so they pick up the prefix config.

With prefix delegation on a `m5.xlarge` (4 ENI slots × 16 IPs) you get up to **64 pods per node** instead of 15.

---

## Verification

```bash
# Check pods are getting IPs from new subnets (10.38.8.x, 10.38.40.x, 10.38.72.x)
kubectl get pods -A -o wide | awk '{print $7}' | sort | uniq -c

# Check ENIConfig is applied on nodes
kubectl get nodes -o json | jq '.items[].metadata.labels["topology.kubernetes.io/zone"]'

# Check VPC CNI env vars are set
kubectl get ds aws-node -n kube-system -o yaml | grep -A2 CUSTOM_NETWORK
```

---

## Result

| | Before | After |
|---|---|---|
| Pod IP pool | ~753 IPs | ~24,570 IPs |
| IPs per AZ | ~251 | ~8,190 |
| Node disruption | — | Rolling (Karpenter managed) |
| EKS recreate needed | — | No |
| Free IPs remaining in VPC | ~64,768 | ~40,000+ |
