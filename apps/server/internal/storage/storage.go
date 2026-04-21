// Package storage wraps the S3-compatible object store used for artifacts.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ErrNotFound is returned when an object key does not exist.
var ErrNotFound = errors.New("object not found")

// ObjectMeta is metadata returned by Get.
type ObjectMeta struct {
	ContentType  string
	SizeBytes    int64
	ETag         string
	LastModified time.Time
}

// ObjectStore is a thin interface over the parts of S3/MinIO the server uses.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, contentType string, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error)
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Config captures the S3/MinIO wiring knobs.
type Config struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
}

// New builds an ObjectStore backed by aws-sdk-go-v2.
func New(ctx context.Context, cfg Config) (ObjectStore, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

type s3Store struct {
	client *s3.Client
	bucket string
}

func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, contentType string, size int64) error {
	// Single PutObject is fine for our per-entry cap (≤ 100 MiB by default;
	// S3 allows up to 5 GiB in one call). Multipart-via-transfermanager is
	// available behind the same interface if we ever need it.
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	meta := ObjectMeta{}
	if out.ContentType != nil {
		meta.ContentType = *out.ContentType
	}
	if out.ContentLength != nil {
		meta.SizeBytes = *out.ContentLength
	}
	if out.ETag != nil {
		meta.ETag = *out.ETag
	}
	if out.LastModified != nil {
		meta.LastModified = *out.LastModified
	}
	return out.Body, meta, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *s3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	ps := s3.NewPresignClient(s.client)
	req, err := ps.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}
