package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/minio/minio-go/v7"
)

// ReadSeekCloser is the minimum contract needed by http.ServeContent and the
// audio assessment pipeline. Both os.File and minio.Object implement it.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// IsNotFound reports whether opening an object failed because the object
// itself is absent from the bucket or directory (as opposed to credentials,
// network, or configuration failures, which must keep surfacing as 5xx).
// Handlers use it to answer 404 instead of 500 when the database row exists
// but the file was never uploaded to this environment's storage.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		if response.StatusCode == http.StatusNotFound {
			return true
		}
		switch response.Code {
		case "NoSuchKey", "NoSuchBucket", "NoSuchUpload", "NoSuchObject":
			return true
		}
	}
	return false
}

// Store keeps private media objects. Authorization remains in the HTTP/service
// layer; callers never receive a public bucket URL.
type Store interface {
	Put(ctx context.Context, key, contentType string, source io.Reader, size int64) (int64, error)
	Open(ctx context.Context, key string) (ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
	Check(ctx context.Context) error
}
