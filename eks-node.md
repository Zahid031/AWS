apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: my-eks-cluster  # Must match your existing cluster name
  region: us-east-1  # Must match your cluster region

# Managed Node Groups - all in private subnets
managedNodeGroups:
  - name: private-ng-1
    instanceType: t3.medium
    desiredCapacity: 2
    minSize: 1
    maxSize: 4
    volumeSize: 20
    volumeType: gp3
    privateNetworking: true  # This ensures nodes are placed in private subnets
    # Nodes will use the private subnets from your cluster VPC configuration
    labels:
      role: worker
      environment: production
    tags:
      nodegroup-role: worker
    iam:
      withAddonPolicies:
        imageBuilder: true
        autoScaler: true
        externalDNS: true
        certManager: true
        albIngress: true
        ebs: true
        efs: true
        cloudWatch: true
