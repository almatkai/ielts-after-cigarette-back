package aiproviders

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/almatkai/ielts-after-cigarette-back/internal/testdb"
	"github.com/google/uuid"
)

func TestPostgresEncryptionOrderingRevisionAndRotation(t *testing.T) {
	pool := testdb.Open(t)
	actor := testdb.User(t, pool)
	ctx := context.Background()
	repo := NewRepository(pool)
	service := NewService(repo, testCipher(t), Provider{}, testLogger(io.Discard))
	late := input()
	late.Priority = 200
	first, err := service.Save(ctx, uuid.Nil, actor, late)
	if err != nil {
		t.Fatal(err)
	}
	early := input()
	early.Priority = 10
	early.Name = "Early"
	second, err := service.Save(ctx, uuid.Nil, actor, early)
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != 2 || items[0].ID != second.ID {
		t.Fatalf("priority order: %+v %v", items, err)
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT key_ciphertext FROM ai_providers WHERE id=$1`, first.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(late.APIKey)) {
		t.Fatal("plaintext in database")
	}
	if plain, err := service.cipher.Open(ciphertext, first.aad()); err != nil || plain != late.APIKey {
		t.Fatal("database key cannot decrypt")
	}
	rotated, _ := NewCipher(encryptionKey(2), encryptionKey(1))
	service.cipher = rotated
	update := late
	update.APIKey = ""
	update.Revision = first.Revision
	updated, err := service.Save(ctx, first.ID, actor, update)
	if err != nil || updated.Revision != 2 {
		t.Fatal("rotation update failed", err)
	}
	currentOnly, _ := NewCipher(encryptionKey(2), "")
	if plain, err := currentOnly.Open(updated.Ciphertext, updated.aad()); err != nil || plain != late.APIKey {
		t.Fatal("rotation did not re-encrypt existing secret")
	}
	if _, err := service.Save(ctx, first.ID, actor, update); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update accepted")
	}
	if err := service.Delete(ctx, first.ID, first.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete accepted")
	}
	if err := service.Delete(ctx, first.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCreateCannotExceedChainCapacity(t *testing.T) {
	pool := testdb.Open(t)
	actor := testdb.User(t, pool)
	ctx := context.Background()
	service := NewService(NewRepository(pool), testCipher(t), Provider{}, testLogger(io.Discard))
	var group sync.WaitGroup
	failures := make(chan error, 16)
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.Save(ctx, uuid.Nil, actor, input())
			if err != nil && !errors.Is(err, ErrValidation) {
				failures <- err
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != MaxChain {
		t.Fatalf("capacity=%d err=%v", len(items), err)
	}
}
