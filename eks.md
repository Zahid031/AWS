apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: my-eks-cluster
  region: ap-southeast-1

vpc:
  id: "vpc-0a1b2c3d4e5f67890"
  subnets:
    public:
      ap-southeast-1a:
        id: "subnet-0aaa111bbb222ccc1"
      ap-southeast-1b:
        id: "subnet-0ddd444eee555fff2"
    private:
      ap-southeast-1a:
        id: "subnet-0666aaa777bbb8883"
      ap-southeast-1b:
        id: "subnet-0999ccc000ddd1114"

# 👇 Add at least one managed node group
managedNodeGroups:
  - name: ng-public
    instanceType: t3.medium
    desiredCapacity: 2
    minSize: 1
    maxSize: 3
    ssh:
      allow: true
      publicKeyName: my-ssh-key       # existing EC2 key pair name
    subnets:
      - subnet-0aaa111bbb222ccc1
      - subnet-0ddd444eee555fff2
    tags:
      Name: eks-public-nodes

