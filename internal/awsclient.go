package internal

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	asg "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	alb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	albTypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/pkg/errors"
	"os"
	"strings"
)

type AwsClient interface {
	GetRegion() string

	ListS3BucketObjects(ctx context.Context, bucket string, prefix string) ([]s3Types.Object, error)
	HeadS3Bucket(ctx context.Context, bucket string) error
	CreateS3Bucket(ctx context.Context, bucket string, region string) error
	EnableS3BucketVersioning(ctx context.Context, bucket string) error
	MakeS3BucketAclPrivate(ctx context.Context, bucket string) error
	DisableS3BucketPublicAccess(ctx context.Context, bucket string) error
	DeleteS3BucketObject(ctx context.Context, bucket string, key string) error
	PutS3BucketObjectAsBinaryFile(ctx context.Context, bucket string, key string, file *os.File) error
	PutS3BucketObjectAsTextFile(ctx context.Context, bucket string, key string, value string) error
	GetS3BucketObject(ctx context.Context, bucket string, key string) (*s3.GetObjectOutput, error)

	GetALBListenerRule(ctx context.Context, listenerRuleArn string) (*albTypes.Rule, error)
	ModifyALBListenerRule(ctx context.Context, listenerRuleArn string, forwardAction *albTypes.ForwardActionConfig) error
	DescribeALBTargetHealth(ctx context.Context, targetGroupArn string) ([]albTypes.TargetHealthDescription, error)
	DescribeALBTargetGroup(ctx context.Context, targetGroupArn string) (*albTypes.TargetGroup, error)

	DescribeAutoScalingGroup(ctx context.Context, name string) (*asgTypes.AutoScalingGroup, error)
	UpdateAutoScalingGroup(ctx context.Context, name string, desiredCapacity *int32, minSize *int32, maxSize *int32) error
	DescribeScheduledActions(ctx context.Context, name string) ([]asgTypes.ScheduledUpdateGroupAction, error)
	PutScheduledUpdateGroupAction(ctx context.Context, name string, action *asgTypes.ScheduledUpdateGroupAction) error
	DeleteScheduledAction(ctx context.Context, autoScalingGroupName string, scheduledActionName string) error
}

type DefaultAwsClient struct {
	asg    *asg.Client
	alb    *alb.Client
	s3     *s3.Client
	region string
}

func NewDefaultAwsClient(ctx context.Context) (*DefaultAwsClient, error) {
	region := GetEnv("AWS_REGION", "us-east-1")
	config, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(region))
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &DefaultAwsClient{
		asg:    asg.NewFromConfig(config),
		alb:    alb.NewFromConfig(config),
		s3:     s3.NewFromConfig(config),
		region: region,
	}, nil
}

func (c *DefaultAwsClient) GetRegion() string {
	return c.region
}

func (c *DefaultAwsClient) ListS3BucketObjects(ctx context.Context, bucket string, prefix string) ([]s3Types.Object, error) {
	output, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return output.Contents, nil
}

func (c *DefaultAwsClient) HeadS3Bucket(ctx context.Context, bucket string) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	if err != nil {
		return err
	}

	return nil
}

func (c *DefaultAwsClient) CreateS3Bucket(ctx context.Context, bucket string, region string) error {
	_, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: &bucket,
		CreateBucketConfiguration: &s3Types.CreateBucketConfiguration{
			LocationConstraint: s3Types.BucketLocationConstraint(region),
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) EnableS3BucketVersioning(ctx context.Context, bucket string) error {
	_, err := c.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: &bucket,
		VersioningConfiguration: &s3Types.VersioningConfiguration{
			Status: s3Types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) MakeS3BucketAclPrivate(ctx context.Context, bucket string) error {
	_, err := c.s3.PutBucketAcl(ctx, &s3.PutBucketAclInput{
		Bucket: &bucket,
		ACL:    s3Types.BucketCannedACLPrivate,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) DisableS3BucketPublicAccess(ctx context.Context, bucket string) error {
	_, err := c.s3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: &bucket,
		PublicAccessBlockConfiguration: &s3Types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       true,
			BlockPublicPolicy:     true,
			IgnorePublicAcls:      true,
			RestrictPublicBuckets: true,
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) DeleteS3BucketObject(ctx context.Context, bucket string, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) PutS3BucketObjectAsBinaryFile(ctx context.Context, bucket string, key string, file *os.File) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   file,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) PutS3BucketObjectAsTextFile(ctx context.Context, bucket string, key string, value string) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		ContentType: aws.String("text/plain"),
		Body:        strings.NewReader(value),
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) GetS3BucketObject(ctx context.Context, bucket string, key string) (*s3.GetObjectOutput, error) {
	output, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return output, nil
}

func (c *DefaultAwsClient) GetALBListenerRule(ctx context.Context, listenerRuleArn string) (*albTypes.Rule, error) {
	output, err := c.alb.DescribeRules(ctx, &alb.DescribeRulesInput{
		RuleArns: []string{listenerRuleArn}})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &output.Rules[0], nil
}

func (c *DefaultAwsClient) DescribeALBTargetHealth(ctx context.Context, targetGroupArn string) ([]albTypes.TargetHealthDescription, error) {
	output, err := c.alb.DescribeTargetHealth(ctx, &alb.DescribeTargetHealthInput{
		TargetGroupArn: &targetGroupArn,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return output.TargetHealthDescriptions, nil
}

func (c *DefaultAwsClient) DescribeAutoScalingGroup(ctx context.Context, name string) (*asgTypes.AutoScalingGroup, error) {
	output, err := c.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{name},
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &output.AutoScalingGroups[0], err
}

func (c *DefaultAwsClient) DescribeALBTargetGroup(ctx context.Context, targetGroupArn string) (*albTypes.TargetGroup, error) {
	output, err := c.alb.DescribeTargetGroups(ctx, &alb.DescribeTargetGroupsInput{
		TargetGroupArns: []string{targetGroupArn},
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &output.TargetGroups[0], nil
}

func (c *DefaultAwsClient) ModifyALBListenerRule(ctx context.Context, listenerRuleArn string, forwardAction *albTypes.ForwardActionConfig) error {
	_, err := c.alb.ModifyRule(ctx, &alb.ModifyRuleInput{
		RuleArn: &listenerRuleArn,
		Actions: []albTypes.Action{
			{
				Type:          albTypes.ActionTypeEnumForward,
				ForwardConfig: forwardAction,
			},
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) UpdateAutoScalingGroup(ctx context.Context, name string, desiredCapacity *int32, minSize *int32, maxSize *int32) error {
	input := &asg.UpdateAutoScalingGroupInput{AutoScalingGroupName: &name}
	if desiredCapacity != nil && *desiredCapacity >= 0 {
		input.DesiredCapacity = desiredCapacity
	}
	if minSize != nil && *minSize >= 0 {
		input.MinSize = minSize
	}
	if maxSize != nil && *maxSize >= 0 {
		input.MaxSize = maxSize
	}
	_, err := c.asg.UpdateAutoScalingGroup(ctx, input)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) DescribeScheduledActions(ctx context.Context, name string) ([]asgTypes.ScheduledUpdateGroupAction, error) {
	output, err := c.asg.DescribeScheduledActions(ctx, &asg.DescribeScheduledActionsInput{
		AutoScalingGroupName: &name,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return output.ScheduledUpdateGroupActions, err
}

func (c *DefaultAwsClient) PutScheduledUpdateGroupAction(ctx context.Context, name string, action *asgTypes.ScheduledUpdateGroupAction) error {
	_, err := c.asg.PutScheduledUpdateGroupAction(ctx, &asg.PutScheduledUpdateGroupActionInput{
		AutoScalingGroupName: &name,
		ScheduledActionName:  action.ScheduledActionName,
		DesiredCapacity:      action.DesiredCapacity,
		EndTime:              action.EndTime,
		MaxSize:              action.MaxSize,
		MinSize:              action.MinSize,
		Recurrence:           action.Recurrence,
		StartTime:            action.StartTime,
		Time:                 action.Time,
		TimeZone:             action.TimeZone,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (c *DefaultAwsClient) DeleteScheduledAction(ctx context.Context, autoScalingGroupName string, scheduledActionName string) error {
	_, err := c.asg.DeleteScheduledAction(ctx, &asg.DeleteScheduledActionInput{
		AutoScalingGroupName: &autoScalingGroupName,
		ScheduledActionName:  &scheduledActionName,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
