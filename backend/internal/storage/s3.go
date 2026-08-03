package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	httptransport "github.com/aws/smithy-go/transport/http"
)

type S3ObjectStore struct {
	client *s3.Client
	bucket string
}

type S3Configuration interface {
	ObjectStorageEndpoint() string
	ObjectStorageBucket() string
	ObjectStorageAccessKey() string
	ObjectStorageSecretKey() string
	ObjectStorageUsePathStyle() bool
}

type awsConfigLoader func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error)

var _ ObjectStore = (*S3ObjectStore)(nil)

func NewS3ObjectStore(cfg S3Configuration) (*S3ObjectStore, error) {
	return newS3ObjectStore(cfg, awsconfig.LoadDefaultConfig)
}

func newS3ObjectStore(cfg S3Configuration, load awsConfigLoader) (*S3ObjectStore, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               cfg.ObjectStorageEndpoint(),
				HostnameImmutable: cfg.ObjectStorageUsePathStyle(),
			}, nil
		},
	)

	awsCfg, err := load(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.ObjectStorageAccessKey(), cfg.ObjectStorageSecretKey(), "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("S3 object-store construction failed because the AWS SDK configuration could not be loaded in storage.NewS3ObjectStore during pre-dispatch dependency composition; transcript serving and blob jobs cannot start; correct the AWS/S3 SDK environment or shared configuration and retry: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ObjectStorageUsePathStyle()
	})

	return &S3ObjectStore{client: client, bucket: cfg.ObjectStorageBucket()}, nil
}

func (s *S3ObjectStore) Put(ctx context.Context, key ObjectKey, body []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(string(key)),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("object upload failed because the S3-compatible provider rejected the request in storage.S3ObjectStore.Put during ciphertext persistence; the object was not confirmed stored; verify endpoint, credentials, bucket, and connectivity, then retry: %w", err)
	}
	return nil
}

func (s *S3ObjectStore) Get(ctx context.Context, key ObjectKey) ([]byte, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(string(key))})
	if err != nil {
		if isS3NotFound(err) {
			return nil, "", fmt.Errorf("object read failed because the immutable generation does not exist in storage.S3ObjectStore.Get during ciphertext retrieval; the caller may reload an authorized changed descriptor once; reconcile the opaque object key if it is unchanged: %w", ErrObjectNotFound)
		}
		return nil, "", fmt.Errorf("object read failed because the S3-compatible provider rejected or could not complete the request in storage.S3ObjectStore.Get during ciphertext retrieval; no plaintext can be returned; verify provider access and connectivity without retrying as absence: %w", err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", fmt.Errorf("object read failed because the ciphertext response body could not be fully read in storage.S3ObjectStore.Get during retrieval; no plaintext can be returned; restore connectivity and retry: %w", err)
	}
	return body, aws.ToString(out.ContentType), nil
}

func isS3NotFound(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode() == "NoSuchKey"
	}
	var response *httptransport.ResponseError
	return errors.As(err, &response) && response.HTTPStatusCode() == 404
}

func (s *S3ObjectStore) Delete(ctx context.Context, key ObjectKey) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(string(key)),
	})
	if err != nil {
		return fmt.Errorf("object deletion failed because the S3-compatible provider rejected the request in storage.S3ObjectStore.Delete during ciphertext cleanup; encrypted bytes may remain; verify provider access and retry cleanup: %w", err)
	}
	return nil
}
