# Deployman
A CLI for controlling ALB and two AutoScalingGroups and performing Blue/Green Deployment.

# Install
There are the following methods.

### 1. Download binary
TODO

### 2. Compile from source
You should have the latest go installed (>= 1.19).
```bash
cd ./cmd/deployman && go build
```

# Requirements
- Requires `AWS_ACCESS_KEY/AWS_SECRET_ACCESS_KEY` or `AWS_PROFILE` environment variables.
- You will need `config.json` in the same location as the deploynam The contents are as follows.

    ```json
    {
      "bundleBucket": "bundle-bucket",
      "listenerRuleArn": "arn:aws:elasticloadbalancing:xxxx:xxxx:listener-rule/app/xxxx/xxxx",
      "target": {
        "blue": {
          "autoScalingGroupName": "blue-target",
          "targetGroupArn": "arn:aws:elasticloadbalancing:xxxx:xxxx:targetgroup/blue-target/xxxx"
        },
        "green": {
          "autoScalingGroupName": "green-target",
          "targetGroupArn": "arn:aws:elasticloadbalancing:xxxx:xxxx:targetgroup/green-target/xxxx"
        }
      }
    }
    ```

# usage
Please enter `./deployman help`
