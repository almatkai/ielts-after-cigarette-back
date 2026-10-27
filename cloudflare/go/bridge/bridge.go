// Package bridge connects only the isolated js/wasm build to Worker bindings.
package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"syscall/js"
	"time"
)

type requestKey struct{}

func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey{}, id)
}
func ID(ctx context.Context) string { id, _ := ctx.Value(requestKey{}).(string); return id }
func Normalize(v any) any {
	if v == nil {
		return nil
	}
	r := reflect.ValueOf(v)
	if r.Kind() == reflect.Pointer {
		if r.IsNil() {
			return nil
		}
		return Normalize(r.Elem().Interface())
	}
	if (r.Kind() == reflect.Map || r.Kind() == reflect.Slice) && r.IsNil() {
		return nil
	}
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format("2006-01-02T15:04:05.000000000Z")
	case json.RawMessage:
		return map[string]any{"$json": string(x)}
	case []byte:
		return map[string]any{"$bytes": base64.StdEncoding.EncodeToString(x)}
	case fmt.Stringer:
		return x.String()
	}

	if r.Kind() == reflect.Slice || r.Kind() == reflect.Array {
		a := make([]any, r.Len())
		for i := range a {
			a[i] = Normalize(r.Index(i).Interface())
		}
		return map[string]any{"$array": a}
	}
	if r.Kind() == reflect.Map || r.Kind() == reflect.Struct {
		b, _ := json.Marshal(v)
		return map[string]any{"$json": string(b)}
	}
	return v
}

func Call(ctx context.Context, op string, input any, output any) error {
	return call(ctx, op, input, nil, output)
}
func Bytes(ctx context.Context, op string, input any, bytes []byte, output any) error {
	return call(ctx, op, input, bytes, output)
}
func call(ctx context.Context, op string, input any, data []byte, output any) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("Worker bridge %s: %v", op, e)
		}
	}()
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var value js.Value
	if data != nil {
		a := js.Global().Get("Uint8Array").New(len(data))
		js.CopyBytesToJS(a, data)
		value = js.Global().Get("iacBridge").Invoke(op, string(b), a, ID(ctx))
	} else {
		value = js.Global().Get("iacBridge").Invoke(op, string(b), js.Undefined(), ID(ctx))
	}
	if value.Type() == js.TypeObject && value.Get("then").Type() == js.TypeFunction {
		type result struct {
			s   string
			err error
		}
		ch := make(chan result, 1)
		yes := js.FuncOf(func(_ js.Value, args []js.Value) any { ch <- result{s: args[0].String()}; return nil })
		no := js.FuncOf(func(_ js.Value, args []js.Value) any { ch <- result{err: errors.New(args[0].String())}; return nil })
		value.Call("then", yes, no)
		// Keep callbacks alive until settlement even on cancellation.
		r := <-ch
		yes.Release()
		no.Release()
		if r.err != nil {
			return r.err
		}
		value = js.ValueOf(r.s)
	}
	var envelope struct {
		Data       json.RawMessage `json:"data"`
		Error      string          `json:"error"`
		Code       string          `json:"code"`
		Constraint string          `json:"constraint"`
	}
	if err = json.Unmarshal([]byte(value.String()), &envelope); err != nil {
		return err
	}
	if envelope.Error != "" {
		return &Error{envelope.Error, envelope.Code, envelope.Constraint}
	}
	if output != nil && len(envelope.Data) > 0 {
		return json.Unmarshal(envelope.Data, output)
	}
	return nil
}

type Error struct{ Message, Code, Constraint string }

func (e *Error) Error() string { return e.Message }
