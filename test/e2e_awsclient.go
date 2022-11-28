package test

import (
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	asgTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	albTypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	ssmTypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/givery-technology/deployman/internal"
	"github.com/pkg/errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type MockAwsClient struct {
	State *TestingState
}

func (c *MockAwsClient) WithState(state *TestingState) *MockAwsClient {
	c.State = state
	return c
}

func (c *MockAwsClient) Region() string {
	return "us-east-1"
}

func (c *MockAwsClient) ListS3BucketObjects(_ context.Context, bucket string, prefix string) ([]s3Types.Object, error) {
	var objects []TestingBucketObject
	if c.State.Bucket != nil && *c.State.Bucket.Name == bucket {
		objects = internal.Filter(c.State.Bucket.Objects, func(o *TestingBucketObject) bool {
			return strings.Contains(*o.Key, prefix)
		})
	}
	return internal.Map(objects, func(_ int, o *TestingBucketObject) *s3Types.Object {
		return &s3Types.Object{
			Key:          aws.String(*o.Key),
			LastModified: o.LastModified,
		}
	}), nil
}

func (c *MockAwsClient) HeadS3Bucket(_ context.Context, bucket string) error {
	if c.State.Bucket == nil {
		return &s3Types.NotFound{Message: aws.String("BucketNotFound")}
	}
	return nil
}

func (c *MockAwsClient) CreateS3Bucket(_ context.Context, bucket string, _ string) error {
	if c.State.Bucket != nil {
		return errors.Errorf("Bucket already exists. bucket:%s", bucket)
	}
	c.State.Bucket = &TestingBucket{
		Name:                   aws.String(bucket),
		IsVersioningEnabled:    aws.Bool(false),
		IsAclPrivated:          aws.Bool(false),
		IsPublicAccessDisabled: aws.Bool(false),
		Objects:                []TestingBucketObject{},
	}
	return nil
}

func (c *MockAwsClient) EnableS3BucketVersioning(_ context.Context, bucket string) error {
	if c.State.Bucket == nil {
		return errors.Errorf("Bucket not found. bucket:%s", bucket)
	}
	c.State.Bucket.IsVersioningEnabled = aws.Bool(true)
	return nil
}

func (c *MockAwsClient) MakeS3BucketAclPrivate(_ context.Context, bucket string) error {
	if c.State.Bucket == nil {
		return errors.Errorf("Bucket not found. bucket:%s", bucket)
	}
	c.State.Bucket.IsAclPrivated = aws.Bool(true)
	return nil
}

func (c *MockAwsClient) DisableS3BucketPublicAccess(_ context.Context, bucket string) error {
	if c.State.Bucket == nil {
		return errors.Errorf("Bucket not found. bucket:%s", bucket)
	}
	c.State.Bucket.IsPublicAccessDisabled = aws.Bool(true)
	return nil
}

func (c *MockAwsClient) DeleteS3BucketObject(_ context.Context, bucket string, key string) error {
	var objects []TestingBucketObject
	if c.State.Bucket != nil && *c.State.Bucket.Name == bucket {
		objects = internal.Filter(c.State.Bucket.Objects, func(o *TestingBucketObject) bool {
			return strings.Contains(*o.Key, key)
		})
	}
	c.State.Bucket.Objects = internal.FastDelete(objects, func(o *TestingBucketObject) bool {
		return *o.Key == key
	})
	return nil
}

func (c *MockAwsClient) PutS3BucketObjectAsBinaryFile(_ context.Context, bucket string, key string, file *os.File) error {
	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	buf, err := io.ReadAll(file)
	if err != nil {
		return errors.WithStack(err)
	}
	if c.State.Bucket != nil {
		c.State.Bucket.Objects = append(c.State.Bucket.Objects, TestingBucketObject{
			LastModified: aws.Time(time.Now()),
			Key:          aws.String(key),
			Value:        buf,
		})
	}
	return nil
}

func (c *MockAwsClient) PutS3BucketObjectAsTextFile(_ context.Context, bucket string, key string, value string) error {
	if c.State.Bucket != nil {
		c.State.Bucket.Objects = append(c.State.Bucket.Objects, TestingBucketObject{
			LastModified: aws.Time(time.Now()),
			Key:          aws.String(key),
			Value:        []byte(value),
			ContentType:  aws.String("text/plain"),
		})
	}
	return nil
}

func (c *MockAwsClient) GetS3BucketObject(_ context.Context, bucket string, key string) (*s3.GetObjectOutput, error) {
	if c.State.Bucket != nil && *c.State.Bucket.Name == bucket {
		object := internal.FirstOrNil(c.State.Bucket.Objects, func(o *TestingBucketObject) bool {
			return strings.Contains(*o.Key, key)
		})
		if object != nil {
			output := &s3.GetObjectOutput{
				LastModified: aws.Time(time.Now()),
				Body:         io.NopCloser(bytes.NewReader(object.Value)),
			}
			return output, nil
		}
	}
	return nil, errors.Errorf("Bucket object not found. bucket:%s, key:%s", bucket, key)
}

func (c *MockAwsClient) GetALBListenerRule(_ context.Context, listenerRuleArn string) (*albTypes.Rule, error) {
	if *c.State.LoadBalancer.ListenerRuleArn != listenerRuleArn {
		return nil, errors.Errorf("ListenerRule not found. listenerRuleArn:%s", listenerRuleArn)
	}
	targetGroups := internal.Map(c.State.LoadBalancer.TargetGroups, func(_ int, tg *TestingTargetGroup) *albTypes.TargetGroupTuple {
		return &albTypes.TargetGroupTuple{
			TargetGroupArn: tg.TargetGroupArn,
			Weight:         tg.Weight,
		}
	})
	return &albTypes.Rule{
		Actions: []albTypes.Action{
			{
				Type: albTypes.ActionTypeEnumForward,
				ForwardConfig: &albTypes.ForwardActionConfig{
					TargetGroupStickinessConfig: c.State.LoadBalancer.ForwardActionStickness,
					TargetGroups:                targetGroups,
				},
			},
		},
		IsDefault: true,
		RuleArn:   c.State.LoadBalancer.ListenerRuleArn,
	}, nil
}

func (c *MockAwsClient) DescribeALBTargetHealth(_ context.Context, targetGroupArn string) ([]albTypes.TargetHealthDescription, error) {
	targetGroup := internal.FirstOrNil(c.State.LoadBalancer.TargetGroups, func(tg *TestingTargetGroup) bool {
		return *tg.TargetGroupArn == targetGroupArn
	})
	if targetGroup == nil {
		return nil, errors.Errorf("TargetHealth not found. targetGruopArn:%s", targetGroupArn)
	}
	return internal.Map(targetGroup.HealthStates, func(_ int, state *albTypes.TargetHealthStateEnum) *albTypes.TargetHealthDescription {
		return &albTypes.TargetHealthDescription{
			TargetHealth: &albTypes.TargetHealth{
				State: *state,
			},
		}
	}), nil
}

func (c *MockAwsClient) DescribeAutoScalingGroup(_ context.Context, name string) (*asgTypes.AutoScalingGroup, error) {
	autoScalingGroup := internal.FirstOrNil(c.State.AutoScalingGroups, func(g *TestingAutoScalingGroup) bool {
		return *g.AutoScalingGroupName == name
	})
	if autoScalingGroup == nil {
		return nil, errors.Errorf("AutoScalingGroup not found. name:%s", name)
	}
	return autoScalingGroup.AutoScalingGroup, nil
}

func (c *MockAwsClient) DescribeALBTargetGroup(_ context.Context, targetGroupArn string) (*albTypes.TargetGroup, error) {
	targetGroup := internal.FirstOrNil(c.State.LoadBalancer.TargetGroups, func(tg *TestingTargetGroup) bool {
		return *tg.TargetGroupArn == targetGroupArn
	})
	if targetGroup == nil {
		return nil, errors.Errorf("TargetGroup not found. listenerRuleArn:%s", targetGroupArn)
	}
	return &albTypes.TargetGroup{
		TargetGroupName: targetGroup.TargetGroupName,
	}, nil
}

func (c *MockAwsClient) ModifyALBListenerRule(_ context.Context, listenerRuleArn string, forwardAction *albTypes.ForwardActionConfig) error {
	if *c.State.LoadBalancer.ListenerRuleArn != listenerRuleArn {
		return errors.Errorf("ListenerRule not found. listenerRuleArn:%s", listenerRuleArn)
	}
	for x := range forwardAction.TargetGroups {
		from := &forwardAction.TargetGroups[x]
		for y := range c.State.LoadBalancer.TargetGroups {
			to := &c.State.LoadBalancer.TargetGroups[y]
			if *from.TargetGroupArn == *to.TargetGroupArn {
				*to.TargetGroupTuple = *from
			}
		}
	}
	return nil
}

func (c *MockAwsClient) UpdateAutoScalingGroup(_ context.Context, name string, desiredCapacity *int32, minSize *int32, maxSize *int32) error {
	for i := range c.State.AutoScalingGroups {
		autoScalingGroup := &c.State.AutoScalingGroups[i]
		if *autoScalingGroup.AutoScalingGroupName == name {
			if desiredCapacity != nil {
				autoScalingGroup.DesiredCapacity = desiredCapacity
			}
			if minSize != nil {
				autoScalingGroup.MinSize = minSize
				if *minSize > 0 {
					for i := 0; i < int(*minSize); i++ {
						autoScalingGroup.Instances = append(autoScalingGroup.Instances, asgTypes.Instance{
							InstanceId:     aws.String("ins" + strconv.Itoa(i)),
							LifecycleState: asgTypes.LifecycleStateInService,
						})
					}
				}
			}
			if maxSize != nil {
				autoScalingGroup.MaxSize = maxSize
			}
		}
	}
	return nil
}

func (c *MockAwsClient) DescribeScheduledActions(_ context.Context, name string) ([]asgTypes.ScheduledUpdateGroupAction, error) {
	autoScalingGroup := internal.FirstOrNil(c.State.AutoScalingGroups, func(g *TestingAutoScalingGroup) bool {
		return *g.AutoScalingGroupName == name
	})
	if autoScalingGroup == nil {
		return nil, errors.Errorf("AutoScalingGroup not found. name:%s", name)
	}
	return autoScalingGroup.ScheduledActions, nil
}

func (c *MockAwsClient) PutScheduledUpdateGroupAction(_ context.Context, name string, action *asgTypes.ScheduledUpdateGroupAction) error {
	for i := range c.State.AutoScalingGroups {
		autoScalingGroup := &c.State.AutoScalingGroups[i]
		if *autoScalingGroup.AutoScalingGroupName == name {
			autoScalingGroup.ScheduledActions = append(autoScalingGroup.ScheduledActions, *action)
		}
	}
	return nil
}

func (c *MockAwsClient) DeleteScheduledAction(_ context.Context, autoScalingGroupName string, scheduledActionName string) error {
	for x := range c.State.AutoScalingGroups {
		autoScalingGroup := &c.State.AutoScalingGroups[x]
		if *autoScalingGroup.AutoScalingGroupName == autoScalingGroupName {
			autoScalingGroup.ScheduledActions = internal.FastDelete(autoScalingGroup.ScheduledActions, func(a *asgTypes.ScheduledUpdateGroupAction) bool {
				return *a.ScheduledActionName == scheduledActionName
			})
		}
	}
	return nil
}

func (c *MockAwsClient) GetSSMParameter(_ context.Context, name string, withDecription bool) (*ssmTypes.Parameter, error) {
	return &ssmTypes.Parameter{
		LastModifiedDate: aws.Time(time.Now()),
		Name:             aws.String("test/parameter/001"),
		Type:             ssmTypes.ParameterTypeString,
		Value:            aws.String("TestParameterValue001"),
		Version:          0,
	}, nil
}
