package aiproviders

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func rotationService(t *testing.T) (*Service, []Provider) {
	t.Helper()
	s := NewService(&memoryRepo{}, testCipher(t), Provider{}, testLogger(io.Discard))
	var providers []Provider
	for i := 0; i < 4; i++ {
		in := input()
		in.Priority = 10
		p, err := s.Save(context.Background(), uuid.Nil, uuid.New(), in)
		if err != nil {
			t.Fatal(err)
		}
		providers = append(providers, p)
	}
	sorted, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, sorted
}
func TestPriorityLevelRotatesRequestsWithoutDuplicatingCalls(t *testing.T) {
	s, providers := rotationService(t)
	for i := 0; i < 12; i++ {
		count := 0
		winner, err := Execute(context.Background(), s, "assistant", func(_ context.Context, p Provider) (uuid.UUID, error) { count++; return p.ID, nil })
		if err != nil || count != 1 {
			t.Fatalf("request %d made %d provider calls; err=%v", i, count, err)
		}
		if winner != providers[i%len(providers)].ID {
			t.Fatalf("request %d did not rotate to expected provider", i)
		}
	}
}
func TestRotationConcurrentRequestsAreBalancedAndNeverFanOut(t *testing.T) {
	s, providers := rotationService(t)
	var mu sync.Mutex
	counts := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			calls := 0
			_, err := Execute(context.Background(), s, "writing", func(_ context.Context, p Provider) (string, error) {
				calls++
				mu.Lock()
				counts[p.ID]++
				mu.Unlock()
				return "valid", nil
			})
			if err != nil || calls != 1 {
				t.Errorf("one user request fanned out: calls=%d err=%v", calls, err)
			}
		}()
	}
	wg.Wait()
	for _, p := range providers {
		if counts[p.ID] != 10 {
			t.Fatalf("unbalanced rotation: %d calls", counts[p.ID])
		}
	}
}
