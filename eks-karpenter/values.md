# Fix the critical issues immediately
kubectl patch configmap amazon-vpc-cni -n kube-system --patch '
data:
  branch-eni-cooldown: "60"
  enable-network-policy-controller: "true"
  enable-windows-ipam: "false"
  enable-windows-prefix-delegation: "false"
  enable-prefix-delegation: "true"
  enable-subnet-discovery: "true"
  warm-prefix-target: "1"
  warm-ip-target: "2"
  minimum-ip-target: "8"
  max-eni: "4"
'
