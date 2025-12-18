package internal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
)

const (
	BundlePrefix          string = "bundles/"
	ActiveBundleKeyPrefix string = "active_bundle_"
	MaxKeepBundles        int    = 100
)

type Bundler struct {
	config *Config
	client AwsClient
	logger Logger
}

type BundleInfo struct {
	Value        string
	LastModified *time.Time
}

func NewBundler(deployConfig *Config, awsClient AwsClient, logger Logger) *Bundler {
	return &Bundler{
		config: deployConfig,
		client: awsClient,
		logger: logger,
	}
}

func (b *Bundler) listBundles(ctx context.Context, bucket string) ([]s3Types.Object, error) {
	objects, err := b.client.ListS3BucketObjects(ctx, bucket, BundlePrefix)
	if err != nil {
		return nil, err
	}

	// desc sort
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].LastModified.After(*objects[j].LastModified)
	})

	return objects, nil
}

func (b *Bundler) ListBundles(ctx context.Context) error {
	hasError := func(err error) bool {
		if err == nil {
			return false
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey" {
			return false
		}
		return true
	}

	blueBundle, err := b.getActiveBundle(ctx, BlueTargetType)
	if hasError(err) {
		return err
	}

	greenBundle, err := b.getActiveBundle(ctx, GreenTargetType)
	if hasError(err) {
		return err
	}

	bundleObjects, err := b.listBundles(ctx, b.config.BundleBucket)
	if err != nil {
		return err
	}

	var data [][]string
	for i, bundleObject := range bundleObjects {
		var targets []string
		if blueBundle != nil && strings.Contains(*bundleObject.Key, blueBundle.Value) {
			targets = append(targets, "blue")
		}
		if greenBundle != nil && strings.Contains(*bundleObject.Key, greenBundle.Value) {
			targets = append(targets, "green")
		}
		status := ""
		if len(targets) > 0 {
			status = "active:[" + strings.Join(targets, ", ") + "]"
		}
		location := b.config.TimeZone.CurrentLocation()
		data = append(data, []string{
			strconv.Itoa(i + 1),
			bundleObject.LastModified.In(location).Format(time.RFC3339),
			strings.Replace(*bundleObject.Key, BundlePrefix, "", 1),
			status,
		})
	}

	fmt.Printf("Bucket: %s\n", b.config.BundleBucket)
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"#", "last updated", "bundle name", "status"})
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (b *Bundler) Register(ctx context.Context, uploadFile string, bundleName string) error {
	createBucketIfNotExsists := func() error {
		err := b.client.HeadS3Bucket(ctx, b.config.BundleBucket)
		var apiErr smithy.APIError
		if err != nil {
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NotFound" {
				if err := b.client.CreateS3Bucket(ctx, b.config.BundleBucket, b.client.Region()); err != nil {
					return err
				}
				if err := b.client.EnableS3BucketVersioning(ctx, b.config.BundleBucket); err != nil {
					return err
				}
				if err := b.client.MakeS3BucketAclPrivate(ctx, b.config.BundleBucket); err != nil {
					return err
				}
				if err := b.client.DisableS3BucketPublicAccess(ctx, b.config.BundleBucket); err != nil {
					return err
				}
			}
		}

		return nil
	}

	removeOldBundlesIfNeed := func() error {
		objects, err := b.listBundles(ctx, b.config.BundleBucket)
		if err != nil {
			return err
		}
		for i, o := range objects {
			if i >= MaxKeepBundles-1 {
				if err := b.client.DeleteS3BucketObject(ctx, b.config.BundleBucket, *o.Key); err != nil {
					return err
				}
			}
		}

		return nil
	}

	if err := createBucketIfNotExsists(); err != nil {
		return err
	}

	if err := removeOldBundlesIfNeed(); err != nil {
		return err
	}

	file, err := os.Open(uploadFile)
	if err != nil {
		return errors.WithStack(err)
	}

	if err := b.client.PutS3BucketObjectAsBinaryFile(ctx, b.config.BundleBucket, BundlePrefix+bundleName, file); err != nil {
		return err
	}

	return nil
}

func (b *Bundler) getActiveBundle(ctx context.Context, targetType TargetType) (*BundleInfo, error) {
	output, err := b.client.GetS3BucketObject(ctx, b.config.BundleBucket, ActiveBundleKeyPrefix+string(targetType))
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &BundleInfo{
		Value:        buf.String(),
		LastModified: output.LastModified,
	}, nil
}

func (b *Bundler) Activate(ctx context.Context, targetType TargetType, bundleValue string) error {
	key := ActiveBundleKeyPrefix + string(targetType)
	b.logger.Info(fmt.Sprintf("'%s' registered in 's3://%s/%s'", bundleValue, b.config.BundleBucket, key))
	if err := b.client.PutS3BucketObjectAsTextFile(ctx, b.config.BundleBucket, key, bundleValue); err != nil {
		return err
	}

	return nil
}

func (b *Bundler) Download(ctx context.Context, targetType TargetType) error {
	bundle, err := b.getActiveBundle(ctx, targetType)
	if err != nil {
		return err
	}

	output, err := b.client.GetS3BucketObject(ctx, b.config.BundleBucket, BundlePrefix+bundle.Value)
	if err != nil {
		return errors.WithStack(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return errors.WithStack(err)
	}

	err = os.WriteFile(bundle.Value, buf.Bytes(), 0755)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
