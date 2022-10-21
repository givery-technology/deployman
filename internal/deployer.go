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
	BlueTargetType    TargetType = "blue"
	GreenTargetType   TargetType = "green"
	UnknownTargetType TargetType = "unknown"

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
	ListenerRule     *albTypes.Rule
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

func (d *Deployer) suspendAutoScalingProcesses(ctx context.Context, autoScalingGroupName *string, scalingProcesses *[]string) error {
	_, err := d.asg.SuspendProcesses(ctx, &asg.SuspendProcessesInput{
		AutoScalingGroupName: autoScalingGroupName,
		ScalingProcesses:     *scalingProcesses,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (d *Deployer) resumeAutoScalingProcesses(ctx context.Context, autoScalingGroupName *string, scalingProcesses *[]string) error {
	_, err := d.asg.ResumeProcesses(ctx, &asg.ResumeProcessesInput{
		AutoScalingGroupName: autoScalingGroupName,
		ScalingProcesses:     *scalingProcesses,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

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

func (d *Deployer) ShowStatus(ctx context.Context) error {
	getAutoScalingGroup := func(autoScalingGroups *[]asgTypes.AutoScalingGroup, autoScalingGroupName *string) (*asgTypes.AutoScalingGroup, error) {
		for _, autoScalingGroup := range *autoScalingGroups {
			if *autoScalingGroup.AutoScalingGroupName == *autoScalingGroupName {
				return &autoScalingGroup, nil
			}
		}

		return nil, errors.Errorf("MissingAutoScalingGroup: %s", *autoScalingGroupName)
	}

	getTargetWeight := func(rule *albTypes.Rule, targetGroupArn *string) (*int32, error) {
		for _, action := range rule.Actions {
			if action.Type == albTypes.ActionTypeEnumForward {
				for _, targetGroup := range action.ForwardConfig.TargetGroups {
					if *targetGroup.TargetGroupArn == *targetGroupArn {
						return targetGroup.Weight, nil
					}
				}
			}
		}

		return nil, errors.Errorf("MissingTargetGroup: %s", *targetGroupArn)
	}

	collect := func(info *DeployInfo, healthInfos *[]HealthInfo, autoScalingGroups *[]asgTypes.AutoScalingGroup, targetType TargetType) (*[]string, error) {
		var target *DeployTarget
		if info.RunningTarget.Type == targetType {
			target = info.RunningTarget
		} else if info.IdlingTarget.Type == targetType {
			target = info.IdlingTarget
		} else {
			return nil, errors.Errorf("MissingDeployTarget: %s", targetType)
		}

		autoScalingGroup, err := getAutoScalingGroup(autoScalingGroups, target.AutoScalingGroup.AutoScalingGroupName)
		if err != nil {
			return nil, err
		}

		targetWeight, err := getTargetWeight(target.ListenerRule, target.TargetGroup.TargetGroupArn)
		if err != nil {
			return nil, err
		}

		var currentHealth HealthInfo
		for _, info := range *healthInfos {
			if info.TargetGroupArn == *target.TargetGroup.TargetGroupArn {
				currentHealth = info
			}
		}

		return &[]string{
			fmt.Sprint(target.Type),
			fmt.Sprint(*targetWeight),
			fmt.Sprint(*autoScalingGroup.DesiredCapacity),
			fmt.Sprint(*autoScalingGroup.MinSize),
			fmt.Sprint(*autoScalingGroup.MaxSize),
			fmt.Sprint(currentHealth.TotalCount),
			fmt.Sprint(currentHealth.HealthyCount),
			fmt.Sprint(currentHealth.UnhealthyCount),
			fmt.Sprint(currentHealth.UnusedCount),
			fmt.Sprint(currentHealth.InitialCount),
			fmt.Sprint(currentHealth.DrainingCount),
		}, nil
	}

	getHealthInfos := func(targetGroupArns *[]string) (*[]HealthInfo, error) {
		done := make(chan *HealthInfo)
		defer close(done)

		// send
		for _, targetGroupArn := range *targetGroupArns {
			go func(targetGroupArn string, done chan *HealthInfo) {
				health := d.getHealthInfo(ctx, &targetGroupArn)
				done <- health
			}(targetGroupArn, done)
		}

		// recv
		var infos []HealthInfo
		for range *targetGroupArns {
			info := <-done
			infos = append(infos, *info)
		}

		for _, info := range infos {
			if info.Error != nil {
				return nil, info.Error
			}
		}

		return &infos, nil
	}

	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return err
	}

	healthInfos, err := getHealthInfos(&[]string{*info.IdlingTarget.TargetGroup.TargetGroupArn, *info.RunningTarget.TargetGroup.TargetGroupArn})
	if err != nil {
		return err
	}

	autoScalingGroups := []asgTypes.AutoScalingGroup{*info.IdlingTarget.AutoScalingGroup, *info.RunningTarget.AutoScalingGroup}

	blue, err := collect(info, healthInfos, &autoScalingGroups, BlueTargetType)
	if err != nil {
		return err
	}

	green, err := collect(info, healthInfos, &autoScalingGroups, GreenTargetType)
	if err != nil {
		return err
	}

	var data [][]string
	data = append(data, *blue)
	data = append(data, *green)

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"target", "traffic(%)", "asg:desired", "asg:min", "asg:max", "elb:total", "elb:healthy", "elb:unhealthy", "elb:unused", "elb:initial", "elb:draining"})
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (d *Deployer) GetDeployInfo(ctx context.Context) (*DeployInfo, error) {
	getTargetGroup := func(rule *albTypes.Rule, targetGroupArns *[]string, cond func(tg *albTypes.TargetGroupTuple) bool) (*albTypes.TargetGroupTuple, error) {
		for _, action := range rule.Actions {
			if action.Type != albTypes.ActionTypeEnumForward {
				continue
			}

			for _, targetGroup := range action.ForwardConfig.TargetGroups {
				if !Contains(targetGroupArns, targetGroup.TargetGroupArn) {
					break
				}

				if cond(&targetGroup) {
					return &targetGroup, nil
				}
			}
		}

		return nil, errors.Errorf("MissingTargetGroups: %+v", targetGroupArns)
	}

	getAutoScalingGroup := func(autoScalingGroups *[]asgTypes.AutoScalingGroup, targetGroupArn *string) (*asgTypes.AutoScalingGroup, error) {
		for _, autoScalingGroup := range *autoScalingGroups {
			if Contains(&autoScalingGroup.TargetGroupARNs, targetGroupArn) {
				return &autoScalingGroup, nil
			}
		}

		return nil, errors.Errorf("InvalidTargetGroup: %s", *targetGroupArn)
	}

	ruleOutput, err := d.alb.DescribeRules(ctx, &alb.DescribeRulesInput{
		RuleArns: []string{d.config.ListenerRuleArn},
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if len(ruleOutput.Rules) <= 0 {
		return nil, errors.Errorf("MissingListenerRule: %s", d.config.ListenerRuleArn)
	}

	rule := &ruleOutput.Rules[0]
	targetGroupArns := &[]string{d.config.Target.Blue.TargetGroupArn, d.config.Target.Green.TargetGroupArn}

	idlingTargetGroup, err := getTargetGroup(rule, targetGroupArns, func(tg *albTypes.TargetGroupTuple) bool {
		return *tg.Weight == int32(0)
	})
	if err != nil {
		return nil, errors.WithMessage(err, "IdlingTargetGroupError")
	}

	runningTargetGroup, err := getTargetGroup(rule, targetGroupArns, func(tg *albTypes.TargetGroupTuple) bool {
		return *tg.Weight != int32(0)
	})
	if err != nil {
		return nil, errors.WithMessage(err, "RunningTargetGroupError")
	}

	asgOutput, err := d.asg.DescribeAutoScalingGroups(ctx, &asg.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{d.config.Target.Blue.AutoScalingGroupName, d.config.Target.Green.AutoScalingGroupName},
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	idlingAutoScalingGroup, err := getAutoScalingGroup(&asgOutput.AutoScalingGroups, idlingTargetGroup.TargetGroupArn)
	if err != nil {
		return nil, errors.WithMessage(err, "IdlingAutoScalingGroupError")
	}

	runningAutoScalingGroup, err := getAutoScalingGroup(&asgOutput.AutoScalingGroups, runningTargetGroup.TargetGroupArn)
	if err != nil {
		return nil, errors.WithMessage(err, "RunningAutoScalingGroupError")
	}

	blueOrGreen := func(targetGroupArn *string) TargetType {
		switch *targetGroupArn {
		case d.config.Target.Blue.TargetGroupArn:
			return BlueTargetType
		case d.config.Target.Green.TargetGroupArn:
			return GreenTargetType
		default:
			return UnknownTargetType
		}
	}

	return &DeployInfo{
		IdlingTarget: &DeployTarget{
			Type:             blueOrGreen(idlingTargetGroup.TargetGroupArn),
			ListenerRule:     rule,
			TargetGroup:      idlingTargetGroup,
			AutoScalingGroup: idlingAutoScalingGroup,
		},
		RunningTarget: &DeployTarget{
			Type:             blueOrGreen(runningTargetGroup.TargetGroupArn),
			ListenerRule:     rule,
			TargetGroup:      runningTargetGroup,
			AutoScalingGroup: runningAutoScalingGroup,
		},
	}, nil
}

func (d *Deployer) Deploy(ctx context.Context, swap bool, beforeCleanup bool, afterCleanup bool) error {
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

	// see: https://docs.aws.amazon.com/codedeploy/latest/userguide/integrations-aws-auto-scaling.html#integrations-aws-auto-scaling-behaviors-mixed-environment
	scalingProcesses := []string{"AZRebalance", "AlarmNotification", "ScheduledActions", "ReplaceUnhealthy"}
	if err := d.suspendAutoScalingProcesses(ctx, info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, &scalingProcesses); err != nil {
		d.logger.Warn("ScalingProcesses failed to suspend, but will continue processing", err)
	}
	defer func() {
		if err := d.resumeAutoScalingProcesses(ctx, info.RunningTarget.AutoScalingGroup.AutoScalingGroupName, &scalingProcesses); err != nil {
			d.logger.Warn("ScalingProcesses failed to resume", err)
		}
	}()

	d.logger.Info(fmt.Sprintf("Start deployment to %s target. Prepare instances of the same amount as %s target.", info.IdlingTarget.Type, info.RunningTarget.Type))
	if _, err := d.UpdateAutoScalingGroup(ctx,
		info.IdlingTarget.AutoScalingGroup.AutoScalingGroupName,
		info.RunningTarget.AutoScalingGroup.DesiredCapacity,
		info.RunningTarget.AutoScalingGroup.MinSize,
		info.RunningTarget.AutoScalingGroup.MaxSize,
		false); err != nil {
		return err
	}

	time.Sleep(10 * time.Second) // Wait a bit for stability

	d.logger.Info("Show deployment status after deploy")
	if err := d.ShowStatus(ctx); err != nil {
		return err
	}

	if swap {
		if err := d.SwapTraffic(ctx); err != nil {
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

func (d *Deployer) UpdateTraffic(ctx context.Context, blueWeight *int32, greenWeight *int32) error {
	d.logger.Info(fmt.Sprintf("Start update traffic. blue: %d%%, green: %d%%", *blueWeight, *greenWeight))

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

func (d *Deployer) SwapTraffic(ctx context.Context) error {
	decideWeight := func(info *DeployInfo, typ TargetType) *int32 {
		if info.IdlingTarget.Type == typ {
			return info.IdlingTarget.TargetGroup.Weight
		} else if info.RunningTarget.Type == typ {
			return info.RunningTarget.TargetGroup.Weight
		} else {
			return nil
		}
	}

	info, err := d.GetDeployInfo(ctx)
	if err != nil {
		return err
	}

	blueWeight := decideWeight(info, BlueTargetType)
	if blueWeight == nil {
		return errors.New("MissingBlueTargetGroup")
	}

	greenWeight := decideWeight(info, GreenTargetType)
	if greenWeight == nil {
		return errors.New("MissingGreenTargetGroup")
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
