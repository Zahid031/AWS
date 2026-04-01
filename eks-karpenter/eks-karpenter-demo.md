#  Production-Grade EKS + Karpenter Setup

> **Cluster:** `eks-karpenter-demo` | **Region:** `ap-southeast-1` | **K8s:** `1.35` | **Karpenter:** `v1.9.0`
> **Workers:** Private subnets only | **Autoscaler:** Karpenter (replaces Cluster Autoscaler)
> **Reference:** [karpenter.sh/docs/getting-started](https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/)

---

## 📋 Table of Contents

1. [Project Structure](#1-project-structure)
2. [Prerequisites & Tools](#2-prerequisites--tools)
3. [Environment Variables](#3-environment-variables)
4. [Step 1 — CloudFormation (IAM + SQS)](#4-step-1--cloudformation-iam--sqs)
5. [Step 2 — Create EKS Cluster](#5-step-2--create-eks-cluster-cluster-configyaml)
6. [Step 3 — Tag Subnets & Security Groups](#6-step-3--tag-private-subnets--security-groups)
7. [Step 4 — Install Karpenter](#7-step-4--install-karpenter-karpenter-valuesyaml)
8. [Step 5 — Apply Karpenter Manifests](#8-step-5--apply-karpenter-manifests-karpenter-manifestsyaml)
9. [Step 6 — Anti-Throttling FlowSchemas](#9-step-6--anti-throttling-flowschemas-karpenter-flowschemasyaml)
10. [Step 7 — Verify Everything Works](#10-step-7--verify-everything-works)
11. [Troubleshooting](#11-troubleshooting)
12. [Cheatsheet](#12-cheatsheet)

---

## 1. Project Structure

Keep all config files in a single directory. This guide produces the following files — everything you need to run or re-run the setup:

```
eks-karpenter-demo/
├── cluster-config.yaml          # eksctl ClusterConfig — creates EKS + IAM + addons
├── karpenter-values.yaml        # Helm values for Karpenter controller
├── karpenter-manifests.yaml     # EC2NodeClass + NodePool (workers on private subnets)
├── karpenter-flowschemas.yaml   # Kubernetes API anti-throttling
└── README.md                    # This file (setup guide)
```

---

## 2. Prerequisites & Tools

Install these on your **Bastion Host** before starting.

```bash
# ── 1. AWS CLI v2 ─────────────────────────────────────────────────────────────
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip && sudo ./aws/install
aws --version  # must be v2

# ── 2. kubectl ────────────────────────────────────────────────────────────────
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
kubectl version --client

# ── 3. eksctl >= v0.202.0  (REQUIRED — older versions lack Pod Identity support)
ARCH=amd64
PLATFORM=$(uname -s)_$ARCH
curl -sLO "https://github.com/eksctl-io/eksctl/releases/latest/download/eksctl_${PLATFORM}.tar.gz"
tar -xzf eksctl_${PLATFORM}.tar.gz -C /tmp
sudo mv /tmp/eksctl /usr/local/bin
eksctl version  # must be >= 0.202.0

# ── 4. helm >= 3.x ────────────────────────────────────────────────────────────
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
helm version

# ── 5. Verify AWS identity ────────────────────────────────────────────────────
aws sts get-caller-identity
```

---

## 3. Environment Variables

>  **Re-export these every time you open a new shell.** Save them in a file like `env.sh` and `source env.sh` before each session.

```bash
# ── Versions (from official Karpenter docs) ───────────────────────────────────
export KARPENTER_NAMESPACE="kube-system"
export KARPENTER_VERSION="1.9.0"
export K8S_VERSION="1.35"
export AWS_PARTITION="aws"

# ── Cluster Identity ──────────────────────────────────────────────────────────
export CLUSTER_NAME="eks-karpenter-demo"
export AWS_DEFAULT_REGION="ap-southeast-1"
export AWS_ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"

# ── Existing VPC ──────────────────────────────────────────────────────────────
# Find the VPC ID by name tag
aws ec2 describe-vpcs \
  --filters "Name=tag:Name,Values=portyou-prod-vpc" \
  --query 'Vpcs[0].VpcId' \
  --output text \
  --region ap-southeast-1

export VPC_ID="vpc-06d97bfdb665xxxxx"

# ── AMI alias version (AL2023 pinned — from official docs) ────────────────────
export ALIAS_VERSION="$(aws ssm get-parameter \
  --name "/aws/service/eks/optimized-ami/${K8S_VERSION}/amazon-linux-2023/x86_64/standard/recommended/image_id" \
  --query Parameter.Value \
  | xargs aws ec2 describe-images --query 'Images[0].Name' --image-ids \
  | sed -r 's/^.*(v[[:digit:]]+).*$/\1/')"

export TEMPOUT="$(mktemp)"

# ── Verify ────────────────────────────────────────────────────────────────────
echo "Account  : ${AWS_ACCOUNT_ID}"
echo "Cluster  : ${CLUSTER_NAME}  Region: ${AWS_DEFAULT_REGION}"
echo "Karpenter: v${KARPENTER_VERSION}  K8s: ${K8S_VERSION}  AMI alias: ${ALIAS_VERSION}"
```

---

## 4. Step 1 — CloudFormation (IAM + SQS)

> **From official docs:** The CloudFormation template creates all Karpenter IAM roles, policies, and the SQS interruption queue. Run this **before** creating the cluster.

```bash
# Download & deploy Karpenter CloudFormation stack
curl -fsSL \
  "https://raw.githubusercontent.com/aws/karpenter-provider-aws/v${KARPENTER_VERSION}/website/content/en/preview/getting-started/getting-started-with-karpenter/cloudformation.yaml" \
  > "${TEMPOUT}"

aws cloudformation deploy \
  --stack-name "Karpenter-${CLUSTER_NAME}" \
  --template-file "${TEMPOUT}" \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides "ClusterName=${CLUSTER_NAME}"

# Verify — expected: "CREATE_COMPLETE"
aws cloudformation describe-stacks \
  --stack-name "Karpenter-${CLUSTER_NAME}" \
  --query 'Stacks[0].StackStatus'
```

### What the CloudFormation Stack Creates

| Resource | Description |
|---|---|
| `KarpenterNodeRole-eks-karpenter-demo` | IAM role attached to all Karpenter-launched EC2 nodes |
| `KarpenterControllerNodeLifecyclePolicy` | Allows EC2 `RunInstances` / `TerminateInstances` |
| `KarpenterControllerIAMIntegrationPolicy` | Manages instance profiles |
| `KarpenterControllerEKSIntegrationPolicy` | Discovers EKS cluster details |
| `KarpenterControllerInterruptionPolicy` | Reads SQS spot interruption queue |
| `KarpenterControllerResourceDiscoveryPolicy` | Discovers subnets, SGs, AMIs |
| **SQS Queue + EventBridge rules** | Graceful spot interruption / rebalance handling |

---

## 5. Step 2 — Create EKS Cluster (`cluster-config.yaml`)

> **Official docs approach:** Use a single `eksctl` ClusterConfig file that includes the cluster, managed node group, IAM via Pod Identity, aws-auth mapping, and all addons.

### `cluster-config.yaml`

```yaml
# ──────────────────────────────────────────────────────────────────────────────
# eks-karpenter-demo EKS Cluster — eksctl ClusterConfig
# Ref: https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/
# ──────────────────────────────────────────────────────────────────────────────
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

accessConfig:
  # Supports both API access and legacy aws-auth ConfigMap
  # Required for iamIdentityMappings (Karpenter node role) to work
  authenticationMode: API_AND_CONFIG_MAP

metadata:
  name: eks-karpenter-demo
  region: ap-southeast-1
  version: "1.35"
  tags:
    environment: prod
    team: eks-karpenter-demo
    # REQUIRED: Karpenter uses this tag to discover the cluster
    karpenter.sh/discovery: eks-karpenter-demo

# ── Existing VPC ──────────────────────────────────────────────────────────────
vpc:
  id: "vpc-06d97bfdb6f3xxxx"
  cidr: "10.38.0.0/16"
  subnets:
    # Workers launch ONLY into private subnets (privateNetworking: true below)
    private:
      ap-southeast-1a:
        id: "subnet-0122ae8526dxxxxx"
      ap-southeast-1b:
        id: "subnet-02851165dxxxxxxx"
      ap-southeast-1c:
        id: "subnet-09d3ccdb78xxxxx"
    # Public subnets used ONLY for ALB/NLB — no nodes launch here
    public:
      ap-southeast-1a:
        id: "subnet-0b8fc61aexxxxx"
      ap-southeast-1b:
        id: "subnet-0e5a1b046xxxxx"
      ap-southeast-1c:
        id: "subnet-0dc29c5aexxxxx"

# ── IAM: OIDC + Pod Identity + Node Role Mapping ──────────────────────────────
iam:
  # Enable OIDC provider — required for Pod Identity / IRSA
  withOIDC: true

  # Pod Identity Association for Karpenter controller (official docs recommended method)
  # These policy ARNs are created by the CloudFormation stack in Step 1
  # Replace ACCOUNT_ID with original ID
  podIdentityAssociations:
    - namespace: "kube-system"
      serviceAccountName: karpenter
      roleName: eks-karpenter-demo-karpenter
      permissionPolicyARNs:
        - arn:aws:iam::ACCOUNT_ID:policy/KarpenterControllerNodeLifecyclePolicy-eks-karpenter-demo
        - arn:aws:iam::ACCOUNT_ID:policy/KarpenterControllerIAMIntegrationPolicy-eks-karpenter-demo
        - arn:aws:iam::ACCOUNT_ID:policy/KarpenterControllerEKSIntegrationPolicy-eks-karpenter-demo
        - arn:aws:iam::ACCOUNT_ID:policy/KarpenterControllerInterruptionPolicy-eks-karpenter-demo
        - arn:aws:iam::ACCOUNT_ID:policy/KarpenterControllerResourceDiscoveryPolicy-eks-karpenter-demo

  # Additional service accounts for other controllers
  serviceAccounts:
    - metadata:
        name: aws-load-balancer-controller
        namespace: kube-system
      wellKnownPolicies:
        awsLoadBalancerController: true
      roleName: eks-load-balancer-controller-role


# ── IAM Identity Mapping: Allow Karpenter nodes to join the cluster ───────────
# Official docs: must map KarpenterNodeRole so provisioned nodes can register
iamIdentityMappings:
  - arn: "arn:aws:iam::ACCOUNT_ID:role/KarpenterNodeRole-eks-karpenter-demo"
    username: "system:node:{{EC2PrivateDNSName}}"
    groups:
      - system:bootstrappers
      - system:nodes

# ── System Managed Node Group ─────────────────────────────────────────────────
# This is the SYSTEM node group — runs kube-system components and Karpenter.
# Karpenter provisions all APPLICATION worker nodes dynamically on private subnets.
managedNodeGroups:
  - name: eks-karpenter-demo-system-ng
    instanceType: m5.xlarge    # Enough for system pods + 2 Karpenter controller replicas
    amiFamily: AmazonLinux2023
    desiredCapacity: 2         # 2 for HA (Karpenter controller runs 2 replicas)
    minSize: 2
    maxSize: 4                 # Only scales for system components
    volumeSize: 50
    volumeType: gp3

    # Workers on PRIVATE subnets only
    privateNetworking: true

    # Custom SG + SSH access
    securityGroups:
      attachIDs:
        - "sg-0cbf0511da65c5fXXX"
    ssh:
      allow: false


    # Label + taint so ONLY system pods land here
    # Karpenter controller has a toleration for this taint
    labels:
      node-role: system
      node-type: kube-system
    taints:
      - key: CriticalAddonsOnly
        value: "true"
        effect: NoSchedule

    iam:
      attachPolicyARNs:
        - arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy
        - arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy
        - arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly
        - arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy
      withAddonPolicies:
        cloudWatch: true
        ebs: true
        albIngress: true
        awsLoadBalancerController: true

    tags:
      environment: prod
      node-role: system

# ── EKS Addons ────────────────────────────────────────────────────────────────
addons:
  - name: vpc-cni
    version: latest
    configurationValues: '{"enableNetworkPolicy": "true"}'

  - name: coredns
    version: latest

  - name: kube-proxy
    version: latest

  - name: aws-ebs-csi-driver
    version: latest
    wellKnownPolicies:
      ebsCSIController: true

  # REQUIRED for Pod Identity (Karpenter IRSA)
  - name: eks-pod-identity-agent
    version: latest

# ── Kubernetes Networking ─────────────────────────────────────────────────────
kubernetesNetworkConfig:
  ipFamily: IPv4

# ── CloudWatch Logging ────────────────────────────────────────────────────────
cloudWatch:
  clusterLogging:
    enableTypes: ["*"]
    logRetentionInDays: 7
```

> ⚠️ **Replace `ACCOUNT_ID`** with your actual AWS account ID before applying. Use `envsubst` or the `sed` command below.

### Run the Cluster Creation

```bash
# Replace ACCOUNT_ID placeholder with real account ID
sed "s/ACCOUNT_ID/${AWS_ACCOUNT_ID}/g" cluster-config.yaml > cluster-config-rendered.yaml

# Create the cluster (takes ~15–20 minutes)
eksctl create cluster -f cluster-config-rendered.yaml

# Export endpoint and role ARN after creation
export CLUSTER_ENDPOINT="$(aws eks describe-cluster \
  --name "${CLUSTER_NAME}" \
  --query "cluster.endpoint" \
  --output text)"

export KARPENTER_IAM_ROLE_ARN="arn:${AWS_PARTITION}:iam::${AWS_ACCOUNT_ID}:role/${CLUSTER_NAME}-karpenter"

echo "Endpoint : ${CLUSTER_ENDPOINT}"
echo "Role ARN : ${KARPENTER_IAM_ROLE_ARN}"

# Enable EC2 Spot service-linked role (run once per AWS account)
# From official Karpenter docs — prevents ServiceLinkedRoleCreationNotPermitted error
aws iam create-service-linked-role --aws-service-name spot.amazonaws.com || true
```

---

## 6. Step 3 — Tag Private Subnets & Security Groups

> Karpenter discovers subnets and security groups via the `karpenter.sh/discovery` tag. Tag **only private subnets** so workers never land in public subnets.

```bash
# Tag PRIVATE subnets only
for SUBNET_ID in \
  subnet-09ec1f093bXXX \
  subnet-02851165d8XXX \
  subnet-09d3ccdb78XXX; do
  aws ec2 create-tags \
    --resources "${SUBNET_ID}" \
    --tags "Key=karpenter.sh/discovery,Value=${CLUSTER_NAME}"
  echo "Tagged subnet: ${SUBNET_ID}"
done

# Tag the cluster security group
CLUSTER_SG="$(aws eks describe-cluster \
  --name "${CLUSTER_NAME}" \
  --query 'cluster.resourcesVpcConfig.clusterSecurityGroupId' \
  --output text)"

aws ec2 create-tags \
  --resources "${CLUSTER_SG}" \
  --tags "Key=karpenter.sh/discovery,Value=${CLUSTER_NAME}"

# Tag your custom node security group
aws ec2 create-tags \
  --resources "sg-0cbf0511da65c5fa6" \
  --tags "Key=karpenter.sh/discovery,Value=${CLUSTER_NAME}"

# Verify tags
aws ec2 describe-subnets \
  --filters "Name=tag:karpenter.sh/discovery,Values=${CLUSTER_NAME}" \
  --query 'Subnets[].{ID:SubnetId, AZ:AvailabilityZone, CIDR:CidrBlock}' \
  --output table
```

> ⚠️ **Do NOT tag public subnets.** If public subnets have the discovery tag, Karpenter may launch nodes there. Only the three private subnet IDs above should be tagged.

---

## 7. Step 4 — Install Karpenter (`karpenter-values.yaml`)

### `karpenter-values.yaml`

```yaml
# ──────────────────────────────────────────────────────────────────────────────
# Karpenter Helm values — eks-karpenter-demo
# Ref: https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/
# ──────────────────────────────────────────────────────────────────────────────

settings:
  clusterName: eks-karpenter-demo
  # SQS queue name for spot interruption handling (same as cluster name — created by CloudFormation)
  interruptionQueue: eks-karpenter-demo

# Run 2 replicas for high availability in production
replicas: 2

# Controller resource limits — from official docs baseline
controller:
  resources:
    requests:
      cpu: "1"
      memory: "1Gi"
    limits:
      cpu: "1"
      memory: "1Gi"

# Karpenter controller runs on the system node group
# It tolerates the CriticalAddonsOnly taint set on the system node group
tolerations:
  - key: CriticalAddonsOnly
    operator: Exists
    effect: NoSchedule

# Prefer system node group for Karpenter controller pods
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: node-role
              operator: In
              values:
                - system
  podAntiAffinity:
    # Spread 2 replicas across different nodes for HA
    requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            app.kubernetes.io/name: karpenter
        topologyKey: kubernetes.io/hostname

# Use ClusterFirst DNS policy (keeps internal DNS resolution)
dnsPolicy: ClusterFirst

# Pod disruption budget — keep at least 1 Karpenter controller running
podDisruptionBudget:
  minAvailable: 1
```

### Install via Helm

```bash
# Logout to do unauthenticated pull from ECR Public (from official docs)
helm registry logout public.ecr.aws || true

# Install Karpenter
helm upgrade --install karpenter \
  oci://public.ecr.aws/karpenter/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" \
  --create-namespace \
  --values karpenter-values.yaml \
  --wait

# Verify pods are running — expected: 2/2 Running
kubectl get pods -n "${KARPENTER_NAMESPACE}" -l app.kubernetes.io/name=karpenter

# Verify Karpenter logs — no errors at startup
kubectl logs -n "${KARPENTER_NAMESPACE}" \
  -l app.kubernetes.io/name=karpenter \
  -c controller \
  --tail=30
```

---

## 8. Step 5 — Apply Karpenter Manifests (`karpenter-manifests.yaml`)

> From official docs: A single `NodePool` handles many pod shapes. `EC2NodeClass` defines HOW nodes launch. Both are applied together in one file.

### `karpenter-manifests.yaml`

```yaml
# ──────────────────────────────────────────────────────────────────────────────
# Karpenter EC2NodeClass + NodePool — eks-karpenter-demo
# Ref: https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/
# Workers launch in PRIVATE subnets only via karpenter.sh/discovery tag
# ──────────────────────────────────────────────────────────────────────────────

# EC2NodeClass: HOW to launch nodes
# Discovers subnets and security groups via tags set in Step 3
apiVersion: karpenter.k8s.aws/v1
kind: EC2NodeClass
metadata:
  name: default
spec:
  # IAM role for Karpenter-launched nodes (created by CloudFormation in Step 1)
  role: "KarpenterNodeRole-eks-karpenter-demo"

  # AMI — AmazonLinux2023, pinned to the alias version resolved at setup time
  # Replace ALIAS_VERSION with the output of the $ALIAS_VERSION env var
  amiSelectorTerms:
    - alias: "al2023@ALIAS_VERSION"

  # Subnet discovery — only private subnets are tagged (see Step 3)
  subnetSelectorTerms:
    - tags:
        karpenter.sh/discovery: "eks-karpenter-demo"

  # Security group discovery — cluster SG + node SG tagged in Step 3
  securityGroupSelectorTerms:
    - tags:
        karpenter.sh/discovery: "eks-karpenter-demo"

  # Block device — 100GB gp3 encrypted
  blockDeviceMappings:
    - deviceName: /dev/xvda
      ebs:
        volumeSize: 100Gi
        volumeType: gp3
        encrypted: true
        deleteOnTermination: true

  # Tags applied to all EC2 instances Karpenter launches
  tags:
    environment: prod
    team: eks-karpenter-demo
    managed-by: karpenter
    karpenter.sh/discovery: "eks-karpenter-demo"

---
# NodePool: WHAT nodes to launch and when to disrupt them
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  template:
    metadata:
      labels:
        managed-by: karpenter
        node-role: worker
    spec:
      requirements:
        # Architecture
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]

        # OS
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]

        # Capacity type — on-demand and spot for cost optimization
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand", "spot"]

        # Instance families — c (compute), m (general), r (memory)
        - key: karpenter.k8s.aws/instance-category
          operator: In
          values: ["c", "m", "r"]

        # Minimum instance generation — 5th gen and newer
        - key: karpenter.k8s.aws/instance-generation
          operator: Gt
          values: ["4"]

      # Reference the EC2NodeClass defined above
      nodeClassRef:
        group: karpenter.k8s.aws
        kind: EC2NodeClass
        name: default

      # Node expiry — recycle nodes every 30 days for AMI security patches
      expireAfter: 720h  # 30 days

  # Safety cap — Karpenter will not exceed this total capacity
  limits:
    cpu: "1000"
    memory: "2000Gi"

  # Disruption / consolidation settings
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    # Wait 5 min before consolidating (avoids thrashing on brief idle periods)
    consolidateAfter: 5m

    # Never disrupt more than 10% of Karpenter nodes at once
    budgets:
      - nodes: "10%"
```

### Render and Apply

```bash
# Render ALIAS_VERSION into the manifest
sed "s/ALIAS_VERSION/${ALIAS_VERSION}/g" karpenter-manifests.yaml \
  | kubectl apply -f -

# Verify NodePool and EC2NodeClass are ready
kubectl get nodepool        # Expected: READY=True, NODES=0 (no workload yet)
kubectl get ec2nodeclass    # Expected: READY=True

# Check discovered subnets and security groups
kubectl describe ec2nodeclass default | grep -A 10 "Subnets\|Security Groups"
```

---

## 9. Step 6 — Anti-Throttling FlowSchemas (`karpenter-flowschemas.yaml`)

> From official docs: Since Karpenter is in `kube-system`, apply these FlowSchemas explicitly to guarantee Karpenter is never API-throttled by other busy components.

### `karpenter-flowschemas.yaml`

```yaml
# ──────────────────────────────────────────────────────────────────────────────
# Karpenter API Server FlowSchemas — eks-karpenter-demo
# Ref: https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/#preventing-apiserver-request-throttling
# ──────────────────────────────────────────────────────────────────────────────

# High priority for leader election (lease management)
apiVersion: flowcontrol.apiserver.k8s.io/v1
kind: FlowSchema
metadata:
  name: karpenter-leader-election
spec:
  distinguisherMethod:
    type: ByUser
  matchingPrecedence: 200
  priorityLevelConfiguration:
    name: leader-election
  rules:
    - resourceRules:
        - apiGroups:
            - coordination.k8s.io
          namespaces:
            - "*"
          resources:
            - leases
          verbs:
            - get
            - create
            - update
      subjects:
        - kind: ServiceAccount
          serviceAccount:
            name: karpenter
            namespace: kube-system

---
# High priority for all Karpenter workload API calls
apiVersion: flowcontrol.apiserver.k8s.io/v1
kind: FlowSchema
metadata:
  name: karpenter-workload
spec:
  distinguisherMethod:
    type: ByUser
  matchingPrecedence: 1000
  priorityLevelConfiguration:
    name: workload-high
  rules:
    - nonResourceRules:
        - nonResourceURLs:
            - "*"
          verbs:
            - "*"
      resourceRules:
        - apiGroups:
            - "*"
          clusterScope: true
          namespaces:
            - "*"
          resources:
            - "*"
          verbs:
            - "*"
      subjects:
        - kind: ServiceAccount
          serviceAccount:
            name: karpenter
            namespace: kube-system
```

```bash
kubectl apply -f karpenter-flowschemas.yaml

# Verify
kubectl get flowschema | grep karpenter
```

---

## 10. Step 7 — Verify Everything Works

### 10.1 Check Cluster Health

```bash
# Cluster nodes — system node group should be Ready
kubectl get nodes -o wide

# Karpenter pods — expected: 2/2 Running
kubectl get pods -n kube-system -l app.kubernetes.io/name=karpenter

# All system pods
kubectl get pods -n kube-system
```

### 10.2 Test Karpenter Node Provisioning

```bash
# Deploy a test workload (from official docs)
kubectl apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
  namespace: default
spec:
  replicas: 0
  selector:
    matchLabels:
      app: inflate
  template:
    metadata:
      labels:
        app: inflate
    spec:
      terminationGracePeriodSeconds: 0
      securityContext:
        runAsUser: 1000
        runAsGroup: 3000
        fsGroup: 2000
      containers:
        - name: inflate
          image: public.ecr.aws/eks-distro/kubernetes/pause:3.7
          resources:
            requests:
              cpu: "1"
              memory: "1Gi"
          securityContext:
            allowPrivilegeEscalation: false
EOF

# Scale up — triggers Karpenter to provision new nodes
kubectl scale deployment inflate --replicas=5

# Watch Karpenter logs for provisioning events
kubectl logs -f -n kube-system \
  -l app.kubernetes.io/name=karpenter \
  -c controller \
  | grep -E "launched|provisioned|nodeclaim|NodeClaim"

# Watch nodes appear
kubectl get nodes -w
```

### 10.3 Verify Workers Are in Private Subnets

```bash
# Check all node IPs — must be in private CIDR ranges
kubectl get nodes -o wide

# Cross-check in AWS — all instances should have NO public IPs
aws ec2 describe-instances \
  --filters "Name=tag:karpenter.sh/nodepool,Values=default" \
  --query 'Reservations[].Instances[].{ID:InstanceId,Subnet:SubnetId,IP:PrivateIpAddress,State:State.Name}' \
  --output table
```

### 10.4 Test Consolidation (Scale Down)

```bash
# Delete the test workload
kubectl delete deployment inflate

# Watch Karpenter consolidate and terminate empty nodes (takes ~5-10 min)
kubectl logs -f -n kube-system \
  -l app.kubernetes.io/name=karpenter \
  -c controller \
  | grep -E "consolidat|terminat|disruption"
```

### 10.5 Verify Spot Interruption Queue

```bash
# SQS queue exists (created by CloudFormation in Step 1)
aws sqs get-queue-url \
  --queue-name "${CLUSTER_NAME}" \
  --region "${AWS_DEFAULT_REGION}"

# EventBridge rules are active
aws events list-rules \
  --name-prefix "Karpenter" \
  --region "${AWS_DEFAULT_REGION}" \
  --query 'Rules[].{Name:Name,State:State}' \
  --output table
```

---

## 11. Troubleshooting

### Nodes Not Launching

```bash
# Check Karpenter controller logs for errors
kubectl logs -n kube-system \
  -l app.kubernetes.io/name=karpenter \
  -c controller \
  | grep -i "error\|failed\|warn"
```

**Common causes:**

| Cause | Fix |
|---|---|
| Subnets not tagged | Re-run Step 3 |
| `KarpenterNodeRole` not in `aws-auth` | Check `iamIdentityMappings` in `cluster-config.yaml` |
| CloudFormation stack missing | Re-run Step 1 |
| IAM policy ARNs use wrong account ID | Check `AWS_ACCOUNT_ID` |

### EC2NodeClass Shows `READY=False`

```bash
kubectl describe ec2nodeclass default

# Check if subnets are tagged correctly — should show 3 private subnets
aws ec2 describe-subnets \
  --filters "Name=tag:karpenter.sh/discovery,Values=${CLUSTER_NAME}" \
  --query 'Subnets[].{ID:SubnetId,AZ:AvailabilityZone}' \
  --output table
```

> If empty, re-run Step 3.

### Nodes Launching in Wrong Subnet (Public)

```bash
# Remove karpenter.sh/discovery tag from public subnets
for SUBNET_ID in \
  subnet-0b8fc61aee99e \
  subnet-0e5a1b046c7ud \
  subnet-0dc29c5ae7014; do
  aws ec2 delete-tags \
    --resources "${SUBNET_ID}" \
    --tags "Key=karpenter.sh/discovery"
  echo "Removed tag from: ${SUBNET_ID}"
done
```

### NodePool Limit Reached

```bash
# Check current usage vs limits
kubectl get nodepool default -o jsonpath='{.status}' | jq .

# Increase limits in karpenter-manifests.yaml, then re-apply:
# limits:
#   cpu: "2000"
#   memory: "4000Gi"
```

### Karpenter Controller OOMKilled

```bash
# Update karpenter-values.yaml to increase memory, then upgrade:
# controller:
#   resources:
#     requests:
#       memory: "2Gi"
#     limits:
#       memory: "2Gi"

helm upgrade karpenter \
  oci://public.ecr.aws/karpenter/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" \
  --values karpenter-values.yaml
```

---

## 12. Cheatsheet

```bash
# ── Karpenter Status ──────────────────────────────────────────────────────────
kubectl get nodepool                                    # NodePool status + node count
kubectl get nodeclaims                                  # All node requests from Karpenter
kubectl get ec2nodeclass                                # EC2NodeClass status
kubectl get nodes -l karpenter.sh/nodepool=default      # Karpenter-managed nodes only
kubectl logs -n kube-system -l app.kubernetes.io/name=karpenter -c controller --tail=50

# ── Node Operations ───────────────────────────────────────────────────────────
kubectl delete node <node-name>                         # Graceful drain + terminate
kubectl annotate node <node-name> karpenter.sh/do-not-disrupt=true    # Pause disruption
kubectl annotate node <node-name> karpenter.sh/do-not-disrupt-        # Resume disruption

# ── Inspect ───────────────────────────────────────────────────────────────────
kubectl describe nodeclaim <name>                       # Full NodeClaim details
kubectl describe ec2nodeclass default                   # Subnets/SGs discovered
kubectl get events -n kube-system --field-selector reason=Launched

# ── Helm ──────────────────────────────────────────────────────────────────────
helm list -n kube-system | grep karpenter               # Installed version
helm history karpenter -n kube-system                   # Upgrade history
helm upgrade karpenter oci://public.ecr.aws/karpenter/karpenter \
  --version "${KARPENTER_VERSION}" \
  --namespace "${KARPENTER_NAMESPACE}" \
  --values karpenter-values.yaml

# ── AWS Cross-Check ───────────────────────────────────────────────────────────
aws ec2 describe-instances \
  --filters "Name=tag:karpenter.sh/nodepool,Values=default" \
  --query 'Reservations[].Instances[].{ID:InstanceId,Type:InstanceType,Subnet:SubnetId,IP:PrivateIpAddress}' \
  --output table

aws sqs get-queue-url --queue-name eks-karpenter-demo --region ap-southeast-1
```

---

## 🖥️ Dashboard Access

```bash
export CLUSTER_NAME="eks-karpenter-demo"
export REGION="ap-southeast-1"
export SSO_ROLE_ARN="arn:aws:iam::19518892208:role/aws-reserved/sso.amazonaws.com/ap-southeast-1/AWSReservedSSO_AdministratorAccess_fa4aef520f296a7"

# Step 1: Create access entry for the SSO role
aws eks create-access-entry \
  --cluster-name ${CLUSTER_NAME} \
  --region ${REGION} \
  --principal-arn ${SSO_ROLE_ARN}

# Step 2: Associate the cluster admin policy
aws eks associate-access-policy \
  --cluster-name ${CLUSTER_NAME} \
  --region ${REGION} \
  --principal-arn ${SSO_ROLE_ARN} \
  --policy-arn arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy \
  --access-scope type=cluster
```

---

## 📄 Summary of Files

| File | Purpose | When to Apply |
|---|---|---|
| `cluster-config.yaml` | EKS cluster, system node group, IAM, addons | Once — cluster creation |
| `karpenter-values.yaml` | Helm chart config for Karpenter controller | After cluster is ready |
| `karpenter-manifests.yaml` | EC2NodeClass + NodePool (worker behavior) | After Karpenter is installed |
| `karpenter-flowschemas.yaml` | API server priority for Karpenter | After Karpenter is installed |

## 🔢 Order of Operations

```
1.  Export env vars
2.  Deploy CloudFormation (IAM + SQS)          →  aws cloudformation deploy
3.  Create cluster                              →  eksctl create cluster -f cluster-config.yaml
4.  Tag private subnets + security groups       →  aws ec2 create-tags
5.  Enable EC2 Spot service-linked role         →  aws iam create-service-linked-role
6.  Install Karpenter                           →  helm upgrade --install ... --values karpenter-values.yaml
7.  Apply Karpenter manifests                   →  kubectl apply -f karpenter-manifests.yaml
8.  Apply FlowSchemas                           →  kubectl apply -f karpenter-flowschemas.yaml
9.  Verify                                      →  kubectl get nodepool, deploy inflate test
```

---

*Reference: [karpenter.sh/docs/getting-started/getting-started-with-karpenter](https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/) — Karpenter v1.9.0*
