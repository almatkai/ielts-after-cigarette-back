package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FileStore struct {
	root string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{root: filepath.Clean(strings.TrimSpace(root))}
}

func (s *FileStore) Put(_ context.Context, key, _ string, source io.Reader, size int64) (int64, error) {
	target, err := s.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return 0, err
	}
	temporary := target + ".upload"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != size {
		_ = os.Remove(temporary)
		switch {
		case copyErr != nil:
			return written, copyErr
		case closeErr != nil:
			return written, closeErr
		default:
			return written, fmt.Errorf("object size mismatch: wrote %d bytes, expected %d", written, size)
		}
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return written, err
	}
	return written, nil
}

func (s *FileStore) Open(_ context.Context, key string) (ReadSeekCloser, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *FileStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *FileStore) Check(_ context.Context) error {
	if s.root == "" || s.root == "." {
		return errors.New("filesystem object storage root is empty")
	}
	return os.MkdirAll(s.root, 0o750)
}

func (s *FileStore) path(key string) (string, error) {
	if s.root == "" || s.root == "." {
		return "", errors.New("filesystem object storage root is empty")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(key)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid object key")
	}
	return filepath.Join(s.root, clean), nil
}
