package internal

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	asg "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	alb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	albTypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
	"os"
	"time"
)

const (
	BlueTargetType  TargetType = "blue"
	GreenTargetType TargetType = "green"

	UpdateAutoScalingGroupSkipped UpdateAutoScalingGroupResult = false
	UpdateAutoScalingGroupDone    UpdateAutoScalingGroupResult = true
)

type TargetType string
type CleanupResult bool
type UpdateAutoScalingGroupResult bool

type Deployer struct {
	asg    *asg.Client
	alb    *alb.Client
	region *string
	config *Config
	logger Logger
}

type DeployTarget struct {
	Type             TargetType
	TargetGroup      *albTypes.TargetGroupTuple
	AutoScalingGroup *asgTypes.AutoScalingGroup
}

type DeployInfo struct {
	IdlingTarget  *DeployTarget
	RunningTarget *DeployTarget
}

type HealthInfo struct {
	TargetGroupArn string
	TotalCount     int
	HealthyCount   int
	UnhealthyCount int
	UnusedCount    int
	InitialCount   int
	DrainingCount  int
	Error          error
}

func NewDeployer(awsRegion *string, awsConfig *aws.Config, deployConfig *Config, logger Logger) *Deployer {
	return &Deployer{
		alb:    alb.NewFromConfig(*awsConfig),
		asg:    asg.NewFromConfig(*awsConfig),
		region: awsRegion,
		config: deployConfig,
		logger: logger,
	}
}

//TODO: Actually, it may be necessary, so I'll keep it as a comment.
//func (d *Deployer) suspendAutoScalingProcesses(ctx context.Context, autoScalingGroupName *string, scalingProcesses *[]string) error {
//	_, err := d.asg.SuspendProcesses(ctx, &asg.SuspendProcessesInput{
//		AutoScalingGroupName: autoScalingGroupName,
//		ScalingProcesses:     *scalingProcesses,
//	})
//	if err != nil {
//		return errors.WithStack(err)
//	}
//
//	return nil
//}
//
//func (d *Deployer) resumeAutoScalingProcesses(ctx context.Context, autoScalingGroupName *string, scalingProcesses *[]string) error {
//	_, err := d.asg.ResumeProcesses(ctx, &asg.ResumeProcessesInput{
//		AutoScalingGroupName: autoScalingGroupName,
//		ScalingProcesses:     *scalingProcesses,
//	})
//	if err != nil {
//		return errors.WithStack(err)
//	}
//
//	return nil
//}

func (d *Deployer) getHealthInfo(ctx context.Context, targetGroupArn *string) *HealthInfo {
	health, err := d.alb.DescribeTargetHealth(ctx, &alb.DescribeTargetHealthInput{
		TargetGroupArn: targetGroupArn,
	})
	if err != nil {
		return &HealthInfo{Error: errors.WithStack(err)}
	}

	countBy := func(state albTypes.TargetHealthStateEnum) int {
		return Count(&health.TargetHealthDescriptions, func(desc *albTypes.TargetHealthDescription) bool {
			return desc.TargetHealth.State == state
		})
	}

	return &HealthInfo{
		TargetGroupArn: *targetGroupArn,
		TotalCount:     len(health.TargetHealthDescriptions),
		HealthyCount:   countBy(albTypes.TargetHealthStateEnumHealthy),
		UnhealthyCount: countBy(albTypes.TargetHealthStateEnumUnhealthy),
		UnusedCount:    countBy(albTypes.TargetHealthStateEnumUnused),
		InitialCount:   countBy(albTypes.TargetHealthStateEnumInitial),
		DrainingCount:  countBy(albTypes.TargetHealthStateEnumDraining),
	}
}

func (d *Deployer) getDeployTarget(ctx context.Context, targetType TargetType, target *Target) (*DeployTarget, error) {
	ruleOutput, err := d.alb.DescribeRules(ctx, &alb.DescribeRulesInput{RuleArns: []string{d.config.ListenerRuleArn}})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	listenerRule := ruleOutput.Rules[0]

	var targetGroupTuple *albTypes.TargetGroupTuple
	for _, action := range listenerRule.Actions {
		if action.Type == albTypes.ActionTypeEnumForward {
			for _, tg := range action.ForwardConfig.TargetGroups {
				if tg.TargetGroupArn == &target.TargetGroupArn {
					targetGroupTuple = &tg
					break
				}
			}
		}
	}
	asgOutput, err := d.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{AutoScalingGroupNames: []string{target.AutoScalingGroupName}})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &DeployTarget{
		Type:             targetType,
		TargetGroup:      targetGroupTuple,
		AutoScalingGroup: &asgOutput.AutoScalingGroups[0],
	}, nil
}

func (d *Deployer) ShowStatus(ctx context.Context) error {
	blue, err := d.getDeployTarget(ctx, BlueTargetType, &d.config.Target.Blue)
	if err != nil {
		return err
	}
	blueHealth := d.getHealthInfo(ctx, &d.config.Target.Blue.TargetGroupArn)

	green, err := d.getDeployTarget(ctx, GreenTargetType, &d.config.Target.Green)
	if err != nil {
		return err
	}
	greenHealth := d.getHealthInfo(ctx, &d.config.Target.Green.TargetGroupArn)

	toData := func(target *DeployTarget, health *HealthInfo) *[]string {
		status := "idling"
		if *target.TargetGroup.Weight > int32(0) {
			status = "running"
		}
		return &[]string{
			fmt.Sprint(status),
			fmt.Sprint(*target.TargetGroup.Weight),
			fmt.Sprint(*target.AutoScalingGroup.AutoScalingGroupName),
			fmt.Sprint(*target.AutoScalingGroup.DesiredCapacity),
			fmt.Sprint(*target.AutoScalingGroup.MinSize),
			fmt.Sprint(*target.AutoScalingGroup.MaxSize),
			fmt.Sprint(health.TotalCount),
			fmt.Sprint(health.HealthyCount),
			fmt.Sprint(health.UnhealthyCount),
			fmt.Sprint(health.UnusedCount),
			fmt.Sprint(health.InitialCount),
			fmt.Sprint(health.DrainingCount),
		}
	}

	data := [][]string{*toData(blue, blueHealth), *toData(green, greenHealth)}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"target", "traffic(%)", "asg:name", "asg:desired", "asg:min", "asg:max", "elb:total", "elb:healthy", "elb:unhealthy", "elb:unused", "elb:initial", "elb:draining"})
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (d *Deployer) GetDeployInfo(ctx context.Context) (*DeployInfo, error) {
	blue, err := d.getDeployTarget(ctx, BlueTargetType, &d.config.Target.Blue)
	if err != nil {
		return nil, err
	}

	green, err := d.getDeployTarget(ctx, GreenTargetType, &d.config.Target.Green)
	if err != nil {
		return nil, err
	}

	if *blue.TargetGroup.Weight > int32(0) && *green.TargetGroup.Weight <= int32(0) {
		return &DeployInfo{
			IdlingTarget:  green,
			RunningTarget: blue,
		}, nil
	} else if *green.TargetGroup.Weight > int32(0) && *blue.TargetGroup.Weight <= int32(0) {
		return &DeployInfo{
			IdlingTarget:  blue,
			RunningTarget: green,
		}, nil
	} else {
		return nil, errors.Errorf("Failed to identify idling and running target groups. Either two weighted TargetGroup must be 0")
	}
}

func (d *Deployer) Deploy(ctx context.Context, swap bool, beforeCleanup bool, afterCleanup bool, swapDuration *time.Duration) error {
	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return err
	}

	d.logger.Info("Show current deployment status")
	if err := d.ShowStatus(ctx); err != nil {
		return err
	}

	if beforeCleanup {
		updateResult, err := d.CleanupIdlingTarget(ctx)
		if err != nil {
			return err
		}

		if updateResult == UpdateAutoScalingGroupDone {
			d.logger.Info("Show deployment status after cleanup")
			if err := d.ShowStatus(ctx); err != nil {
				return err
			}
		}
	}

	//TODO: Actually, it may be necessary, so I'll keep it as a comment.
	//// see: https://docs.aws.amazon.com/codedeploy/latest/userguide/integrations-aws-auto-scaling.html#integrations-aws-auto-scaling-behaviors-mixed-environment
	//scalingProcesses := []string{"AZRebalance", "AlarmNotification", "ScheduledActions", "ReplaceUnhealthy"}
	//if err := d.suspendAutoScalingProcesses(ctx, info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, &scalingProcesses); err != nil {
	//	d.logger.Warn("ScalingProcesses failed to suspend, but will continue processing", err)
	//}
	//defer func() {
	//	if err := d.resumeAutoScalingProcesses(ctx, info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, &scalingProcesses); err != nil {
	//		d.logger.Warn("ScalingProcesses failed to resume", err)
	//	}
	//}()

	d.logger.Info(fmt.Sprintf("Start deployment to %s target. Prepare instances of the same amount as %s target.", info.IdlingTarget.Type, info.RunningTarget.Type))
	if _, err := d.UpdateAutoScalingGroup(ctx,
		info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName,
		info.RunningTarget.AutoScalingGroup.DesiredCapacity,
		info.RunningTarget.AutoScalingGroup.MinSize,
		info.RunningTarget.AutoScalingGroup.MaxSize,
		false); err != nil {
		return err
	}

	d.logger.Info(fmt.Sprintf("Start healthcheck for %s target.", info.IdlingTarget.Type))
	if err := d.HealthCheck(ctx, info.IdlingTarget.TargetGroup.TargetGroupArn); err != nil {
		return err
	}

	d.logger.Info("Show deployment status after deploy")
	if err := d.ShowStatus(ctx); err != nil {
		return err
	}

	if swap {
		if err := d.SwapTraffic(ctx, swapDuration); err != nil {
			return err
		}

		d.logger.Info("Show deployment status after swap traffic")
		if err := d.ShowStatus(ctx); err != nil {
			return err
		}
	}

	if afterCleanup {
		info, err := d.GetDeployInfo(ctx)
		if err != nil {
			return err
		}

		d.logger.Info(fmt.Sprintf("Set the target MinSize to zero to clean up instances that are no longer needed. The automatic scale-in will clean up slowly. AutoScalingGroup: %s", *info.RunningTarget.AutoScalingGroup.AutoScalingGroupName))
		if _, err := d.UpdateAutoScalingGroup(ctx,
			info.RunningTarget.AutoScalingGroup.AutoScalingGroupName,
			nil,
			aws.Int32(0),
			nil,
			true); err != nil {
			return err
		}

		d.logger.Info("Show deployment status after cleanup")
		if err = d.ShowStatus(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (d *Deployer) HealthCheck(ctx context.Context, targetGroupArn *string) error {
	return NewFixedIntervalRetryer(d.config.RetryPolicy.MaxLimit, time.Duration(d.config.RetryPolicy.IntervalSeconds)*time.Second).Start(
		func(index int, interval *time.Duration) (RetryResult, error) {
			health := d.getHealthInfo(ctx, targetGroupArn)
			if health.Error != nil {
				return FinishRetry, errors.WithMessage(health.Error, "HealthCheck was aborted")
			}

			if health.HealthyCount == health.TotalCount {
				d.logger.Info("HealthCheck was completed")
				return FinishRetry, nil
			} else {
				d.logger.Info(fmt.Sprintf("HealthCheck total:%d, healthy:%d, unhealthy:%d, unused:%d, init:%d, drain:%d",
					health.TotalCount,
					health.HealthyCount,
					health.UnhealthyCount,
					health.UnusedCount,
					health.InitialCount,
					health.DrainingCount,
				))
				return ContinueRetry, nil
			}
		})
}

func (d *Deployer) UpdateTraffic(ctx context.Context, blueWeight *int32, greenWeight *int32) error {
	d.logger.Info(fmt.Sprintf("Start update traffic. blue->%d%%, green->%d%%", *blueWeight, *greenWeight))
	_, err := d.alb.ModifyRule(ctx, &alb.ModifyRuleInput{
		RuleArn: &d.config.ListenerRuleArn,
		Actions: []albTypes.Action{
			{
				Type: albTypes.ActionTypeEnumForward,
				ForwardConfig: &albTypes.ForwardActionConfig{
					TargetGroups: []albTypes.TargetGroupTuple{
						{
							TargetGroupArn: &d.config.Target.Blue.TargetGroupArn,
							Weight:         blueWeight,
						},
						{
							TargetGroupArn: &d.config.Target.Green.TargetGroupArn,
							Weight:         greenWeight,
						},
					},
					TargetGroupStickinessConfig: &albTypes.TargetGroupStickinessConfig{
						DurationSeconds: aws.Int32(10),
						Enabled:         aws.Bool(true),
					},
				},
			},
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	d.logger.Info("Update traffic is complete.")

	return nil
}

func (d *Deployer) SwapTraffic(ctx context.Context, duration *time.Duration) error {
	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return err
	}

	if *duration > 0 {
		if err := d.UpdateTraffic(ctx, aws.Int32(50), aws.Int32(50)); err != nil {
			return err
		}
		time.Sleep(*duration)
	}

	decideWeight := func(info *DeployInfo, typ TargetType) (*int32, error) {
		if info.IdlingTarget.Type == typ {
			return info.IdlingTarget.TargetGroup.Weight, nil
		} else if info.RunningTarget.Type == typ {
			return info.RunningTarget.TargetGroup.Weight, nil
		} else {
			return nil, errors.New("MissingBlueTargetGroup")
		}
	}

	blueWeight, err := decideWeight(info, BlueTargetType)
	if err != nil {
		return err
	}

	greenWeight, err := decideWeight(info, GreenTargetType)
	if err != nil {
		return err
	}

	return d.UpdateTraffic(ctx, greenWeight, blueWeight)
}

func (d *Deployer) UpdateAutoScalingGroup(
	ctx context.Context, autoScalingGroupName *string, desiredCapacity *int32, minSize *int32, maxSize *int32, fireAndForget bool) (UpdateAutoScalingGroupResult, error) {

	_, err := d.asg.UpdateAutoScalingGroup(ctx, &asg.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: autoScalingGroupName,
		DesiredCapacity:      desiredCapacity,
		MinSize:              minSize,
		MaxSize:              maxSize,
	})
	if err != nil {
		return UpdateAutoScalingGroupSkipped, errors.WithStack(err)
	}

	if fireAndForget {
		return UpdateAutoScalingGroupDone, nil
	}

	return UpdateAutoScalingGroupDone, NewFixedIntervalRetryer(d.config.RetryPolicy.MaxLimit, time.Duration(d.config.RetryPolicy.IntervalSeconds)*time.Second).Start(
		func(index int, interval *time.Duration) (RetryResult, error) {
			output, err := d.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{*autoScalingGroupName},
			})
			if err != nil {
				return FinishRetry, errors.WithStack(err)
			}

			if len(output.AutoScalingGroups[0].Instances) <= 0 {
				d.logger.Info("Cleanup is completed.")
				return FinishRetry, nil
			} else {
				d.logger.Info(fmt.Sprintf("Retry in progress, wait %dsec", int(interval.Seconds())))
				return ContinueRetry, nil
			}
		})
}

func (d *Deployer) CleanupIdlingTarget(ctx context.Context) (UpdateAutoScalingGroupResult, error) {
	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return UpdateAutoScalingGroupSkipped, err
	}
	if len(info.IdlingTarget.AutoScalingGroup.Instances) <= 0 {
		return UpdateAutoScalingGroupSkipped, nil
	}

	d.logger.Info(fmt.Sprintf("Unused idling instances detected. Start cleaning up %s AutoScalingGroup.", *info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName))
	return d.UpdateAutoScalingGroup(ctx,
		info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName,
		aws.Int32(0),
		aws.Int32(0),
		nil,
		false)
}

func (d *Deployer) MoveScheduledActions(ctx context.Context, fromAutoScalingGroupName *string, toAutoScalingGroupName *string) error {
	output, err := d.asg.DescribeScheduledActions(ctx, &asg.DescribeScheduledActionsInput{
		AutoScalingGroupName: fromAutoScalingGroupName,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	if len(output.ScheduledUpdateGroupActions) <= 0 {
		return nil
	}

	msg := fmt.Sprintf(" from:%s, to:%s", *fromAutoScalingGroupName, *toAutoScalingGroupName)
	for _, from := range output.ScheduledUpdateGroupActions {
		_, err := d.asg.PutScheduledUpdateGroupAction(ctx, &asg.PutScheduledUpdateGroupActionInput{
			AutoScalingGroupName: toAutoScalingGroupName,
			ScheduledActionName:  from.ScheduledActionName,
			DesiredCapacity:      from.DesiredCapacity,
			EndTime:              from.EndTime,
			MaxSize:              from.MaxSize,
			MinSize:              from.MinSize,
			Recurrence:           from.Recurrence,
			StartTime:            from.StartTime,
			Time:                 from.Time,
			TimeZone:             from.TimeZone,
		})
		if err != nil {
			d.logger.Warn("Failed to copy ScheduledActions, but processing continues."+msg, err)
			continue
		}
		_, err = d.asg.DeleteScheduledAction(ctx, &asg.DeleteScheduledActionInput{
			AutoScalingGroupName: from.AutoScalingGroupName,
			ScheduledActionName:  from.ScheduledActionName,
		})
		if err != nil {
			d.logger.Warn("Failed to delete ScheduledActions, but processing continues."+msg, err)
			continue
		}
	}

	return nil
}
