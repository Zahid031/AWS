################
#Md. Zahid Hasan
#################



#!/bin/bash

# Variables (Customize these)
AMI_ID="ami-084568db4383264d4"        #  AMI ID (e.g., for Ubuntu 20.04)
INSTANCE_TYPE="t2.micro"               # EC2 instance type (adjust based on your needs)
KEY_NAME="DevOps"               # Key Pair name (make sure the key is already created)
SECURITY_GROUP="sg-01a18f3dc35d37227"  
INSTANCE_NAME="Ubuntu-EC2-Instance"    
REGION="us-east-1"                   

# Create EC2 instance
aws ec2 run-instances \
  --image-id $AMI_ID \
  --instance-type $INSTANCE_TYPE \
  --key-name $KEY_NAME \
  --security-group-ids $SECURITY_GROUP \
  --region $REGION \
  --count 1 \
  --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$INSTANCE_NAME}]" \
  --output table

echo "EC2 instance creation initiated..."

