package internal

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	JST = time.FixedZone("Asia/Tokyo", 9*60*60)
)

const (
	BundlePrefix             string = "bundles/"
	ActivatedBundleKeyPrefix string = "activated_bundle_"
	MaxKeepBundles           int    = 100
)

type Bundler struct {
	s3     *s3.Client
	region *string
	config *Config
	logger Logger
}

type BundleInfo struct {
	Value        *string
	LastModified *time.Time
}

func NewBundler(awsRegion *string, awsConfig *aws.Config, deployConfig *Config, logger Logger) *Bundler {
	return &Bundler{
		region: awsRegion,
		s3:     s3.NewFromConfig(*awsConfig),
		config: deployConfig,
		logger: logger,
	}
}

func (b *Bundler) listBundles(ctx context.Context, bucket *string) (*[]s3Types.Object, error) {
	output, err := b.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: bucket,
		Prefix: aws.String(BundlePrefix),
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// desc sort
	sort.Slice(output.Contents, func(i, j int) bool {
		return output.Contents[i].LastModified.After(*output.Contents[j].LastModified)
	})

	return &output.Contents, nil
}

func (b *Bundler) ListBundles(ctx context.Context) error {
	blueBundle, err := b.getActivatedBundle(ctx, BlueTargetType)
	if err != nil {
		return err
	}

	greenBundle, err := b.getActivatedBundle(ctx, GreenTargetType)
	if err != nil {
		return err
	}

	bundleObjects, err := b.listBundles(ctx, &b.config.BundleBucket)
	if err != nil {
		return err
	}

	var data [][]string
	for i, bundleObject := range *bundleObjects {
		status := ""
		if strings.Contains(*bundleObject.Key, *blueBundle.Value) {
			status = "activated:blue"
		} else if strings.Contains(*bundleObject.Key, *greenBundle.Value) {
			status = "activated:green"
		}

		data = append(data, []string{
			strconv.Itoa(i + 1),
			bundleObject.LastModified.In(JST).Format(time.RFC3339),
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

func (b *Bundler) Register(ctx context.Context, uploadFile *string, bundleName *string) error {
	createBucketIfNotExsists := func() error {
		_, err := b.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &b.config.BundleBucket})
		var apiErr smithy.APIError
		if err != nil {
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NotFound" {
				_, err := b.s3.CreateBucket(ctx, &s3.CreateBucketInput{
					Bucket: &b.config.BundleBucket,
					CreateBucketConfiguration: &s3Types.CreateBucketConfiguration{
						LocationConstraint: s3Types.BucketLocationConstraint(*b.region),
					},
				})
				if err != nil {
					return errors.WithStack(err)
				}

				_, err = b.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
					Bucket: &b.config.BundleBucket,
					VersioningConfiguration: &s3Types.VersioningConfiguration{
						Status: s3Types.BucketVersioningStatusEnabled,
					},
				})
				if err != nil {
					return errors.WithStack(err)
				}

				_, err = b.s3.PutBucketAcl(ctx, &s3.PutBucketAclInput{
					Bucket: &b.config.BundleBucket,
					ACL:    s3Types.BucketCannedACLPrivate,
				})
				if err != nil {
					return errors.WithStack(err)
				}

				_, err = b.s3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
					Bucket: &b.config.BundleBucket,
					PublicAccessBlockConfiguration: &s3Types.PublicAccessBlockConfiguration{
						BlockPublicAcls:       true,
						BlockPublicPolicy:     true,
						IgnorePublicAcls:      true,
						RestrictPublicBuckets: true,
					},
				})
				if err != nil {
					return errors.WithStack(err)
				}
			}
		}

		return nil
	}

	removeOldBundlesIfNeed := func() error {
		objects, err := b.listBundles(ctx, &b.config.BundleBucket)
		if err != nil {
			return err
		}
		for i, o := range *objects {
			if i >= MaxKeepBundles-1 {
				_, err := b.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(b.config.BundleBucket),
					Key:    o.Key,
				})
				if err != nil {
					return errors.WithStack(err)
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

	file, err := os.Open(*uploadFile)
	if err != nil {
		return errors.WithStack(err)
	}

	_, err = b.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.config.BundleBucket),
		Key:    aws.String(BundlePrefix + *bundleName),
		Body:   file,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (b *Bundler) getActivatedBundle(ctx context.Context, targetType TargetType) (*BundleInfo, error) {
	output, err := b.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.config.BundleBucket),
		Key:    aws.String(ActivatedBundleKeyPrefix + string(targetType)),
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &BundleInfo{
		Value:        aws.String(buf.String()),
		LastModified: output.LastModified,
	}, nil
}

func (b *Bundler) Activate(ctx context.Context, targetType TargetType, bundleValue *string) error {
	_, err := b.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.config.BundleBucket),
		Key:         aws.String(ActivatedBundleKeyPrefix + string(targetType)),
		ContentType: aws.String("text/plain"),
		Body:        strings.NewReader(*bundleValue),
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (b *Bundler) Download(ctx context.Context, targetType TargetType) error {
	bundle, err := b.getActivatedBundle(ctx, targetType)
	if err != nil {
		return err
	}

	output, err := b.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.config.BundleBucket,
		Key:    aws.String(BundlePrefix + *bundle.Value),
	})
	if err != nil {
		return errors.WithStack(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return errors.WithStack(err)
	}

	err = os.WriteFile(*bundle.Value, buf.Bytes(), 0755)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
