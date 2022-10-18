package internal

import (
	"bytes"
	"context"
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
	BundlePrefix   string = "bundles/"
	DeployBundle   string = "deploy_bundle"
	MaxKeepBundles int    = 100
)

type Bundler struct {
	s3     *s3.Client
	region *string
	config *Config
	logger Logger
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

func (b *Bundler) RegisterBundle(ctx context.Context, uploadFile *string, bundleName *string) error {
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

	err = b.UpdateDeployBundle(ctx, bundleName)
	if err != nil {
		return err
	}

	return nil
}

func (b *Bundler) ListBundles(ctx context.Context) error {
	deployBundle, err := b.GetDeployBundle(ctx, nil, false)
	if err != nil {
		return err
	}

	objects, err := b.listBundles(ctx, &b.config.BundleBucket)
	if err != nil {
		return err
	}

	var data [][]string
	for i, object := range *objects {
		currentlyInUse := ""
		if strings.Contains(*object.Key, *deployBundle) {
			currentlyInUse = "running"
		}

		data = append(data, []string{
			strconv.Itoa(i + 1),
			object.LastModified.In(JST).Format(time.RFC3339),
			*object.Key,
			currentlyInUse,
		})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"#", "last updated", "bundle name", "usage"})
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (b *Bundler) GetDeployBundle(ctx context.Context, versionId *string, showOutput bool) (*string, error) {
	output, err := b.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(b.config.BundleBucket),
		Key:       aws.String(DeployBundle),
		VersionId: versionId,
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	deployBundle := buf.String()

	if showOutput {
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{DeployBundle, "last updated"})
		table.AppendBulk([][]string{{deployBundle, output.LastModified.In(JST).Format(time.RFC3339)}})
		table.Render()
	}

	return &deployBundle, nil
}

func (b *Bundler) RollbackDeployBundle(ctx context.Context) error {
	versionOutput, err := b.s3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket:  aws.String(b.config.BundleBucket),
		Prefix:  aws.String(DeployBundle),
		MaxKeys: 2,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	if len(versionOutput.Versions) <= 1 {
		return errors.Errorf("The process cannot continue because the previous deployment history does not exist. bucket: %s", b.config.BundleBucket)
	}

	previousVersionId := versionOutput.Versions[1].VersionId
	deployBundle, err := b.GetDeployBundle(ctx, previousVersionId, false)
	if err != nil {
		return err
	}

	return b.UpdateDeployBundle(ctx, deployBundle)
}

func (b *Bundler) UpdateDeployBundle(ctx context.Context, deployBundle *string) error {
	_, err := b.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.config.BundleBucket),
		Key:         aws.String(DeployBundle),
		ContentType: aws.String("text/plain"),
		Body:        strings.NewReader(*deployBundle),
	})
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (b *Bundler) DownloadBundle(ctx context.Context, fileName *string) error {
	deployBundle, err := b.GetDeployBundle(ctx, nil, false)

	output, err := b.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.config.BundleBucket,
		Key:    aws.String(BundlePrefix + *deployBundle),
	})
	if err != nil {
		return errors.WithStack(err)
	}

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return errors.WithStack(err)
	}

	writeFileName := *deployBundle
	if len(*fileName) > 0 {
		writeFileName = *fileName
	}

	err = os.WriteFile(writeFileName, buf.Bytes(), 0755)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
