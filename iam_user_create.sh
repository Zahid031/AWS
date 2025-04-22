################
#Md. Zahid Hasan
################

#!/bin/bash

# AWS CLI script to create an IAM user with AdministratorAccess

USER_NAME="Your Custom Name"

echo "Creating IAM user: $USER_NAME"
aws iam create-user --user-name $USER_NAME

echo "Attaching AdministratorAccess policy or if you want to add custom policy just add them in policy"
aws iam attach-user-policy \
  --user-name $USER_NAME \
  --policy-arn arn:aws:iam::aws:policy/AdministratorAccess

echo "Creating access keys (DO NOT commit these to GitHub!)"
aws iam create-access-key --user-name $USER_NAME

echo "Creating console login profile (temporary password)"
aws iam create-login-profile \
  --user-name $USER_NAME \
  --password 'String Password' \
  --password-reset-required

echo "User $USER_NAME setup complete with custom policy access."

