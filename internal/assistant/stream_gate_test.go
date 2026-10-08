package assistant

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestStreamGateReplacesProvisionalLoserAndFreezesOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	visible := ""
	resets := 0
	gate := newStreamGate(func(event StreamEvent) error {
		mu.Lock()
		defer mu.Unlock()
		if event.Type == "reset" {
			visible = ""
			resets++
		}
		if event.Type == "delta" {
			visible += event.Text
		}
		return nil
	}, cancel)
	slow, fast := uuid.New(), uuid.New()
	first, second := gate.emitter(ctx, slow), gate.emitter(ctx, fast)
	first(StreamEvent{Type: "delta", Text: "slow partial"})
	second(StreamEvent{Type: "delta", Text: "fast "})
	second(StreamEvent{Type: "delta", Text: "complete"})
	mu.Lock()
	if visible != "slow partial" {
		t.Fatal("parallel answers mixed before selection")
	}
	mu.Unlock()
	if err := gate.commit(fast); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if visible != "fast complete" || resets != 2 {
		t.Fatal("winner did not reset loser")
	}
	mu.Unlock()
	if err := first(StreamEvent{Type: "delta", Text: "late loser"}); err == nil {
		t.Fatal("late loser not blocked")
	}
	mu.Lock()
	defer mu.Unlock()
	if visible != "fast complete" {
		t.Fatal("loser modified committed result")
	}
}
func TestStreamGateToolResetDropsBufferedPreamble(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visible := ""
	gate := newStreamGate(func(e StreamEvent) error {
		if e.Type == "reset" {
			visible = ""
		}
		if e.Type == "delta" {
			visible += e.Text
		}
		return nil
	}, cancel)
	one, two := uuid.New(), uuid.New()
	gate.emitter(ctx, one)(StreamEvent{Type: "delta", Text: "primary"})
	emit := gate.emitter(ctx, two)
	emit(StreamEvent{Type: "delta", Text: "tool preamble"})
	emit(StreamEvent{Type: "reset"})
	emit(StreamEvent{Type: "delta", Text: "grounded answer"})
	if err := gate.commit(two); err != nil || visible != "grounded answer" {
		t.Fatal("buffered preamble leaked into final answer")
	}
}
