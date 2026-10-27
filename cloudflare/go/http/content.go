package cfhttp

import (
	"context"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	"io"
	"net/http"
	"time"
)

// ServeContent retains Go's conditional/range semantics. Its ReaderFrom fast
// path sends an authorized R2 range directly to the response with backpressure.
func ServeContent(w http.ResponseWriter, r *http.Request, name string, modtime time.Time, content io.ReadSeeker) {
	http.ServeContent(&contentWriter{ResponseWriter: w, ctx: r.Context()}, r, name, modtime, content)
}

type contentWriter struct {
	http.ResponseWriter
	ctx context.Context
}
type r2Source interface {
	CloudflareKey() string
	CloudflareOffset() int64
	io.Seeker
}

func (w *contentWriter) ReadFrom(reader io.Reader) (int64, error) {
	if limited, ok := reader.(*io.LimitedReader); ok {
		if source, ok := limited.R.(r2Source); ok {
			var written int64
			err := bridge.Call(w.ctx, "http.r2stream", map[string]any{"key": source.CloudflareKey(), "offset": source.CloudflareOffset(), "length": limited.N}, &written)
			if written > 0 {
				_, _ = source.Seek(written, io.SeekCurrent)
				limited.N -= written
			}
			return written, err
		}
	}
	// Hide ReaderFrom here to prevent recursion for multipart byte ranges.
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, reader)
}
