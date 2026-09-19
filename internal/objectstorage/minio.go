package objectstorage

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

type MinIOStore struct {
	client *minio.Client
	bucket string
	region string
}

func NewMinIOStore(config MinIOConfig) (*MinIOStore, error) {
	if strings.TrimSpace(config.Endpoint) == "" || strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" || strings.TrimSpace(config.Bucket) == "" {
		return nil, errors.New("MinIO endpoint, credentials and bucket are required")
	}
	client, err := minio.New(strings.TrimSpace(config.Endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey), ""),
		Secure: config.UseSSL,
		Region: strings.TrimSpace(config.Region),
	})
	if err != nil {
		return nil, err
	}
	return &MinIOStore{client: client, bucket: strings.TrimSpace(config.Bucket), region: strings.TrimSpace(config.Region)}, nil
}

func (s *MinIOStore) Put(ctx context.Context, key, contentType string, source io.Reader, size int64) (int64, error) {
	key, err := cleanObjectKey(key)
	if err != nil {
		return 0, err
	}
	if err := s.ensureBucket(ctx); err != nil {
		return 0, err
	}
	info, err := s.client.PutObject(ctx, s.bucket, key, source, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return info.Size, err
	}
	if info.Size != size {
		_ = s.client.RemoveObject(context.WithoutCancel(ctx), s.bucket, key, minio.RemoveObjectOptions{})
		return info.Size, errors.New("uploaded object size mismatch")
	}
	return info.Size, nil
}

func (s *MinIOStore) Open(ctx context.Context, key string) (ReadSeekCloser, error) {
	key, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy; Stat turns missing objects and authorization failures
	// into an error before the HTTP handler starts writing a response.
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		return nil, err
	}
	return object, nil
}

func (s *MinIOStore) Delete(ctx context.Context, key string) error {
	key, err := cleanObjectKey(key)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *MinIOStore) Check(ctx context.Context) error {
	return s.ensureBucket(ctx)
}

func (s *MinIOStore) ensureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			exists, checkErr := s.client.BucketExists(ctx, s.bucket)
			if checkErr != nil || !exists {
				return err
			}
		}
	}
	return nil
}

func cleanObjectKey(key string) (string, error) {
	key = strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(key), "\\", "/"), "/")
	clean := path.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("invalid object key")
	}
	return clean, nil
}
