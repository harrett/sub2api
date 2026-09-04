package repository

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wei-Shaw/sub2api/internal/convlog"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
)

// ConvLogS3Store 用 S3 兼容对象存储实现 convlog.ObjectStore。
// 与备份、异步生图共用 newS3Client，行为差异只在桶与前缀。
type ConvLogS3Store struct {
	client *s3.Client
	bucket string
}

var _ convlog.ObjectStore = (*ConvLogS3Store)(nil)

// NewConvLogObjectStore 是注入 convlog 的工厂。
func NewConvLogObjectStore(ctx context.Context, cfg convlog.S3Config) (convlog.ObjectStore, error) {
	client, err := newS3Client(ctx, s3ClientParams{
		Endpoint:        cfg.Endpoint,
		Region:          cfg.Region,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		ForcePathStyle:  cfg.ForcePathStyle,
	})
	if err != nil {
		return nil, err
	}
	return &ConvLogS3Store{client: client, bucket: cfg.Bucket}, nil
}

// Put 上传一个已压缩的 JSONL 段。size 显式传入是因为 body 是文件句柄，
// SDK 需要确定长度才能避免分片签名路径（部分 S3 兼容实现不支持）。
func (s *ConvLogS3Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          body,
		ContentType:   &contentType,
		ContentLength: &size,
	})
	finish()
	if err != nil {
		return fmt.Errorf("S3 PutObject %s: %w", key, err)
	}
	return nil
}

// Get 下载一个段，供后台"查看全文"按 request_id 定位单行。
func (s *ConvLogS3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	finish := servertiming.ObserveDependency(ctx, "s3")
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	finish()
	if err != nil {
		return nil, fmt.Errorf("S3 GetObject %s: %w", key, err)
	}
	return out.Body, nil
}
