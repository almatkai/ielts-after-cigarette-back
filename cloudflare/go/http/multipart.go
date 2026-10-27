// Package cfhttp supplies diskless multipart files for the isolated Worker build.
// The standard multipart parser still owns boundary/header decoding. Uploaded
// bytes spill directly to private R2 objects instead of Go heap or a filesystem.
package cfhttp

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/almatkai/ielts-after-cigarette-back/internal/cf/bridge"
	"github.com/google/uuid"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
)

type stored struct {
	key  string
	size int64
	ctx  context.Context
}

var files = struct {
	sync.Mutex
	values map[string]map[*multipart.FileHeader]stored
}{values: map[string]map[*multipart.FileHeader]stored{}}

func ParseMultipartForm(r *http.Request, maxMemory int64) error {
	if r.MultipartForm != nil {
		return nil
	}
	if e := r.ParseForm(); e != nil {
		return e
	}
	reader, e := r.MultipartReader()
	if e != nil {
		return e
	}
	form := &multipart.Form{Value: map[string][]string{}, File: map[string][]*multipart.FileHeader{}}
	valuesBudget := int64(10 << 20)
	headersBudget := 10000
	for parts := 0; ; parts++ {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if parts >= 1000 {
			return multipart.ErrMessageTooLarge
		}
		for _, values := range part.Header {
			headersBudget -= len(values)
		}
		if headersBudget < 0 {
			return multipart.ErrMessageTooLarge
		}
		name := part.FormName()
		if name == "" {
			part.Close()
			continue
		}
		if part.FileName() == "" {
			value, e := io.ReadAll(io.LimitReader(part, valuesBudget+1))
			if e != nil {
				return e
			}
			valuesBudget -= int64(len(value))
			if valuesBudget < 0 {
				return multipart.ErrMessageTooLarge
			}
			form.Value[name] = append(form.Value[name], string(value))
			r.Form[name] = append(r.Form[name], string(value))
			continue
		}
		key := "_cf-temp/" + uuid.NewString()
		var upload string
		if e = bridge.Call(r.Context(), "r2start", map[string]any{"key": key, "contentType": part.Header.Get("Content-Type")}, &upload); e != nil {
			return e
		}
		var size int64
		e = func() error {
			complete := false
			defer func() {
				if !complete {
					_ = bridge.Call(context.WithoutCancel(r.Context()), "r2abort", upload, nil)
				}
			}()
			buffer := make([]byte, 5<<20)
			for number := 1; ; number++ {
				n, err := io.ReadFull(part, buffer)
				if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
					return err
				}
				size += int64(n)
				if size > maxMemory {
					return multipart.ErrMessageTooLarge
				}
				if n > 0 {
					if err = bridge.Bytes(r.Context(), "r2part", map[string]any{"upload": upload, "part": number}, buffer[:n], nil); err != nil {
						return err
					}
				}
				if n < len(buffer) {
					break
				}
			}
			if size == 0 {
				_ = bridge.Call(context.WithoutCancel(r.Context()), "r2abort", upload, nil)
				return nil
			}
			if err := bridge.Call(r.Context(), "r2complete", upload, nil); err != nil {
				return err
			}
			complete = true
			return nil
		}()
		if e != nil {
			return e
		}
		header := &multipart.FileHeader{Filename: part.FileName(), Header: part.Header, Size: size}
		form.File[name] = append(form.File[name], header)
		files.Lock()
		id := bridge.ID(r.Context())
		if files.values[id] == nil {
			files.values[id] = map[*multipart.FileHeader]stored{}
		}
		files.values[id][header] = stored{key, size, r.Context()}
		files.Unlock()
	}
	r.MultipartForm = form
	r.PostForm = form.Value
	return nil
}
func FormFile(r *http.Request, name string) (multipart.File, *multipart.FileHeader, error) {
	if r.MultipartForm == nil {
		if e := ParseMultipartForm(r, 32<<20); e != nil {
			return nil, nil, e
		}
	}
	headers := r.MultipartForm.File[name]
	if len(headers) == 0 {
		return nil, nil, http.ErrMissingFile
	}
	header := headers[0]
	files.Lock()
	s, ok := files.values[bridge.ID(r.Context())][header]
	files.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("multipart file is not owned by this request")
	}
	return &file{stored: s}, header, nil
}
func Cleanup(ctx context.Context) {
	files.Lock()
	owned := files.values[bridge.ID(ctx)]
	delete(files.values, bridge.ID(ctx))
	files.Unlock()
	for _, s := range owned {
		if s.size > 0 {
			_ = bridge.Call(context.WithoutCancel(ctx), "r2delete", s.key, nil)
		}
	}
}

type file struct {
	stored
	position int64
	cache    []byte
	cacheAt  int64
}

func (f *file) CloudflareKey() string { return f.key }
func (*file) Close() error            { return nil }
func (f *file) Read(p []byte) (int, error) {
	n, e := f.ReadAt(p, f.position)
	f.position += int64(n)
	if e == io.EOF && n > 0 {
		e = nil
	}
	return n, e
}
func (f *file) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, fmt.Errorf("negative read offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	total := 0
	for len(p) > 0 && offset < f.size {
		if offset < f.cacheAt || offset >= f.cacheAt+int64(len(f.cache)) {
			length := int64(1 << 20)
			if offset == 0 && len(p) <= 512 {
				length = 64 << 10
			}
			length = min(length, f.size-offset)
			var encoded string
			if e := bridge.Call(f.ctx, "r2range", map[string]any{"key": f.key, "offset": offset, "length": length}, &encoded); e != nil {
				return total, e
			}
			b, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil {
				return total, e
			}
			f.cache = b
			f.cacheAt = offset
		}
		n := copy(p, f.cache[offset-f.cacheAt:])
		if n == 0 {
			return total, io.ErrUnexpectedEOF
		}
		total += n
		offset += int64(n)
		p = p[n:]
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}
func (f *file) Seek(offset int64, whence int) (int64, error) {
	next := offset
	switch whence {
	case io.SeekCurrent:
		next += f.position
	case io.SeekEnd:
		next += f.size
	case io.SeekStart:
	default:
		return 0, fmt.Errorf("invalid seek")
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	f.position = next
	return next, nil
}
