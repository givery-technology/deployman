package test

import (
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	asgTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	albTypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/givery-technology/deployman/internal"
)

type TestingState struct {
	config *internal.Config

	Bucket            *TestingBucket
	LoadBalancer      *TestingLoadBalancer
	AutoScalingGroups []TestingAutoScalingGroup
}

func NewTestingState(config *internal.Config) *TestingState {
	return &TestingState{config: config}
}

func (s *TestingState) FindAutoScalingGroup(name string) *TestingAutoScalingGroup {
	return internal.FirstOrDefault(s.AutoScalingGroups, func(g *TestingAutoScalingGroup) bool {
		return *g.AutoScalingGroupName == name
	})
}

type TestingBucket struct {
	Name                   *string
	IsVersioningEnabled    *bool
	IsAclPrivated          *bool
	IsPublicAccessDisabled *bool
	Objects                []TestingBucketObject
}

type TestingBucketObject struct {
	LastModified *time.Time
	Key          *string
	Value        []byte
	ContentType  *string
}

type TestingLoadBalancer struct {
	ListenerRuleArn        *string
	TargetGroups           []TestingTargetGroup
	ForwardActionStickness *albTypes.TargetGroupStickinessConfig
}

func (t *TestingLoadBalancer) FindTargetGroup(targetGroupArn string) *TestingTargetGroup {
	return internal.FirstOrDefault(t.TargetGroups, func(tg *TestingTargetGroup) bool {
		return *tg.TargetGroupArn == targetGroupArn
	})
}

type TestingTargetGroup struct {
	*albTypes.TargetGroupTuple
	TargetGroupName *string
	HealthStates    []albTypes.TargetHealthStateEnum
}

type TestingAutoScalingGroup struct {
	*asgTypes.AutoScalingGroup
	ScheduledActions []asgTypes.ScheduledUpdateGroupAction
}

func (s *TestingState) WithBucket(config *internal.Config) *TestingState {
	s.Bucket = &TestingBucket{
		Name:                   aws.String(config.BundleBucket),
		IsVersioningEnabled:    aws.Bool(false),
		IsAclPrivated:          aws.Bool(false),
		IsPublicAccessDisabled: aws.Bool(false),
		Objects:                []TestingBucketObject{},
	}
	return s
}

type (
	BlueWeight        int32
	BlueHealthStates  []albTypes.TargetHealthStateEnum
	GreenWeight       int32
	GreenHealthStates []albTypes.TargetHealthStateEnum
)

func (s *TestingState) WithLoadBalancer(
	blueWeight BlueWeight,
	blueStates BlueHealthStates,
	greenWeight GreenWeight,
	greenStates GreenHealthStates,
) *TestingState {
	s.LoadBalancer = &TestingLoadBalancer{
		ListenerRuleArn: aws.String(s.config.ListenerRuleArn),
		TargetGroups: []TestingTargetGroup{
			{
				TargetGroupTuple: &albTypes.TargetGroupTuple{
					TargetGroupArn: aws.String(s.config.Target.Blue.TargetGroupArn),
					Weight:         aws.Int32(int32(blueWeight)),
				},
				TargetGroupName: aws.String(string(internal.BlueTargetType)),
				HealthStates:    blueStates,
			},
			{
				TargetGroupTuple: &albTypes.TargetGroupTuple{
					TargetGroupArn: aws.String(s.config.Target.Green.TargetGroupArn),
					Weight:         aws.Int32(int32(greenWeight)),
				},
				TargetGroupName: aws.String(string(internal.GreenTargetType)),
				HealthStates:    greenStates,
			},
		},
		ForwardActionStickness: &albTypes.TargetGroupStickinessConfig{
			DurationSeconds: aws.Int32(10),
			Enabled:         aws.Bool(true),
		},
	}
	return s
}

type (
	BlueDesiredCapacity  int32
	BlueMinSize          int32
	BlueMaxSize          int32
	BlueInstanceStates   []asgTypes.LifecycleState
	GreenDesiredCapacity int32
	GreenMinSize         int32
	GreenMaxSize         int32
	GreenInstanceStates  []asgTypes.LifecycleState
)

func (s *TestingState) WithAutoScalingGroups(
	blueDesiredCapacity BlueDesiredCapacity,
	blueMinSize BlueMinSize,
	blueMaxSize BlueMaxSize,
	blueStates BlueInstanceStates,
	greenDesiredCapacity GreenDesiredCapacity,
	greenMinSize GreenMinSize,
	greenMaxSize GreenMaxSize,
	greenStates GreenInstanceStates,
) *TestingState {
	s.AutoScalingGroups = []TestingAutoScalingGroup{
		{
			AutoScalingGroup: &asgTypes.AutoScalingGroup{
				AutoScalingGroupName: aws.String(s.config.Target.Blue.AutoScalingGroupName),
				DesiredCapacity:      aws.Int32(int32(blueDesiredCapacity)),
				MinSize:              aws.Int32(int32(blueMinSize)),
				MaxSize:              aws.Int32(int32(blueMaxSize)),
				Instances: internal.Map(blueStates, func(i int, state *asgTypes.LifecycleState) *asgTypes.Instance {
					return &asgTypes.Instance{
						InstanceId:     aws.String(string(internal.BlueTargetType) + strconv.Itoa(i)),
						LifecycleState: *state,
					}
				}),
				TargetGroupARNs: []string{s.config.Target.Blue.TargetGroupArn},
			},
			ScheduledActions: []asgTypes.ScheduledUpdateGroupAction{},
		},
		{
			AutoScalingGroup: &asgTypes.AutoScalingGroup{
				AutoScalingGroupName: aws.String(s.config.Target.Green.AutoScalingGroupName),
				DesiredCapacity:      aws.Int32(int32(greenDesiredCapacity)),
				MinSize:              aws.Int32(int32(greenMinSize)),
				MaxSize:              aws.Int32(int32(greenMaxSize)),
				Instances: internal.Map(greenStates, func(i int, state *asgTypes.LifecycleState) *asgTypes.Instance {
					return &asgTypes.Instance{
						InstanceId:     aws.String(string(internal.GreenTargetType) + strconv.Itoa(i)),
						LifecycleState: *state,
					}
				}),
				TargetGroupARNs: []string{s.config.Target.Blue.TargetGroupArn},
			},
			ScheduledActions: []asgTypes.ScheduledUpdateGroupAction{},
		},
	}
	return s
}

func (s *TestingState) WithAutoScalingGroupScheduledAction(
	blueAutoScalingGroupName string,
	blueScheduledActions []asgTypes.ScheduledUpdateGroupAction,
	greenAutoScalingGroupName string,
	greenScheduledACtions []asgTypes.ScheduledUpdateGroupAction,
) *TestingState {
	s.AutoScalingGroups = []TestingAutoScalingGroup{
		{
			AutoScalingGroup: &asgTypes.AutoScalingGroup{
				AutoScalingGroupName: aws.String(blueAutoScalingGroupName),
			},
			ScheduledActions: blueScheduledActions,
		},
		{
			AutoScalingGroup: &asgTypes.AutoScalingGroup{
				AutoScalingGroupName: aws.String(greenAutoScalingGroupName),
			},
			ScheduledActions: greenScheduledACtions,
		},
	}
	return s
}
