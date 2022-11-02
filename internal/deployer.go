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
	"strconv"
	"strings"
	"time"
)

const (
	BlueTargetType  TargetType = "blue"
	GreenTargetType TargetType = "green"
)

var CancellationError = errors.New("CancellationError")

type TargetType string

type Deployer struct {
	asg    *asg.Client
	alb    *alb.Client
	region string
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
}

func NewDeployer(awsRegion string, awsConfig *aws.Config, deployConfig *Config, logger Logger) *Deployer {
	return &Deployer{
		alb:    alb.NewFromConfig(*awsConfig),
		asg:    asg.NewFromConfig(*awsConfig),
		region: awsRegion,
		config: deployConfig,
		logger: logger,
	}
}

// TODO: Actually, it may be necessary, so I'll keep it as a comment.
//func (d *Deployer) suspendAutoScalingProcesses(ctx context.Context, autoScalingGroupName string, scalingProcesses []string) error {
//	_, err := d.asg.SuspendProcesses(ctx, &asg.SuspendProcessesInput{
//		AutoScalingGroupName: &autoScalingGroupName,
//		ScalingProcesses:     scalingProcesses,
//	})
//	if err != nil {
//		return errors.WithStack(err)
//	}
//
//	return nil
//}
//
//func (d *Deployer) resumeAutoScalingProcesses(ctx context.Context, autoScalingGroupName string, scalingProcesses []string) error {
//	_, err := d.asg.ResumeProcesses(ctx, &asg.ResumeProcessesInput{
//		AutoScalingGroupName: &autoScalingGroupName,
//		ScalingProcesses:     scalingProcesses,
//	})
//	if err != nil {
//		return errors.WithStack(err)
//	}
//
//	return nil
//}

func (d *Deployer) getListenerRule(ctx context.Context) (*albTypes.Rule, error) {
	output, err := d.alb.DescribeRules(ctx, &alb.DescribeRulesInput{RuleArns: []string{d.config.ListenerRuleArn}})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return &output.Rules[0], nil
}

func (d *Deployer) getHealthInfo(ctx context.Context, targetGroupArn string) (*HealthInfo, error) {
	health, err := d.alb.DescribeTargetHealth(ctx, &alb.DescribeTargetHealthInput{
		TargetGroupArn: &targetGroupArn,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	countBy := func(state albTypes.TargetHealthStateEnum) int {
		return Count(&health.TargetHealthDescriptions, func(desc *albTypes.TargetHealthDescription) bool {
			return desc.TargetHealth.State == state
		})
	}

	return &HealthInfo{
		TargetGroupArn: targetGroupArn,
		TotalCount:     len(health.TargetHealthDescriptions),
		HealthyCount:   countBy(albTypes.TargetHealthStateEnumHealthy),
		UnhealthyCount: countBy(albTypes.TargetHealthStateEnumUnhealthy),
		UnusedCount:    countBy(albTypes.TargetHealthStateEnumUnused),
		InitialCount:   countBy(albTypes.TargetHealthStateEnumInitial),
		DrainingCount:  countBy(albTypes.TargetHealthStateEnumDraining),
	}, nil
}

func (d *Deployer) lifecycleStateToString(autoScalingGroup *asgTypes.AutoScalingGroup) string {
	lifecycleStates := map[asgTypes.LifecycleState]int{}
	for _, ins := range autoScalingGroup.Instances {
		if _, ok := lifecycleStates[ins.LifecycleState]; ok {
			lifecycleStates[ins.LifecycleState]++
		} else {
			lifecycleStates[ins.LifecycleState] = 1
		}
	}
	var states []string
	for state, count := range lifecycleStates {
		states = append(states, fmt.Sprintf("%s:%d", string(state), count))
	}
	result := ""
	if len(states) > 0 {
		result = strings.Join(states, ",")
	}
	return result
}

func (d *Deployer) GetDeployTarget(
	ctx context.Context, rule *albTypes.Rule, targetType TargetType) (*DeployTarget, error) {

	var target *Target
	if targetType == BlueTargetType {
		target = d.config.Target.Blue
	} else if targetType == GreenTargetType {
		target = d.config.Target.Green
	} else {
		return nil, errors.Errorf("TargetType:'%s' does not exist.", string(targetType))
	}

	var targetGroupTuple *albTypes.TargetGroupTuple
	for _, action := range rule.Actions {
		if action.Type == albTypes.ActionTypeEnumForward {
			for _, tg := range action.ForwardConfig.TargetGroups {
				if *tg.TargetGroupArn == target.TargetGroupArn {
					targetGroupTuple = &tg
					break
				}
			}
		}
	}

	output, err := d.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{target.AutoScalingGroupName},
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if targetGroupTuple == nil {
		return nil, errors.Errorf("TargetGroup does not exist. TargetType:'%s'", string(targetType))
	}

	return &DeployTarget{
		Type:             targetType,
		TargetGroup:      targetGroupTuple,
		AutoScalingGroup: &output.AutoScalingGroups[0],
	}, nil
}

func (d *Deployer) GetDeployInfo(ctx context.Context) (*DeployInfo, error) {
	rule, err := d.getListenerRule(ctx)
	if err != nil {
		return nil, err
	}

	blue, err := d.GetDeployTarget(ctx, rule, BlueTargetType)
	if err != nil {
		return nil, err
	}

	green, err := d.GetDeployTarget(ctx, rule, GreenTargetType)
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
		return nil, errors.Errorf(
			"Failed to identify idling and running target groups. Either two weighted TargetGroup must be 0")
	}
}

func (d *Deployer) ShowStatus(ctx context.Context) error {
	//DeployTarget
	rule, err := d.getListenerRule(ctx)
	if err != nil {
		return err
	}
	blueTarget, err := d.GetDeployTarget(ctx, rule, BlueTargetType)
	if err != nil {
		return err
	}
	greenTarget, err := d.GetDeployTarget(ctx, rule, GreenTargetType)
	if err != nil {
		return err
	}

	//TargetGroupName
	getTargetGroupName := func(targetGroupArn string) (string, error) {
		output, err := d.alb.DescribeTargetGroups(ctx, &alb.DescribeTargetGroupsInput{
			TargetGroupArns: []string{targetGroupArn},
		})
		if err != nil {
			return "", errors.WithStack(err)
		}
		return *output.TargetGroups[0].TargetGroupName, nil
	}
	blueTGName, err := getTargetGroupName(d.config.Target.Blue.TargetGroupArn)
	if err != nil {
		return err
	}
	greenTGName, err := getTargetGroupName(d.config.Target.Green.TargetGroupArn)
	if err != nil {
		return err
	}

	//HealthInfo
	blueHealth, err := d.getHealthInfo(ctx, d.config.Target.Blue.TargetGroupArn)
	if err != nil {
		return err
	}
	greenHealth, err := d.getHealthInfo(ctx, d.config.Target.Green.TargetGroupArn)
	if err != nil {
		return err
	}

	toData := func(target *DeployTarget, targetGroupName string, health *HealthInfo) []string {
		return []string{
			string(target.Type),
			strconv.Itoa(int(*target.TargetGroup.Weight)),
			*target.AutoScalingGroup.AutoScalingGroupName,
			strconv.Itoa(int(*target.AutoScalingGroup.DesiredCapacity)),
			strconv.Itoa(int(*target.AutoScalingGroup.MinSize)),
			strconv.Itoa(int(*target.AutoScalingGroup.MaxSize)),
			d.lifecycleStateToString(target.AutoScalingGroup),
			targetGroupName,
			strconv.Itoa(health.TotalCount),
			strconv.Itoa(health.HealthyCount),
			strconv.Itoa(health.UnhealthyCount),
			strconv.Itoa(health.UnusedCount),
			strconv.Itoa(health.InitialCount),
			strconv.Itoa(health.DrainingCount),
		}
	}

	data := [][]string{toData(blueTarget, blueTGName, blueHealth), toData(greenTarget, greenTGName, greenHealth)}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{
		"target",
		"traffic(%)",
		"asg:name",
		"asg:desired",
		"asg:min",
		"asg:max",
		"asg:lifecycle",
		"elb:tgname",
		"elb:total",
		"elb:healthy",
		"elb:unhealthy",
		"elb:unused",
		"elb:initial",
		"elb:draining"})
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (d *Deployer) Deploy(
	ctx context.Context, swap bool,
	cleanupBeforeDeploy bool,
	cleanupAfterDeploy bool,
	swapDuration *time.Duration) error {

	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return err
	}

	if cleanupBeforeDeploy {
		d.logger.Info(fmt.Sprintf("Start cleanup on idle '%s' target.", string(info.IdlingTarget.Type)))

		err := d.CleanupAutoScalingGroup(ctx, *info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName)
		if err != nil {
			return err
		}
		d.logger.Info("Cleanup completed.")
	}

	//TODO: Actually, it may be necessary, so I'll keep it as a comment.
	// see: https://docs.aws.amazon.com/codedeploy/latest/userguide/integrations-aws-auto-scaling.html#integrations-aws-auto-scaling-behaviors-mixed-environment
	//scalingProcesses := []string{"AZRebalance", "AlarmNotification", "ScheduledActions", "ReplaceUnhealthy"}
	//if err := d.suspendAutoScalingProcesses(ctx, *info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, scalingProcesses); err != nil {
	//	d.logger.Warn("ScalingProcesses failed to suspend, but will continue processing", err)
	//}
	//defer func() {
	//	if err := d.resumeAutoScalingProcesses(ctx, *info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, scalingProcesses); err != nil {
	//		d.logger.Warn("ScalingProcesses failed to resume", err)
	//	}
	//}()

	d.logger.Info(fmt.Sprintf(
		"Start updating AutoScalingGruop of the '%s' target. Prepare instances of the same capacity as the '%s' target.",
		info.IdlingTarget.Type,
		info.RunningTarget.Type))
	err = d.UpdateAutoScalingGroup(ctx,
		*info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName,
		info.RunningTarget.AutoScalingGroup.DesiredCapacity,
		info.RunningTarget.AutoScalingGroup.MinSize,
		info.RunningTarget.AutoScalingGroup.MaxSize)
	if err != nil {
		return err
	}
	d.logger.Info("AutoScalingGroup has been updated.")

	d.logger.Info(fmt.Sprintf("Start '%s' health check.", info.IdlingTarget.Type))
	err = d.HealthCheck(ctx,
		*info.IdlingTarget.TargetGroup.TargetGroupArn,
		int(*info.RunningTarget.AutoScalingGroup.DesiredCapacity))
	if err != nil {
		if errors.Is(err, RetryTimeout) {
			d.logger.Error("Health check timed out. Initiating a rollback as the process cannot continue.", nil)
			if err := d.CleanupAutoScalingGroup(ctx, *info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName); err != nil {
				return errors.WithMessage(err, "Rollback failed.")
			}
			return CancellationError
		}
		return err
	}

	d.logger.Info("Health check completed.")
	if err := d.ShowStatus(ctx); err != nil {
		return err
	}

	if swap {
		d.logger.Info("Start swap traffic.")
		if err := d.SwapTraffic(ctx, swapDuration); err != nil {
			return err
		}

		d.logger.Info("Traffic swap completed.")
		if err := d.ShowStatus(ctx); err != nil {
			return err
		}
	}

	if cleanupAfterDeploy {
		info, err := d.GetDeployInfo(ctx)
		if err != nil {
			return err
		}

		d.logger.Info(fmt.Sprintf(
			"Update '%s' target MinSize to 0 to clean up instances that are no longer needed. The automatic scale-in will clean up slowly.",
			info.RunningTarget.Type))
		err = d.UpdateAutoScalingGroup(ctx,
			*info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName,
			nil,
			aws.Int32(0),
			nil)
		if err != nil {
			return err
		}
		if err = d.ShowStatus(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (d *Deployer) HealthCheck(ctx context.Context, targetGroupArn string, desiredCount int) error {
	maxLimit := d.config.RetryPolicy.MaxLimit
	interval := aws.Duration(time.Duration(d.config.RetryPolicy.IntervalSeconds) * time.Second)
	return NewFixedIntervalRetryer(maxLimit, interval).Start(
		func(index int, interval *time.Duration) (RetryResult, error) {
			health, err := d.getHealthInfo(ctx, targetGroupArn)
			if err != nil {
				return FinishRetry, err
			}

			if health.HealthyCount >= desiredCount {
				return FinishRetry, nil
			}

			d.logger.Info(fmt.Sprintf("Health check in progress. desired:%d, total:%d, healthy:%d, unhealthy:%d, unused:%d, init:%d, drain:%d",
				desiredCount,
				health.TotalCount,
				health.HealthyCount,
				health.UnhealthyCount,
				health.UnusedCount,
				health.InitialCount,
				health.DrainingCount,
			))

			return ContinueRetry, nil
		})
}

func (d *Deployer) UpdateTraffic(ctx context.Context, blueWeight int32, greenWeight int32) error {
	forwardConfig := &albTypes.ForwardActionConfig{
		TargetGroups: []albTypes.TargetGroupTuple{
			{
				TargetGroupArn: &d.config.Target.Blue.TargetGroupArn,
				Weight:         &blueWeight,
			},
			{
				TargetGroupArn: &d.config.Target.Green.TargetGroupArn,
				Weight:         &greenWeight,
			},
		},
	}

	rule, err := d.getListenerRule(ctx)
	if err != nil {
		return err
	}
	if len(rule.Actions) > 0 {
		forwardConfig.TargetGroupStickinessConfig = rule.Actions[0].ForwardConfig.TargetGroupStickinessConfig
	}

	_, err = d.alb.ModifyRule(ctx, &alb.ModifyRuleInput{
		RuleArn: &d.config.ListenerRuleArn,
		Actions: []albTypes.Action{{Type: albTypes.ActionTypeEnumForward, ForwardConfig: forwardConfig}},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (d *Deployer) SwapTraffic(ctx context.Context, duration *time.Duration) error {
	rule, err := d.getListenerRule(ctx)
	if err != nil {
		return err
	}

	blue, err := d.GetDeployTarget(ctx, rule, BlueTargetType)
	if err != nil {
		return err
	}

	green, err := d.GetDeployTarget(ctx, rule, GreenTargetType)
	if err != nil {
		return err
	}

	if *duration > 0 {
		d.logger.Info(fmt.Sprintf(
			"Traffic update to blue->50%%, green->50%%, wait %.0f seconds.", duration.Seconds()))
		if err := d.UpdateTraffic(ctx, int32(50), int32(50)); err != nil {
			return err
		}
		time.Sleep(*duration)
	}

	d.logger.Info(fmt.Sprintf("Traffic update to blue->%d%%, green->%d%%.",
		*green.TargetGroup.Weight,
		*blue.TargetGroup.Weight))
	return d.UpdateTraffic(ctx, *green.TargetGroup.Weight, *blue.TargetGroup.Weight)
}

func (d *Deployer) UpdateAutoScalingGroup(
	ctx context.Context, autoScalingGroupName string, desiredCapacity *int32, minSize *int32, maxSize *int32) error {

	input := &asg.UpdateAutoScalingGroupInput{AutoScalingGroupName: &autoScalingGroupName}
	if desiredCapacity != nil && *desiredCapacity >= 0 {
		input.DesiredCapacity = desiredCapacity
	}
	if minSize != nil && *minSize >= 0 {
		input.MinSize = minSize
	}
	if maxSize != nil && *maxSize >= 0 {
		input.MaxSize = maxSize
	}
	_, err := d.asg.UpdateAutoScalingGroup(ctx, input)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (d *Deployer) UpdateAutoScalingGroupByTarget(
	ctx context.Context, targetType TargetType, desiredCapacity *int32, minSize *int32, maxSize *int32) error {

	rule, err := d.getListenerRule(ctx)
	if err != nil {
		return err
	}

	target, err := d.GetDeployTarget(ctx, rule, targetType)
	if err != nil {
		return err
	}

	err = d.UpdateAutoScalingGroup(ctx,
		*target.AutoScalingGroup.AutoScalingGroupName,
		desiredCapacity,
		minSize,
		maxSize)
	if err != nil {
		return err
	}

	return nil
}

func (d *Deployer) CleanupAutoScalingGroup(ctx context.Context, autoScalingGroupName string) error {
	if err := d.UpdateAutoScalingGroup(
		ctx, autoScalingGroupName, aws.Int32(0), aws.Int32(0), nil); err != nil {
		return errors.WithStack(err)
	}

	maxLimit := d.config.RetryPolicy.MaxLimit
	interval := aws.Duration(time.Duration(d.config.RetryPolicy.IntervalSeconds) * time.Second)
	return NewFixedIntervalRetryer(maxLimit, interval).Start(
		func(index int, interval *time.Duration) (RetryResult, error) {
			output, err := d.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil {
				return FinishRetry, errors.WithStack(err)
			}

			current := output.AutoScalingGroups[0]

			if len(current.Instances) <= 0 {
				return FinishRetry, nil
			}

			d.logger.Info(fmt.Sprintf(
				"Cleanup ASG:'%s', desired:%d, min:%d, max:%d, instances:%d, lifecycle:{%s}",
				autoScalingGroupName,
				*current.DesiredCapacity,
				*current.MinSize,
				*current.MaxSize,
				len(current.Instances),
				d.lifecycleStateToString(&current),
			))

			return ContinueRetry, nil
		})
}

func (d *Deployer) MoveScheduledActions(
	ctx context.Context, fromAutoScalingGroupName string, toAutoScalingGroupName string) error {

	output, err := d.asg.DescribeScheduledActions(ctx, &asg.DescribeScheduledActionsInput{
		AutoScalingGroupName: &fromAutoScalingGroupName,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	if len(output.ScheduledUpdateGroupActions) <= 0 {
		return nil
	}

	msg := fmt.Sprintf(" from:%s, to:%s", fromAutoScalingGroupName, toAutoScalingGroupName)
	for _, from := range output.ScheduledUpdateGroupActions {
		_, err := d.asg.PutScheduledUpdateGroupAction(ctx, &asg.PutScheduledUpdateGroupActionInput{
			AutoScalingGroupName: &toAutoScalingGroupName,
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
		d.logger.Info(fmt.Sprintf("ScheduledActions:'%s' moved successfully.", *from.ScheduledActionName))
	}

	return nil
}
