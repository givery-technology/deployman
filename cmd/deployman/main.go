package main

import (
	"context"
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
var UpdatedAt = "unset"

var (
	app     = kingpin.New("deployman", "A CLI for controlling ALB and two AutoScalingGroups and performing Blue/Green Deployment.")
	config  = app.Flag("config", "Configuration filepath. By default, './config.json'").Default("./config.json").String()
	verbose = app.Flag("verbose", "A detailed log containing call stacks will be output").Default("false").Bool()
	version = app.Command("version", "Show current CLI version.")

	bundle                        = app.Command("bundle", "Manage applicaiton bundle.")
	bundleRegister                = bundle.Command("register", "Register new application bundle.")
	bundleRegisterFile            = bundleRegister.Flag("file", "Bundle file path").Required().String()
	bundleRegisterName            = bundleRegister.Flag("name", "Bundle file name").Required().String()
	bundleList                    = bundle.Command("list", "List registered application bundles.")
	bundleUpdateDeployTarget      = bundle.Command("update-deploy-target", "Update current deployable application bundle.")
	bundleUpdateDeployTargetValue = bundleUpdateDeployTarget.Flag("value", "Value of deployable application bundle").Required().String()
	bundleDownload                = bundle.Command("download", "Download application bunle file.")
	bundleDownloadFile            = bundleDownload.Flag("file", "bundle file name and path.").Default("").String()

	ec2              = app.Command("ec2", "Manage application deployment for EC2.")
	ec2status        = ec2.Command("status", "Show current deployment status.")
	ec2deploy        = ec2.Command("deploy", "Deploy a new application to an idling AutoScalingGroup.")
	ec2deployCleanup = ec2deploy.Flag("cleanup", "Cleanup idling AutoScalingGroup's instances.").Default("true").Bool()
	ec2rollback      = ec2.Command("rollback", "Restore the AutoScalingGroup to their original state, then swap traffic.")

	ec2cleanup    = ec2.Command("cleanup", "Cleanup idling AutoScalingGroup's instances.")
	ec2swap       = ec2.Command("swap", "Swap traffic from a running AutoScalingGroup to an idling AutoScalingGroup.")
	ec2asg        = ec2.Command("asg", "Update AutoScalingGroup.")
	ec2asgName    = ec2asg.Flag("name", "The name of AutoScalingGroup").Required().String()
	ec2asgDesired = ec2asg.Flag("desired", "DesiredCapacity").Int32()
	ec2asgMinSize = ec2asg.Flag("min", "MinSize").Int32()
	ec2asgMaxSize = ec2asg.Flag("max", "MaxSize").Int32()
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
		fmt.Println("deployman", Version, UpdatedAt)
		os.Exit(0)

	case bundleRegister.FullCommand():
		err = bundler.RegisterBundle(ctx, bundleRegisterFile, bundleRegisterName)

	case bundleList.FullCommand():
		err = bundler.ListBundles(ctx)

	case bundleUpdateDeployTarget.FullCommand():
		err = bundler.UpdateDeployBundle(ctx, bundleUpdateDeployTargetValue)

	case bundleDownload.FullCommand():
		err = bundler.DownloadBundle(ctx, bundleDownloadFile)

	case ec2status.FullCommand():
		err = deployer.ShowStatus(ctx)

	case ec2deploy.FullCommand():
		// Existence check
		if _, err = bundler.GetDeployBundle(ctx, nil, false); err != nil {
			break
		}
		err = deployer.Deploy(ctx, true, true, *ec2deployCleanup)

	case ec2rollback.FullCommand():
		if err = bundler.RollbackDeployBundle(ctx); err != nil {
			break
		}
		err = deployer.Deploy(ctx, true, false, false)

	case ec2cleanup.FullCommand():
		_, err = deployer.CleanupIdlingTarget(ctx)

	case ec2swap.FullCommand():
		err = deployer.SwapTraffic(ctx)

	case ec2asg.FullCommand():
		_, err = deployer.UpdateAutoScalingGroup(ctx, ec2asgName, ec2asgDesired, ec2asgMinSize, ec2asgMaxSize, false)

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
