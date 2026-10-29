package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"syscall/js"

	"github.com/almatkai/ielts-after-cigarette-back/internal/aiproviders"
	"github.com/almatkai/ielts-after-cigarette-back/internal/app"
	"github.com/almatkai/ielts-after-cigarette-back/internal/attempts"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cache"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	cfhttp "github.com/almatkai/ielts-after-cigarette-back/internal/cf/http"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/pgxpool"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/redis"
	"github.com/almatkai/ielts-after-cigarette-back/internal/config"
	"github.com/almatkai/ielts-after-cigarette-back/internal/jobs"
	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
	"github.com/almatkai/ielts-after-cigarette-back/internal/observability"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speaking"
	"github.com/almatkai/ielts-after-cigarette-back/internal/speakingpipeline"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writing"
	"github.com/almatkai/ielts-after-cigarette-back/internal/writingpipeline"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"time"
)

var handler http.Handler
var metricsHandler http.Handler
var writingWorker *writingpipeline.Worker
var speakingWorker *speakingpipeline.Worker

type speakingSTTAdapter struct {
	stt *listening.STTService
}

func (a *speakingSTTAdapter) Transcribe(ctx context.Context, filename string, audioStream io.Reader) (string, error) {
	res, err := a.stt.Transcribe(ctx, filename, audioStream)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

func promise(fn func() (any, error)) any {
	executor := js.FuncOf(func(_ js.Value, a []js.Value) any {
		resolve, reject := a[0], a[1]
		go func() {
			defer func() {
				if e := recover(); e != nil {
					reject.Invoke(fmt.Sprint(e))
				}
			}()
			v, e := fn()
			if e != nil {
				reject.Invoke(e.Error())
			} else {
				b, e := json.Marshal(v)
				if e != nil {
					reject.Invoke(e.Error())
				} else {
					resolve.Invoke(string(b))
				}
			}
		}()
		return nil
	})
	p := js.Global().Get("Promise").New(executor)
	executor.Release()
	return p
}
func main() {
	http.DefaultTransport = cfhttp.Transport{}
	js.Global().Set("iacInit", js.FuncOf(func(_ js.Value, a []js.Value) any {
		input := a[0].String()
		return promise(func() (any, error) {
			var env map[string]string
			if e := json.Unmarshal([]byte(input), &env); e != nil {
				return nil, e
			}
			for k, v := range env {
				os.Setenv(k, v)
			}
			cfg, e := config.Load()
			if e != nil {
				return nil, e
			}
			if cfg.Environment != "development" {
				return nil, fmt.Errorf("DEV runtime requires development environment")
			}
			cfg.WritingWorkerExternal = true
			if cfg.SpeechEnabled {
				return nil, fmt.Errorf("selected DEV speech service is disabled; a VPS speech endpoint cannot be enabled in native runtime")
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			pool := &pgxpool.Pool{}
			client := redis.NewClient(&redis.Options{})
			sharedCache, e := cache.NewJSON(cfg.RedisURL, logger)
			if e != nil {
				return nil, e
			}
			metrics := observability.New(pool, cfg.SpeechEnabled)
			metricsHandler = metrics.Handler()
			evaluator := attempts.NewChatCompletionsEvaluator(cfg.AIChatCompletionsURL, cfg.AIAPIKey, cfg.AIModel, &http.Client{Timeout: cfg.AITimeout}).WithSpeakingModel(cfg.AISpeakingModel).WithSpeakingAudio(cfg.AISpeakingAudioEnabled).WithProviders(aiproviders.NewConfigured(pool, cfg, logger))
			writingWorker = writingpipeline.NewWorker(attempts.NewPostgresRepository(pool), attempts.NewWritingProvider(writing.NewService(writing.NewPostgresRepository(pool), r2Store{})), evaluator, jobs.NewWritingQueue(client), logger)
			speakingWorker = speakingpipeline.NewWorker(
				attempts.NewPostgresRepository(pool),
				attempts.NewSpeakingProvider(speaking.NewService(speaking.NewPostgresRepository(pool))),
				r2Store{},
				nil,
				evaluator,
				jobs.NewSpeakingQueue(client),
				logger,
			).WithLegacyTranscriber(&speakingSTTAdapter{stt: listening.NewSTTService(
				cfg.STTAPIURL,
				cfg.STTAPIKey,
				cfg.AIChatCompletionsURL,
				cfg.AIAPIKey,
				cfg.AIModel,
			)})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			logOutput := io.Discard
			if os.Getenv("IAC_ADAPTER_TEST") == "true" {
				logOutput = os.Stdout
			}
			handler = app.NewWithOptions(cfg, &pgxpool.Pool{}, redis.NewClient(&redis.Options{}), slog.New(slog.NewTextHandler(logOutput, nil)), app.Options{ObjectStore: r2Store{}, WorkerContext: ctx, GradingCache: sharedCache, Metrics: metrics})
			routes := []map[string]string{}
			chi.Walk(handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
				routes = append(routes, map[string]string{"method": method, "path": route})
				return nil
			})
			b, _ := json.Marshal(routes)
			js.Global().Set("iacRoutes", string(b))
			return true, nil
		})
	}))
	js.Global().Set("iacHandle", js.FuncOf(func(_ js.Value, a []js.Value) any {
		input := a[0].String()
		return promise(func() (any, error) {
			var in struct {
				ID, Method, URL, RemoteAddr string
				Headers                     map[string][]string
				ContentLength               int64
				PrivateMetrics              bool
			}
			if e := json.Unmarshal([]byte(input), &in); e != nil {
				return nil, e
			}
			ctx := bridge.WithID(context.Background(), in.ID)
			defer cfhttp.Cleanup(ctx)
			r, e := http.NewRequestWithContext(ctx, in.Method, in.URL, &requestReader{ctx: ctx})
			if e != nil {
				return nil, e
			}
			r.ContentLength = in.ContentLength
			for k, values := range in.Headers {
				for _, v := range values {
					r.Header.Add(k, v)
				}
			}
			r.RemoteAddr = in.RemoteAddr
			w := &responseWriter{header: make(http.Header), ctx: ctx}
			if in.PrivateMetrics {
				metricsHandler.ServeHTTP(w, r)
			} else {
				handler.ServeHTTP(w, r)
			}
			if !w.started {
				w.WriteHeader(200)
			}
			return true, nil
		})
	}))
	js.Global().Set("iacJob", js.FuncOf(func(_ js.Value, a []js.Value) any {
		id := a[0].String()
		jobID := a[1].String()
		kind := "writing"
		if len(a) > 2 && a[2].String() != "" {
			kind = a[2].String()
		}
		return promise(func() (any, error) {
			ctx, cancel := context.WithTimeout(bridge.WithID(context.Background(), jobID), 10*time.Minute)
			defer cancel()
			if kind == "speaking" {
				if id == "" {
					speakingWorker.CloudflareRecover(ctx)
				} else {
					uid, e := uuid.Parse(id)
					if e != nil {
						return nil, e
					}
					speakingWorker.CloudflareProcess(ctx, uid)
				}
				return true, nil
			}
			if id == "" {
				writingWorker.CloudflareRecover(ctx)
			} else {
				uid, e := uuid.Parse(id)
				if e != nil {
					return nil, e
				}
				writingWorker.CloudflareProcess(ctx, uid)
			}
			return true, nil
		})
	}))
	select {}
}

type r2Store struct{}

func (r2Store) Check(ctx context.Context) error { return bridge.Call(ctx, "r2check", nil, nil) }
func (r2Store) Delete(ctx context.Context, key string) error {
	return bridge.Call(ctx, "r2delete", key, nil)
}
func (r2Store) Put(ctx context.Context, key, contentType string, r io.Reader, size int64) (int64, error) {
	if source, ok := r.(interface{ CloudflareKey() string }); ok {
		e := bridge.Call(ctx, "r2copy", map[string]any{"source": source.CloudflareKey(), "key": key, "contentType": contentType, "size": size}, nil)
		return size, e
	}
	var upload string
	if e := bridge.Call(ctx, "r2start", map[string]any{"key": key, "contentType": contentType}, &upload); e != nil {
		return 0, e
	}
	complete := false
	defer func() {
		if !complete {
			_ = bridge.Call(context.WithoutCancel(ctx), "r2abort", upload, nil)
		}
	}()
	buffer := make([]byte, 5<<20)
	var total int64
	for part := 1; ; part++ {
		n, e := io.ReadFull(r, buffer)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			return total, e
		}
		if n > 0 {
			total += int64(n)
			if total > size {
				return total, fmt.Errorf("object size mismatch")
			}
			if e = bridge.Bytes(ctx, "r2part", map[string]any{"upload": upload, "part": part}, buffer[:n], nil); e != nil {
				return total, e
			}
		}
		if n < len(buffer) {
			break
		}
	}
	if total != size {
		return total, fmt.Errorf("object size mismatch")
	}
	if e := bridge.Call(ctx, "r2complete", upload, nil); e != nil {
		return total, e
	}
	complete = true
	return total, nil
}

type objectReader struct {
	ctx            context.Context
	key            string
	size, position int64
	cache          []byte
	cacheAt        int64
}

func (r *objectReader) CloudflareKey() string   { return r.key }
func (r *objectReader) CloudflareOffset() int64 { return r.position }
func (*objectReader) Close() error              { return nil }
func (r *objectReader) Read(p []byte) (int, error) {
	if r.position >= r.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.position < r.cacheAt || r.position >= r.cacheAt+int64(len(r.cache)) {
		length := int64(1 << 20)
		if r.position == 0 && len(p) < 512 {
			length = 64 << 10
		}
		length = min(length, r.size-r.position)
		var encoded string
		if e := bridge.Call(r.ctx, "r2range", map[string]any{"key": r.key, "offset": r.position, "length": length}, &encoded); e != nil {
			return 0, e
		}
		b, e := base64.StdEncoding.DecodeString(encoded)
		if e != nil {
			return 0, e
		}
		r.cache = b
		r.cacheAt = r.position
	}
	n := copy(p, r.cache[r.position-r.cacheAt:])
	r.position += int64(n)
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
func (r *objectReader) Seek(offset int64, whence int) (int64, error) {
	next := offset
	switch whence {
	case io.SeekCurrent:
		next += r.position
	case io.SeekEnd:
		next += r.size
	case io.SeekStart:
	default:
		return 0, fmt.Errorf("invalid seek")
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	r.position = next
	return next, nil
}
func (r2Store) Open(ctx context.Context, key string) (objectstorage.ReadSeekCloser, error) {
	var size *int64
	e := bridge.Call(ctx, "r2head", key, &size)
	if e != nil {
		return nil, e
	}
	if size == nil {
		return nil, os.ErrNotExist
	}
	return &objectReader{ctx: ctx, key: key, size: *size}, nil
}

type requestReader struct{ ctx context.Context }

func (r *requestReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var encoded *string
	if e := bridge.Call(r.ctx, "http.read", min(len(p), 64<<10), &encoded); e != nil {
		return 0, e
	}
	if encoded == nil {
		return 0, io.EOF
	}
	b, e := base64.StdEncoding.DecodeString(*encoded)
	if e != nil {
		return 0, e
	}
	return copy(p, b), nil
}

type responseWriter struct {
	ctx     context.Context
	header  http.Header
	started bool
	err     error
}

func (w *responseWriter) Header() http.Header { return w.header }
func (w *responseWriter) WriteHeader(status int) {
	if w.started {
		return
	}
	w.started = true
	w.err = bridge.Call(w.ctx, "http.headers", map[string]any{"status": status, "headers": w.header}, nil)
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.started {
		if w.header.Get("Content-Type") == "" {
			w.header.Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(200)
	}
	if w.err != nil {
		return 0, w.err
	}
	total := 0
	for len(p) > 0 {
		n := min(len(p), 64<<10)
		if e := bridge.Bytes(w.ctx, "http.write", nil, p[:n], nil); e != nil {
			return total, e
		}
		total += n
		p = p[n:]
	}
	return total, nil
}
func (w *responseWriter) Flush() {
	if !w.started {
		w.WriteHeader(200)
	}
}
