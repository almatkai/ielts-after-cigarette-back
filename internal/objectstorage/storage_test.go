package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	store := NewFileStore(t.TempDir())
	if _, err := store.Open(context.Background(), "listening/missing.mp3"); !IsNotFound(err) {
		t.Fatalf("missing file: IsNotFound = false, err = %v", err)
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("boom"), false},
		{"minio 404", minio.ErrorResponse{StatusCode: http.StatusNotFound}, true},
		{"minio NoSuchKey", minio.ErrorResponse{Code: "NoSuchKey"}, true},
		{"minio NoSuchBucket", minio.ErrorResponse{Code: "NoSuchBucket"}, true},
		{"minio AccessDenied", minio.ErrorResponse{Code: "AccessDenied", StatusCode: http.StatusForbidden}, false},
		{"minio internal", minio.ErrorResponse{Code: "InternalError", StatusCode: http.StatusInternalServerError}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsNotFound(tc.err); got != tc.want {
				t.Fatalf("IsNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
