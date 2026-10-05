package assistant

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// One provisional stream may be visible; other candidates have private bounded
// buffers. A different validated winner replaces it with reset, never append.
// No candidate can write to the client after the gate is frozen.
type streamGate struct {
	mu      sync.Mutex
	emit    Emit
	cancel  context.CancelFunc
	active  uuid.UUID
	buffers map[uuid.UUID]*strings.Builder
	frozen  bool
	err     error
}

func newStreamGate(emit Emit, cancel context.CancelFunc) *streamGate {
	return &streamGate{emit: emit, cancel: cancel, buffers: map[uuid.UUID]*strings.Builder{}}
}
func (g *streamGate) send(e StreamEvent) error {
	if g.err != nil {
		return g.err
	}
	if err := g.emit(e); err != nil {
		g.err = err
		g.cancel()
		return err
	}
	return nil
}
func (g *streamGate) emitter(ctx context.Context, id uuid.UUID) Emit {
	return func(e StreamEvent) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if g.frozen {
			return context.Canceled
		}
		buffer := g.buffers[id]
		if buffer == nil {
			buffer = &strings.Builder{}
			g.buffers[id] = buffer
		}
		if e.Type == "reset" {
			buffer.Reset()
			if g.active == id {
				return g.send(e)
			}
			return nil
		}
		if e.Type != "delta" {
			return nil
		}
		if buffer.Len()+len(e.Text) > 128<<10 {
			return ErrAIChatFailed
		}
		buffer.WriteString(e.Text)
		if g.active == uuid.Nil {
			g.active = id
			if err := g.send(StreamEvent{Type: "reset"}); err != nil {
				return err
			}
			return g.send(StreamEvent{Type: "delta", Text: buffer.String()})
		}
		if g.active == id {
			return g.send(e)
		}
		return nil
	}
}
func (g *streamGate) failed(id uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.frozen && g.active == id {
		g.active = uuid.Nil
		g.send(StreamEvent{Type: "reset"})
	}
}
func (g *streamGate) commit(id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.frozen = true
	if g.err != nil {
		return g.err
	}
	if g.active != id {
		if err := g.send(StreamEvent{Type: "reset"}); err != nil {
			return err
		}
		if buffer := g.buffers[id]; buffer != nil && buffer.Len() > 0 {
			return g.send(StreamEvent{Type: "delta", Text: buffer.String()})
		}
	}
	return nil
}
func (g *streamGate) close() { g.mu.Lock(); g.frozen = true; g.mu.Unlock() }
