package main

import (
	"context"
	"fmt"
	"github.com/alecthomas/kingpin"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/givery-technology/deployman/internal"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var Version = "unset"

var (
	app     = kingpin.New("deployman", "A CLI for controlling ALB and two AutoScalingGroups and performing Blue/Green Deployment.")
	config  = app.Flag("config", "[OPTIONAL] Configuration file path. By default, this value is './deployman.json'. If this file does not exist, an error will occur.").Default("./deployman.json").String()
	verbose = app.Flag("verbose", "[OPTIONAL] A detailed log containing call stacks will be error messages.").Bool()

	version = app.Command("version", "Show current CLI version.")

	bundle = app.Command("bundle", "")

	bundleRegister         = bundle.Command("register", "Register a new application bundle with any name, specifying the local file path to S3 bucket.")
	bundleRegisterFilepath = bundleRegister.Flag("file", "[REQUIRED] File name and path in local").Required().String()
	bundleRegisterName     = bundleRegister.Flag("name", "[REQUIRED] Name of bundle to be registered").Required().String()
	bundleRegisterActivate = bundleRegister.Flag("with-activate", "[OPTIONAL] Associate (activate) this bundle with an idle AutoScalingGroup.").Bool()

	bundleList = bundle.Command("list", "List registered application bundles.")

	bundleActivate       = bundle.Command("activate", "Activate one of the registered bundles. The active bundle will be used for the next deployment or scale-out.")
	bundleActivateTarget = bundleActivate.Flag("target", "[REQUIRED] Target type for bundle. Valid values are either 'blue' or 'green'. The 'ec2 status' command allows you to check the target details.").Required().Enum("blue", "green")
	bundleActivateName   = bundleActivate.Flag("name", "[REQUIRED] Bundle Name. Valid names can be checked with the 'bundle list' command.").Required().String()

	bundleDownload       = bundle.Command("download", "Download application bundle file.")
	bundleDownloadTarget = bundleDownload.Flag("target", "[REQUIRED] Target type for bundle. Valid values are either 'blue' or 'green'. The 'ec2 status' command allows you to check the target details.").Required().Enum("blue", "green")

	ec2 = app.Command("ec2", "")

	ec2status = ec2.Command("status", "Show current deployment status.")

	ec2deploy          = ec2.Command("deploy", "Deploy a new application to an idling AutoScalingGroup.")
	ec2deploySilent    = ec2deploy.Flag("silent", "[OPTIONAL] Skip confirmation before process.").Bool()
	ec2deployNoCleanup = ec2deploy.Flag("no-cleanup", "[OPTIONAL] Skip cleanup of idle old AutoScalingGroups that are no longer needed after deployment.").Bool()
	ec2deploySwapTime  = ec2deploy.Flag("duration", "[OPTIONAL] Time to wait until traffic is completely swapped. Default is '0s'. If this value is set to '60s', the B/G traffic is distributed 50:50 and waits for 60 seconds. After that, the B/G traffic will be completely swapped.").Default("0s").Duration()

	ec2rollback          = ec2.Command("rollback", "Restore the AutoScalingGroup to their original state, then swap traffic.")
	ec2rollbackSilent    = ec2rollback.Flag("silent", "[OPTIONAL] Skip confirmation before process.").Bool()
	ec2rollbackNoCleanup = ec2rollback.Flag("no-cleanup", "[OPTIONAL] Skip cleanup of idle old AutoScalingGroups that are no longer needed after deployment.").Bool()
	ec2rollbackSwapTime  = ec2rollback.Flag("duration", "[OPTIONAL] Time to wait until traffic is completely swapped. Default is '0s'. If this value is set to '60s', the B/G traffic is distributed 50:50 and waits for 60 seconds. After that, the B/G traffic will be completely swapped.").Default("0s").Duration()

	ec2cleanup = ec2.Command("cleanup", "Terminate all instances that are idle, i.e., in an AutoScalingGroup with a traffic weight of 0. You can check the current status with the 'ec2 status' command.")

	ec2swap         = ec2.Command("swap", "B/G Swap the current traffic of the respective 2 AutoScalingGroups. You can check the current status with the 'ec2 status' command.")
	ec2swapDuration = ec2swap.Flag("duration", "[OPTIONAL] Time to wait until traffic is completely swapped. Default is '0s'. If this value is set to '60s', the B/G traffic is distributed 50:50 and waits for 60 seconds. After that, the B/G traffic will be completely swapped.").Default("0s").Duration()

	ec2traffic            = ec2.Command("traffic", "Update the traffic of the respective target group of B/G to any value. You can check the current status with the 'ec2 status' command.")
	ec2trafficBlueWeight  = ec2traffic.Flag("blue", "Traffic weight for blue TargetGroup").Required().Int32()
	ec2trafficGreenWeight = ec2traffic.Flag("green", "Traffic weight for green TargetGroup").Required().Int32()

	ec2autoscaling        = ec2.Command("autoscaling", "Update the capacity of any AutoScalingGroup.")
	ec2autoscalingTarget  = ec2autoscaling.Flag("target", "Target type of AutoScalingGroup. Valid values are either 'blue' or 'green'. The 'ec2 status' command allows you to check the target details.").Required().Enum("blue", "green")
	ec2autoscalingDesired = ec2autoscaling.Flag("desired", "DesiredCapacity").Default("-1").Int32()
	ec2autoscalingMinSize = ec2autoscaling.Flag("min", "MinSize").Default("-1").Int32()
	ec2autoscalingMaxSize = ec2autoscaling.Flag("max", "MaxSize").Default("-1").Int32()

	ec2moveScheduledActions     = ec2.Command("move-scheduled-actions", "Move ScheduledActions that exist in any AutoScalingGroup to another AutoScalingGroup.")
	ec2moveScheduledActionsFrom = ec2moveScheduledActions.Flag("from", "Name of AutoScalingGroup").Required().String()
	ec2moveScheduledActionsTo   = ec2moveScheduledActions.Flag("to", "Name of AutoScalingGroup").Required().String()
)

func main() {
	command := kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := &internal.DefaultLogger{Verbose: *verbose}

	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer func() {
		logger.Error("🚨 Command Cancelled", nil)
		cancel()
	}()

	go func() {
		trap := make(chan os.Signal, 1)
		signal.Notify(trap, syscall.SIGTERM, syscall.SIGINT)
		<-trap
		logger.Fatal("🚨 Command Cancelled", nil)
	}()

	if command == version.FullCommand() {
		fmt.Println("deployman", Version)
		os.Exit(0)
	}

	awsDefaultRegion := internal.GetEnv("AWS_REGION", "us-east-1")
	awsDefaultConfig, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(awsDefaultRegion))
	if err != nil {
		logger.Fatal("🚨 Command Failure", err)
	}

	deployConfig, err := internal.NewConfig(*config)
	if err != nil {
		logger.Fatal("🚨 Command Failure", err)
	}

	deployer := internal.NewDeployer(awsDefaultRegion, &awsDefaultConfig, deployConfig, logger)
	bundler := internal.NewBundler(awsDefaultRegion, &awsDefaultConfig, deployConfig, logger)

	switch command {
	case bundleRegister.FullCommand():
		if err = bundler.Register(ctx, *bundleRegisterFilepath, *bundleRegisterName); err != nil {
			break
		}
		if *bundleRegisterActivate {
			info, err := deployer.GetDeployInfo(ctx)
			if err != nil {
				break
			}
			err = bundler.Activate(ctx, info.IdlingTarget.Type, bundleRegisterName)
		}

	case bundleList.FullCommand():
		err = bundler.ListBundles(ctx)

	case bundleActivate.FullCommand():
		err = bundler.Activate(ctx, internal.TargetType(*bundleActivateTarget), bundleActivateName)

	case bundleDownload.FullCommand():
		err = bundler.Download(ctx, internal.TargetType(*bundleDownloadTarget))

	case ec2status.FullCommand():
		err = deployer.ShowStatus(ctx, nil)

	case ec2deploy.FullCommand():
		if *ec2deploySilent == false && internal.AskToContinue() == false {
			logger.Fatal("🚨 Command Cancelled", nil)
		}
		err = deployer.Deploy(ctx, true, true, !*ec2deployNoCleanup, ec2deploySwapTime)

	case ec2rollback.FullCommand():
		if *ec2rollbackSilent == false && internal.AskToContinue() == false {
			logger.Fatal("🚨 Command Cancelled", nil)
		}
		err = deployer.Deploy(ctx, true, false, !*ec2rollbackNoCleanup, ec2rollbackSwapTime)

	case ec2cleanup.FullCommand():
		info, err := deployer.GetDeployInfo(ctx)
		if err != nil {
			logger.Fatal("🚨 Command Failure", err)
		}
		_, err = deployer.CleanupAutoScalingGroup(ctx, info.IdlingTarget.AutoScalingGroup)

	case ec2swap.FullCommand():
		err = deployer.SwapTraffic(ctx, ec2swapDuration)

	case ec2traffic.FullCommand():
		err = deployer.UpdateTraffic(ctx, *ec2trafficBlueWeight, *ec2trafficGreenWeight)

	case ec2autoscaling.FullCommand():
		target, err := deployer.GetDeployTarget(ctx, internal.TargetType(*ec2autoscalingTarget))
		if err != nil {
			logger.Fatal("🚨 Command Failure", err)
		}
		err = deployer.UpdateAutoScalingGroup(ctx,
			*target.AutoScalingGroup.AutoScalingGroupName,
			ec2autoscalingDesired,
			ec2autoscalingMinSize,
			ec2autoscalingMaxSize)

	case ec2moveScheduledActions.FullCommand():
		err = deployer.MoveScheduledActions(ctx, *ec2moveScheduledActionsFrom, *ec2moveScheduledActionsTo)

	default:
		kingpin.Usage()
		os.Exit(1)
	}

	if err != nil {
		logger.Error("🚨 Command Failure", err)
		os.Exit(1)
	}

	logger.Info("🎉 Command Succeeded")
	os.Exit(0)
}
