package objectstorage

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := NewFileStore(t.TempDir())
	source := []byte("private media")
	written, err := store.Put(context.Background(), "listening/example.mp3", "audio/mpeg", bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(source)) {
		t.Fatalf("written = %d", written)
	}
	object, err := store.Open(context.Background(), "listening/example.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	got, err := io.ReadAll(object)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("got %q", got)
	}
}

func TestFileStoreRejectsTraversal(t *testing.T) {
	t.Parallel()
	store := NewFileStore(t.TempDir())
	if _, err := store.Open(context.Background(), "../secret"); err == nil {
		t.Fatal("expected traversal error")
	}
}
