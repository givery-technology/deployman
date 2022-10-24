package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/alecthomas/kingpin"
	"github.com/aws/aws-sdk-go-v2/aws"
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
	config  = app.Flag("config", "Configuration filepath. By default, './deployman.json'").Default("./deployman.json").String()
	verbose = app.Flag("verbose", "A detailed log containing call stacks will be output.").Default("false").Bool()
	version = app.Command("version", "Show current CLI version.")

	bundle                 = app.Command("bundle", "Manage applicaiton bundles in S3 bucket.")
	bundleRegister         = bundle.Command("register", "Register new application bundle. By default, register bundles associated with idle AutoScalingGroups.")
	bundleRegisterFilepath = bundleRegister.Flag("file", "File path").Required().String()
	bundleRegisterName     = bundleRegister.Flag("name", "Name of bundle to be registered").Required().String()
	bundleRegisterActivate = bundleRegister.Flag("with-activate", "Activate registered bundles.").Bool()
	bundleList             = bundle.Command("list", "List registered application bundles.")
	bundleActivate         = bundle.Command("activate", "Activate one of the registered bundles. The activated bundle will be used for the next deployment or scale-out.")
	bundleActivateTarget   = bundleActivate.Flag("target", "Target type for bundle. Valid values are either 'blue' or 'green'").Enum("blue", "green")
	bundleActivateName     = bundleActivate.Flag("name", "Name of bundle to activate").Required().String()
	bundleDownload         = bundle.Command("download", "Download application bundle file.")
	bundleDownloadTarget   = bundleDownload.Flag("target", "Target type for bundle. Valid values are either 'blue' or 'green'").Enum("blue", "green")

	ec2                  = app.Command("ec2", "Manage B/G deployment for EC2.")
	ec2status            = ec2.Command("status", "Show current deployment status.")
	ec2deploy            = ec2.Command("deploy", "Deploy a new application to an idling AutoScalingGroup.")
	ec2deployNoConfirm   = ec2deploy.Flag("y", "Skip confirmation before process.").Default("false").Bool()
	ec2deployCleanup     = ec2deploy.Flag("cleanup", "Cleanup idling AutoScalingGroup's instances. Cleanup is done slowly by scale-in action.").Default("true").Bool()
	ec2rollback          = ec2.Command("rollback", "Restore the AutoScalingGroup to their original state, then swap traffic.")
	ec2rollbackNoConfirm = ec2rollback.Flag("y", "Skip confirmation before process.").Default("false").Bool()

	ec2cleanup                  = ec2.Command("cleanup", "Cleanup idling AutoScalingGroup's instances.")
	ec2swap                     = ec2.Command("swap", "Swap traffic from a running AutoScalingGroup to an idling AutoScalingGroup.")
	ec2updateASG                = ec2.Command("update-autoscaling", "Update capacity of AutoScalingGroup.")
	ec2updateASGName            = ec2updateASG.Flag("name", "Name of AutoScalingGroup").Required().String()
	ec2updateASGDesired         = ec2updateASG.Flag("desired", "DesiredCapacity").Int32()
	ec2updateASGMinSize         = ec2updateASG.Flag("min", "MinSize").Int32()
	ec2updateASGMaxSize         = ec2updateASG.Flag("max", "MaxSize").Int32()
	ec2moveScheduledActions     = ec2.Command("move-schaduled-actions", "Move ScheduledActions of AutoScalingGroup.")
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

	awsDefaultRegion := internal.GetEnv(aws.String("AWS_REGION"), aws.String("ap-northeast-1"))
	awsDefaultConfig, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion(awsDefaultRegion))
	if err != nil {
		logger.Fatal("🚨 Command Failure", err)
	}

	deployConfig, err := internal.NewConfig(config)
	if err != nil {
		logger.Fatal("🚨 Command Failure", err)
	}

	deployer := internal.NewDeployer(&awsDefaultRegion, &awsDefaultConfig, deployConfig, logger)
	bundler := internal.NewBundler(&awsDefaultRegion, &awsDefaultConfig, deployConfig, logger)

	switch command {
	case version.FullCommand():
		fmt.Println("deployman", Version)
		os.Exit(0)

	case bundleRegister.FullCommand():
		if err = bundler.Register(ctx, bundleRegisterFilepath, bundleRegisterName); err != nil {
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
		var targetType internal.TargetType
		if *bundleActivateTarget == "blue" {
			targetType = internal.BlueTargetType
		} else if *bundleActivateTarget == "green" {
			targetType = internal.GreenTargetType
		} else {
			info, err := deployer.GetDeployInfo(ctx)
			if err != nil {
				logger.Fatal("🚨 Command Failure", err)
			}
			targetType = info.IdlingTarget.Type
		}
		err = bundler.Activate(ctx, targetType, bundleActivateName)

	case bundleDownload.FullCommand():
		var targetType internal.TargetType
		if *bundleDownloadTarget == "blue" {
			targetType = internal.BlueTargetType
		} else if *bundleDownloadTarget == "green" {
			targetType = internal.GreenTargetType
		} else {
			logger.Fatal("🚨 Command Failure", errors.New("the only valid values for the target flag are 'blue' and 'green'"))
		}
		err = bundler.Download(ctx, targetType)

	case ec2status.FullCommand():
		err = deployer.ShowStatus(ctx)

	case ec2deploy.FullCommand():
		if *ec2deployNoConfirm == false {
			logger.Info("Start deployment process")
			if internal.AskToContinue() == false {
				logger.Fatal("🚨 Command Cancelled", nil)
			}
		}
		err = deployer.Deploy(ctx, true, true, *ec2deployCleanup)

	case ec2rollback.FullCommand():
		if *ec2rollbackNoConfirm == false {
			logger.Warn("Start rollback process", nil)
			if internal.AskToContinue() == false {
				logger.Fatal("🚨 Command Cancelled", nil)
			}
		}
		err = deployer.Deploy(ctx, true, false, false)

	case ec2cleanup.FullCommand():
		_, err = deployer.CleanupIdlingTarget(ctx)

	case ec2swap.FullCommand():
		err = deployer.SwapTraffic(ctx)

	case ec2updateASG.FullCommand():
		_, err = deployer.UpdateAutoScalingGroup(ctx, ec2updateASGName, ec2updateASGDesired, ec2updateASGMinSize, ec2updateASGMaxSize, false)

	case ec2moveScheduledActions.FullCommand():
		err = deployer.MoveScheduledActions(ctx, ec2moveScheduledActionsFrom, ec2moveScheduledActionsTo)

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
