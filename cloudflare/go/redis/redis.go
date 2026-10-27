// Package redis maps the selected application's Redis operations to native DO storage.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	"strconv"
	"time"
)

var Nil = errors.New("redis: nil")

type Options struct {
	DialTimeout, ReadTimeout, WriteTimeout, PoolTimeout time.Duration
	ContextTimeoutEnabled                               bool
	MaxRetries                                          int
}

func ParseURL(string) (*Options, error) { return &Options{}, nil }

type Client struct{}

func NewClient(*Options) *Client { return &Client{} }
func (*Client) Close() error     { return nil }

type Z struct {
	Score  float64
	Member any
}
type ZRangeBy struct {
	Min, Max      string
	Offset, Count int64
}
type cmd[T any] struct {
	v T
	e error
}

func (c *cmd[T]) Result() (T, error) { return c.v, c.e }
func (c *cmd[T]) Val() T             { return c.v }
func (c *cmd[T]) Err() error         { return c.e }

type IntCmd = cmd[int64]
type BoolCmd = cmd[bool]
type StatusCmd = cmd[string]
type FloatCmd = cmd[float64]
type SliceCmd = cmd[[]any]
type StringSliceCmd = cmd[[]string]
type ZSliceCmd = cmd[[]Z]
type StringCmd struct{ cmd[string] }

func (c *StringCmd) Int64() (int64, error) {
	if c.e != nil {
		return 0, c.e
	}
	return strconv.ParseInt(c.v, 10, 64)
}
func (c *StringCmd) Bytes() ([]byte, error) { return []byte(c.v), c.e }

type Cmder interface{ Err() error }

func run[T any](ctx context.Context, op string, args ...any) *cmd[T] {
	var v *T
	e := bridge.Call(ctx, "redis", map[string]any{"op": op, "args": args}, &v)
	if e == nil && v == nil {
		e = Nil
	}
	c := &cmd[T]{e: e}
	if v != nil {
		c.v = *v
	}
	return c
}
func str(ctx context.Context, op string, args ...any) *StringCmd {
	return &StringCmd{*run[string](ctx, op, args...)}
}
func value(v any) string {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprint(v)
	}
}
func (*Client) Pipeline() *Pipeline { return &Pipeline{} }

type Pipeline struct{ pending []func() Cmder }

func (p *Pipeline) Len() int { return len(p.pending) }
func (p *Pipeline) Exec(context.Context) ([]Cmder, error) {
	a := []Cmder{}
	var e error
	for _, f := range p.pending {
		c := f()
		a = append(a, c)
		if c.Err() != nil && e == nil {
			e = c.Err()
		}
	}
	p.pending = nil
	return a, e
}

type Script struct{ s string }

func NewScript(s string) *Script { return &Script{s} }

type Cmd struct{ cmd[json.RawMessage] }

func (c *Cmd) Int64() (int64, error) {
	var n int64
	e := json.Unmarshal(c.v, &n)
	if c.e != nil {
		e = c.e
	}
	return n, e
}
func (c *Cmd) Slice() ([]any, error) {
	var a []int64
	e := json.Unmarshal(c.v, &a)
	r := make([]any, len(a))
	for i, n := range a {
		r[i] = n
	}
	if c.e != nil {
		e = c.e
	}
	return r, e
}
func (s *Script) Run(ctx context.Context, c *Client, keys []string, args ...any) *Cmd {
	return &Cmd{*run[json.RawMessage](ctx, "script", s.s, keys, args)}
}

type ScanCmd struct{ cmd[[]string] }
type ScanIterator struct {
	keys []string
	i    int
	e    error
}

func (c *ScanCmd) Iterator() *ScanIterator            { return &ScanIterator{keys: c.v, i: -1, e: c.e} }
func (i *ScanIterator) Next(context.Context) bool     { i.i++; return i.i < len(i.keys) }
func (i *ScanIterator) Val() string                   { return i.keys[i.i] }
func (i *ScanIterator) Err() error                    { return i.e }
func (c *Client) Ping(ctx context.Context) *StatusCmd { return run[string](ctx, "Ping") }
func (p *Pipeline) Ping(ctx context.Context) *StatusCmd {
	result := &StatusCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[string](ctx, "Ping"); *result = *x; return result })
	return result
}
func (c *Client) Get(ctx context.Context, key string) *StringCmd { return str(ctx, "Get", key) }
func (p *Pipeline) Get(ctx context.Context, key string) *StringCmd {
	result := &StringCmd{}
	p.pending = append(p.pending, func() Cmder { x := str(ctx, "Get", key); *result = *x; return result })
	return result
}
func (c *Client) Set(ctx context.Context, key string, v any, ttl time.Duration) *StatusCmd {
	return run[string](ctx, "Set", key, value(v), ttl.Milliseconds())
}
func (p *Pipeline) Set(ctx context.Context, key string, v any, ttl time.Duration) *StatusCmd {
	result := &StatusCmd{}
	p.pending = append(p.pending, func() Cmder {
		x := run[string](ctx, "Set", key, value(v), ttl.Milliseconds())
		*result = *x
		return result
	})
	return result
}
func (c *Client) SetNX(ctx context.Context, key string, v any, ttl time.Duration) *BoolCmd {
	return run[bool](ctx, "SetNX", key, value(v), ttl.Milliseconds())
}
func (p *Pipeline) SetNX(ctx context.Context, key string, v any, ttl time.Duration) *BoolCmd {
	result := &BoolCmd{}
	p.pending = append(p.pending, func() Cmder {
		x := run[bool](ctx, "SetNX", key, value(v), ttl.Milliseconds())
		*result = *x
		return result
	})
	return result
}
func (c *Client) Del(ctx context.Context, keys ...string) *IntCmd {
	return run[int64](ctx, "Del", keys)
}
func (p *Pipeline) Del(ctx context.Context, keys ...string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "Del", keys); *result = *x; return result })
	return result
}
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) *BoolCmd {
	return run[bool](ctx, "Expire", key, ttl.Milliseconds())
}
func (p *Pipeline) Expire(ctx context.Context, key string, ttl time.Duration) *BoolCmd {
	result := &BoolCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[bool](ctx, "Expire", key, ttl.Milliseconds()); *result = *x; return result })
	return result
}
func (c *Client) Incr(ctx context.Context, key string) *IntCmd { return run[int64](ctx, "Incr", key) }
func (p *Pipeline) Incr(ctx context.Context, key string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "Incr", key); *result = *x; return result })
	return result
}
func (c *Client) MGet(ctx context.Context, keys ...string) *SliceCmd {
	return run[[]any](ctx, "MGet", keys)
}
func (p *Pipeline) MGet(ctx context.Context, keys ...string) *SliceCmd {
	result := &SliceCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[[]any](ctx, "MGet", keys); *result = *x; return result })
	return result
}
func (c *Client) LPush(ctx context.Context, key string, v ...any) *IntCmd {
	return run[int64](ctx, "LPush", key, v)
}
func (p *Pipeline) LPush(ctx context.Context, key string, v ...any) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "LPush", key, v); *result = *x; return result })
	return result
}
func (c *Client) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *StringSliceCmd {
	return run[[]string](ctx, "BRPop", keys, timeout.Milliseconds())
}
func (p *Pipeline) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *StringSliceCmd {
	result := &StringSliceCmd{}
	p.pending = append(p.pending, func() Cmder {
		x := run[[]string](ctx, "BRPop", keys, timeout.Milliseconds())
		*result = *x
		return result
	})
	return result
}
func (c *Client) ZAdd(ctx context.Context, key string, z ...Z) *IntCmd {
	return run[int64](ctx, "ZAdd", key, z)
}
func (p *Pipeline) ZAdd(ctx context.Context, key string, z ...Z) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "ZAdd", key, z); *result = *x; return result })
	return result
}
func (c *Client) ZRem(ctx context.Context, key string, members ...any) *IntCmd {
	return run[int64](ctx, "ZRem", key, members)
}
func (p *Pipeline) ZRem(ctx context.Context, key string, members ...any) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "ZRem", key, members); *result = *x; return result })
	return result
}
func (c *Client) ZRemRangeByScore(ctx context.Context, key, min, max string) *IntCmd {
	return run[int64](ctx, "ZRemRangeByScore", key, min, max)
}
func (p *Pipeline) ZRemRangeByScore(ctx context.Context, key, min, max string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "ZRemRangeByScore", key, min, max); *result = *x; return result })
	return result
}
func (c *Client) ZRangeByScore(ctx context.Context, key string, r *ZRangeBy) *StringSliceCmd {
	return run[[]string](ctx, "ZRangeByScore", key, r)
}
func (p *Pipeline) ZRangeByScore(ctx context.Context, key string, r *ZRangeBy) *StringSliceCmd {
	result := &StringSliceCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[[]string](ctx, "ZRangeByScore", key, r); *result = *x; return result })
	return result
}
func (c *Client) ZCount(ctx context.Context, key, min, max string) *IntCmd {
	return run[int64](ctx, "ZCount", key, min, max)
}
func (p *Pipeline) ZCount(ctx context.Context, key, min, max string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "ZCount", key, min, max); *result = *x; return result })
	return result
}
func (c *Client) ZIncrBy(ctx context.Context, key string, n float64, member string) *FloatCmd {
	return run[float64](ctx, "ZIncrBy", key, n, member)
}
func (p *Pipeline) ZIncrBy(ctx context.Context, key string, n float64, member string) *FloatCmd {
	result := &FloatCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[float64](ctx, "ZIncrBy", key, n, member); *result = *x; return result })
	return result
}
func (c *Client) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) *ZSliceCmd {
	return run[[]Z](ctx, "ZRevRangeWithScores", key, start, stop)
}
func (p *Pipeline) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) *ZSliceCmd {
	result := &ZSliceCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[[]Z](ctx, "ZRevRangeWithScores", key, start, stop); *result = *x; return result })
	return result
}
func (c *Client) HSet(ctx context.Context, key string, v ...any) *IntCmd {
	return run[int64](ctx, "HSet", key, v)
}
func (p *Pipeline) HSet(ctx context.Context, key string, v ...any) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "HSet", key, v); *result = *x; return result })
	return result
}
func (c *Client) HDel(ctx context.Context, key string, fields ...string) *IntCmd {
	return run[int64](ctx, "HDel", key, fields)
}
func (p *Pipeline) HDel(ctx context.Context, key string, fields ...string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "HDel", key, fields); *result = *x; return result })
	return result
}
func (c *Client) HMGet(ctx context.Context, key string, fields ...string) *SliceCmd {
	return run[[]any](ctx, "HMGet", key, fields)
}
func (p *Pipeline) HMGet(ctx context.Context, key string, fields ...string) *SliceCmd {
	result := &SliceCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[[]any](ctx, "HMGet", key, fields); *result = *x; return result })
	return result
}
func (c *Client) PFAdd(ctx context.Context, key string, v ...any) *IntCmd {
	return run[int64](ctx, "PFAdd", key, v)
}
func (p *Pipeline) PFAdd(ctx context.Context, key string, v ...any) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "PFAdd", key, v); *result = *x; return result })
	return result
}
func (c *Client) PFCount(ctx context.Context, keys ...string) *IntCmd {
	return run[int64](ctx, "PFCount", keys)
}
func (p *Pipeline) PFCount(ctx context.Context, keys ...string) *IntCmd {
	result := &IntCmd{}
	p.pending = append(p.pending, func() Cmder { x := run[int64](ctx, "PFCount", keys); *result = *x; return result })
	return result
}
func (c *Client) Scan(ctx context.Context, cursor uint64, match string, count int64) *ScanCmd {
	return &ScanCmd{*run[[]string](ctx, "Scan", match)}
}
func (p *Pipeline) Scan(ctx context.Context, cursor uint64, match string, count int64) *ScanCmd {
	result := &ScanCmd{}
	p.pending = append(p.pending, func() Cmder { x := &ScanCmd{*run[[]string](ctx, "Scan", match)}; *result = *x; return result })
	return result
}
