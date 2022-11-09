package test

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	asgTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	albTypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/givery-technology/deployman/internal"
	"github.com/givery-technology/deployman/test/assert"
	"os"
	"testing"
	"time"
)

const testdata = "./data"

func TestE2E(t *testing.T) {
	ctx := context.TODO()
	logger := &internal.DefaultLogger{Verbose: true}
	config, err := internal.NewConfig(testdata + "/default.json")
	if err != nil {
		t.Fatal("InvalidConfig", err)
	}

	t.Run("BundleRegister#IfNoBucket", func(t *testing.T) {
		state := NewTestingState(config)
		bundler := internal.NewBundler(config, NewMockAwsClient(state), logger)
		assert.Success(t, bundler.Register(ctx, testdata+"/bundle.zip", "bundle.zip"))
		assert.True(t, len(*state.Bucket.Name) >= 0)
		assert.True(t, *state.Bucket.IsPublicAccessDisabled)
		assert.True(t, *state.Bucket.IsVersioningEnabled)
		assert.True(t, *state.Bucket.IsAclPrivated)
		assert.True(t, len(state.Bucket.Objects) > 0)
	})

	t.Run("BundleRegister#101Cycle", func(t *testing.T) {
		state := NewTestingState(config)
		bundler := internal.NewBundler(config, NewMockAwsClient(state), logger)
		for i := 0; i < 101; i++ {
			assert.Success(t, bundler.Register(ctx, testdata+"/bundle.zip", "bundle.zip"))
		}
		assert.Equal(t, len(state.Bucket.Objects), internal.MaxKeepBundles)
	})

	t.Run("BundleRegister#ActivationAndDownload", func(t *testing.T) {
		state := NewTestingState(config)
		bundler := internal.NewBundler(config, NewMockAwsClient(state), logger)
		bundleName := "bundle.zip"

		assert.Success(t, bundler.Register(ctx, testdata+"/bundle.zip", bundleName))

		assert.True(t, len(*state.Bucket.Name) >= 0)
		assert.True(t, *state.Bucket.IsPublicAccessDisabled)
		assert.True(t, *state.Bucket.IsVersioningEnabled)
		assert.True(t, *state.Bucket.IsAclPrivated)
		assert.True(t, len(state.Bucket.Objects) > 0)

		assert.Success(t, bundler.Activate(ctx, internal.BlueTargetType, bundleName))
		assert.Success(t, bundler.Activate(ctx, internal.GreenTargetType, bundleName))
		assert.Success(t, bundler.ListBundles(ctx))

		t.Cleanup(func() {
			_ = os.Remove(bundleName) // Measures to clean up downloaded files later
		})
		assert.Success(t, bundler.Download(ctx, internal.BlueTargetType))
	})

	t.Run("EC2Deploy", func(t *testing.T) {
		state := NewTestingState(config).
			WithLoadBalancer(
				BlueWeight(0), BlueHealthStates{albTypes.TargetHealthStateEnumHealthy},
				GreenWeight(100), GreenHealthStates{albTypes.TargetHealthStateEnumHealthy},
			).
			WithAutoScalingGroups(
				BlueDesiredCapacity(0), BlueMinSize(0), BlueMaxSize(2), BlueInstanceStates{},
				GreenDesiredCapacity(1), GreenMinSize(1), GreenMaxSize(2), GreenInstanceStates{asgTypes.LifecycleStateInService},
			)
		deployer := internal.NewDeployer(config, NewMockAwsClient(state), logger)

		assert.Success(t, deployer.Deploy(ctx, true, true, true, aws.Duration(time.Duration(1))))

		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Blue.TargetGroupArn).Weight, int32(100))
		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Green.TargetGroupArn).Weight, int32(0))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).DesiredCapacity, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).DesiredCapacity, int32(1))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MinSize, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MinSize, int32(0))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MaxSize, int32(2))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MaxSize, int32(2))
	})

	t.Run("EC2Deploy", func(t *testing.T) {
		state := NewTestingState(config).
			WithLoadBalancer(
				BlueWeight(0), BlueHealthStates{albTypes.TargetHealthStateEnumHealthy},
				GreenWeight(100), GreenHealthStates{albTypes.TargetHealthStateEnumHealthy},
			).
			WithAutoScalingGroups(
				BlueDesiredCapacity(0), BlueMinSize(0), BlueMaxSize(2), BlueInstanceStates{},
				GreenDesiredCapacity(1), GreenMinSize(1), GreenMaxSize(2), GreenInstanceStates{asgTypes.LifecycleStateInService},
			)
		deployer := internal.NewDeployer(config, NewMockAwsClient(state), logger)

		assert.Success(t, deployer.Deploy(ctx, true, true, true, aws.Duration(time.Duration(1))))

		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Blue.TargetGroupArn).Weight, int32(100))
		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Green.TargetGroupArn).Weight, int32(0))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).DesiredCapacity, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).DesiredCapacity, int32(1))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MinSize, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MinSize, int32(0))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MaxSize, int32(2))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MaxSize, int32(2))
	})

	t.Run("EC2Rollback", func(t *testing.T) {
		state := NewTestingState(config).
			WithLoadBalancer(
				BlueWeight(0), BlueHealthStates{albTypes.TargetHealthStateEnumHealthy},
				GreenWeight(100), GreenHealthStates{albTypes.TargetHealthStateEnumHealthy},
			).
			WithAutoScalingGroups(
				BlueDesiredCapacity(0), BlueMinSize(0), BlueMaxSize(2), BlueInstanceStates{},
				GreenDesiredCapacity(1), GreenMinSize(1), GreenMaxSize(2), GreenInstanceStates{asgTypes.LifecycleStateInService},
			)
		deployer := internal.NewDeployer(config, NewMockAwsClient(state), logger)

		assert.Success(t, deployer.Deploy(ctx, true, false, false, aws.Duration(time.Duration(1))))

		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Blue.TargetGroupArn).Weight, int32(100))
		assert.Equal(t, *state.LoadBalancer.GetTargetGroup(config.Target.Green.TargetGroupArn).Weight, int32(0))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).DesiredCapacity, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).DesiredCapacity, int32(1))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MinSize, int32(1))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MinSize, int32(1))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MaxSize, int32(2))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MaxSize, int32(2))
	})

	t.Run("EC2AutoScalingGroupByTarget", func(t *testing.T) {
		state := NewTestingState(config).
			WithLoadBalancer(
				BlueWeight(0), BlueHealthStates{albTypes.TargetHealthStateEnumHealthy},
				GreenWeight(100), GreenHealthStates{albTypes.TargetHealthStateEnumHealthy},
			).
			WithAutoScalingGroups(
				BlueDesiredCapacity(0), BlueMinSize(0), BlueMaxSize(2), BlueInstanceStates{},
				GreenDesiredCapacity(1), GreenMinSize(1), GreenMaxSize(2), GreenInstanceStates{asgTypes.LifecycleStateInService},
			)
		deployer := internal.NewDeployer(config, NewMockAwsClient(state), logger)

		assert.Success(t, deployer.UpdateAutoScalingGroupByTarget(ctx, internal.BlueTargetType, aws.Int32(11), aws.Int32(12), aws.Int32(13)))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).DesiredCapacity, int32(11))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MinSize, int32(12))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Blue.AutoScalingGroupName).MaxSize, int32(13))

		assert.Success(t, deployer.UpdateAutoScalingGroupByTarget(ctx, internal.GreenTargetType, aws.Int32(21), aws.Int32(22), aws.Int32(23)))

		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).DesiredCapacity, int32(21))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MinSize, int32(22))
		assert.Equal(t, *state.GetAutoScalingGroup(config.Target.Green.AutoScalingGroupName).MaxSize, int32(23))
	})

	t.Run("EC2MoveScheduledAction", func(t *testing.T) {
		now := time.Now()
		state := NewTestingState(config).WithAutoScalingGroupScheduledAction(
			"fromASG", []asgTypes.ScheduledUpdateGroupAction{
				{
					AutoScalingGroupName: aws.String("fromASG"),
					DesiredCapacity:      aws.Int32(1),
					MinSize:              aws.Int32(2),
					MaxSize:              aws.Int32(3),
					Recurrence:           aws.String("1/* * * * *"),
					ScheduledActionARN:   aws.String("fromARN"),
					ScheduledActionName:  aws.String("fromAction"),
					StartTime:            aws.Time(now),
					EndTime:              aws.Time(now.Add(24 + time.Hour)),
					TimeZone:             aws.String(config.TimeZone.Location),
				},
			},
			"toASG", []asgTypes.ScheduledUpdateGroupAction{},
		)
		deployer := internal.NewDeployer(config, NewMockAwsClient(state), logger)

		assert.Success(t, deployer.MoveScheduledActions(ctx, "fromASG", "toASG"))

		assert.Equal(t, len(state.GetAutoScalingGroup("fromASG").ScheduledActions), 0)
		assert.Equal(t, len(state.GetAutoScalingGroup("toASG").ScheduledActions), 1)
	})
}
