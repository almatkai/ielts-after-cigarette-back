// Package pgxpool is a js/wasm-only compatibility layer. No PostgreSQL socket exists.
package pgxpool

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Pool struct{}
type Stat struct{}

func (*Pool) Stat() *Stat                    { return &Stat{} }
func (*Stat) TotalConns() int32              { return 1 }
func (*Stat) AcquiredConns() int32           { return 1 }
func (*Stat) MaxConns() int32                { return 1 }
func (*Pool) Close()                         {}
func (*Pool) Ping(ctx context.Context) error { return bridge.Call(ctx, "ping", nil, nil) }

type result struct {
	Rows    [][]json.RawMessage `json:"rows"`
	Columns []string            `json:"columns"`
	Changes int64               `json:"changes"`
}

func execute(ctx context.Context, q string, args []any) (result, error) {
	a := make([]any, len(args))
	for i, v := range args {
		a[i] = bridge.Normalize(v)
	}
	var r result
	err := bridge.Call(ctx, "sql", map[string]any{"sql": q, "args": a}, &r)
	var be *bridge.Error
	if errors.As(err, &be) && be.Code != "" {
		err = &pgconn.PgError{Code: be.Code, ConstraintName: be.Constraint, Message: be.Message}
	}
	return r, err
}
func (p *Pool) Exec(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	r, e := execute(ctx, q, args)
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", r.Changes)), e
}
func (p *Pool) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	r, e := execute(ctx, q, args)
	return &rows{r: r, err: e, i: -1}, e
}
func (p *Pool) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	r, e := execute(ctx, q, args)
	return &row{r, e}
}
func (p *Pool) Begin(ctx context.Context) (pgx.Tx, error) { return p.BeginTx(ctx, pgx.TxOptions{}) }
func (p *Pool) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	var id string
	e := bridge.Call(ctx, "begin", nil, &id)
	return &tx{Pool: p, id: id}, e
}

type tx struct {
	*Pool
	id     string
	closed bool
}

func (t *tx) Commit(ctx context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	return bridge.Call(ctx, "commit", t.id, nil)
}
func (t *tx) Rollback(ctx context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	return bridge.Call(ctx, "rollback", t.id, nil)
}
func (t *tx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested SQL transactions are not supported")
}
func (t *tx) Conn() *pgx.Conn                { return nil }
func (t *tx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (t *tx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("prepared statements unsupported")
}
func (t *tx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("COPY FROM unsupported")
}

type row struct {
	r   result
	err error
}

func (r *row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(r.r.Rows) == 0 {
		return pgx.ErrNoRows
	}
	return scan(r.r.Rows[0], dest)
}

type rows struct {
	r      result
	err    error
	i      int
	closed bool
}

func (r *rows) Close()     { r.closed = true }
func (r *rows) Err() error { return r.err }
func (r *rows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.i++
	if r.i >= len(r.r.Rows) {
		r.Close()
		return false
	}
	return true
}
func (r *rows) Scan(dest ...any) error {
	if r.i < 0 || r.i >= len(r.r.Rows) {
		return errors.New("Scan without Next")
	}
	e := scan(r.r.Rows[r.i], dest)
	if e != nil {
		r.err = e
		r.Close()
	}
	return e
}
func (r *rows) Values() ([]any, error) {
	a := []any{}
	for _, v := range r.r.Rows[r.i] {
		var x any
		json.Unmarshal(v, &x)
		a = append(a, x)
	}
	return a, nil
}
func (r *rows) RawValues() [][]byte {
	a := [][]byte{}
	for _, v := range r.r.Rows[r.i] {
		a = append(a, []byte(v))
	}
	return a
}
func (r *rows) Conn() *pgx.Conn { return nil }
func (r *rows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", r.r.Changes))
}
func (r *rows) FieldDescriptions() []pgconn.FieldDescription {
	a := []pgconn.FieldDescription{}
	for _, n := range r.r.Columns {
		a = append(a, pgconn.FieldDescription{Name: n})
	}
	return a
}

func scan(values []json.RawMessage, dest []any) error {
	if len(values) != len(dest) {
		return fmt.Errorf("scan columns %d != destinations %d", len(values), len(dest))
	}
	for i, d := range dest {
		if d == nil {
			continue
		}
		if err := assign(reflect.ValueOf(d), values[i]); err != nil {
			return fmt.Errorf("column %d: %w", i, err)
		}
	}
	return nil
}
func assign(p reflect.Value, b json.RawMessage) error {
	if p.Kind() != reflect.Pointer || p.IsNil() {
		return errors.New("scan destination must be pointer")
	}
	if string(b) == "null" {
		p.Elem().SetZero()
		return nil
	}
	if p.Elem().Kind() == reflect.Pointer {
		p.Elem().Set(reflect.New(p.Elem().Type().Elem()))
		return assign(p.Elem(), b)
	}
	var v any
	json.Unmarshal(b, &v)
	if m, ok := v.(map[string]any); ok {
		if s, ok := m["$bytes"].(string); ok {
			bb, e := base64.StdEncoding.DecodeString(s)
			if e != nil {
				return e
			}
			if p.Elem().Kind() == reflect.Slice {
				p.Elem().SetBytes(bb)
				return nil
			}
			v = bb
		}
	}
	if p.Elem().Type() == reflect.TypeOf(time.Time{}) {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("time from %T", v)
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05", time.DateOnly} {
			t, e := time.Parse(layout, s)
			if e == nil {
				p.Elem().Set(reflect.ValueOf(t))
				return nil
			}
		}
		return fmt.Errorf("invalid database timestamp")
	}
	if s, ok := p.Interface().(interface{ Scan(any) error }); ok {
		return s.Scan(v)
	}
	if p.Elem().Kind() == reflect.Slice && p.Elem().Type().Elem().Kind() == reflect.Uint8 {
		if s, ok := v.(string); ok {
			p.Elem().SetBytes([]byte(s))
			return nil
		}
	}
	if p.Elem().Kind() == reflect.Interface {
		if s, ok := v.(string); ok && json.Valid([]byte(s)) {
			return json.Unmarshal([]byte(s), p.Interface())
		}
	}
	switch p.Elem().Kind() {
	case reflect.Bool:
		if n, ok := v.(float64); ok {
			p.Elem().SetBool(n != 0)
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if s, ok := v.(string); ok {
			n, e := strconv.ParseInt(s, 10, 64)
			if e == nil {
				p.Elem().SetInt(n)
				return nil
			}
		}
	case reflect.Slice, reflect.Map, reflect.Struct:
		if s, ok := v.(string); ok {
			return json.Unmarshal([]byte(s), p.Interface())
		}
	}
	return json.Unmarshal(b, p.Interface())
}

type batch struct {
	p      *Pool
	ctx    context.Context
	b      *pgx.Batch
	i      int
	err    error
	closed bool
}

func (p *Pool) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return &batch{p: p, ctx: ctx, b: b}
}
func (b *batch) next() (*pgx.QueuedQuery, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.i >= len(b.b.QueuedQueries) {
		return nil, errors.New("batch exhausted")
	}
	q := b.b.QueuedQueries[b.i]
	b.i++
	return q, nil
}
func (b *batch) Query() (pgx.Rows, error) {
	q, e := b.next()
	if e != nil {
		return &rows{err: e}, e
	}
	r, e := b.p.Query(b.ctx, q.SQL, q.Arguments...)
	b.err = e
	return r, e
}
func (b *batch) QueryRow() pgx.Row {
	q, e := b.next()
	if e != nil {
		return &row{err: e}
	}
	return b.p.QueryRow(b.ctx, q.SQL, q.Arguments...)
}
func (b *batch) Exec() (pgconn.CommandTag, error) {
	q, e := b.next()
	if e != nil {
		return pgconn.CommandTag{}, e
	}
	r, e := b.p.Exec(b.ctx, q.SQL, q.Arguments...)
	b.err = e
	return r, e
}
func (b *batch) Close() error {
	if b.closed {
		return b.err
	}
	for b.i < len(b.b.QueuedQueries) && b.err == nil {
		q := b.b.QueuedQueries[b.i]
		if q.Fn != nil {
			b.err = q.Fn(b)
		} else {
			_, b.err = b.Exec()
		}
	}
	b.closed = true
	return b.err
}

type Conn struct{ *Pool }

func (p *Pool) Acquire(context.Context) (*Conn, error) { return &Conn{p}, nil }
func (c *Conn) Release()                               {}
func (c *Conn) Conn() *Conn                            { return c }
func (c *Conn) PgConn() *Conn                          { return c }
func (c *Conn) CopyTo(ctx context.Context, w io.Writer, q string) (pgconn.CommandTag, error) {
	start := strings.Index(q, "(")
	end := strings.LastIndex(q, ") TO STDOUT")
	if start < 0 || end < 0 {
		return pgconn.CommandTag{}, errors.New("invalid CSV export")
	}
	var cursor struct {
		ID      string
		Columns []string
	}
	if err := bridge.Call(ctx, "cursor.start", q[start+1:end], &cursor); err != nil {
		return pgconn.CommandTag{}, err
	}
	defer bridge.Call(context.WithoutCancel(ctx), "cursor.close", cursor.ID, nil)
	cw := csv.NewWriter(w)
	cw.Write(cursor.Columns)
	var count int
	for {
		var rows [][]json.RawMessage
		if err := bridge.Call(ctx, "cursor.next", cursor.ID, &rows); err != nil {
			return pgconn.CommandTag{}, err
		}
		if len(rows) == 0 {
			break
		}
		for _, raw := range rows {
			a := make([]string, len(raw))
			for i, v := range raw {
				var x any
				json.Unmarshal(v, &x)
				if x != nil {
					a[i] = fmt.Sprint(x)
				}
			}
			if err := cw.Write(a); err != nil {
				return pgconn.CommandTag{}, err
			}
			count++
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return pgconn.CommandTag{}, err
		}
		if len(rows) < 100 {
			break
		}
	}
	cw.Flush()
	return pgconn.NewCommandTag(fmt.Sprintf("COPY %d", count)), cw.Error()
}
