package objectstorage

import (
	"context"
	"io"
)

// ReadSeekCloser is the minimum contract needed by http.ServeContent and the
// audio assessment pipeline. Both os.File and minio.Object implement it.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// Store keeps private media objects. Authorization remains in the HTTP/service
// layer; callers never receive a public bucket URL.
type Store interface {
	Put(ctx context.Context, key, contentType string, source io.Reader, size int64) (int64, error)
	Open(ctx context.Context, key string) (ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
	Check(ctx context.Context) error
}
