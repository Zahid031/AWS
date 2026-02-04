apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: my-eks-cluster
  region: us-east-1  # Change to your region
  version: "1.28"  # Change to your desired Kubernetes version

# VPC Configuration - using your existing VPC
vpc:
  id: "vpc-xxxxxxxxxxxxxxxxx"  # Replace with your VPC ID
  subnets:
    private:
      us-east-1a:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your private subnet ID in AZ 1
      us-east-1b:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your private subnet ID in AZ 2
      us-east-1c:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your private subnet ID in AZ 3
    public:
      us-east-1a:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your public subnet ID in AZ 1
      us-east-1b:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your public subnet ID in AZ 2
      us-east-1c:
        id: "subnet-xxxxxxxxxxxxxxxxx"  # Replace with your public subnet ID in AZ 3

# IAM Configuration
iam:
  withOIDC: true  # Enable OIDC provider for IAM roles for service accounts

# CloudWatch Logging
cloudWatch:
  clusterLogging:
    enableTypes:
      - api
      - audit
      - authenticator
      - controllerManager
      - scheduler

# NO NODE GROUPS - Just the control plane
