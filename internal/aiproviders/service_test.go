package aiproviders

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/almatkai/ielts-after-cigarette-back/internal/httpx"
	"github.com/google/uuid"
)

type memoryRepo struct {
	items   []Provider
	listErr error
}

func (r *memoryRepo) List(context.Context) ([]Provider, error) {
	return append([]Provider{}, r.items...), r.listErr
}
func (r *memoryRepo) Get(_ context.Context, id uuid.UUID) (Provider, error) {
	for _, p := range r.items {
		if p.ID == id {
			return p, nil
		}
	}
	return Provider{}, ErrNotFound
}
func (r *memoryRepo) Save(_ context.Context, p Provider, _ uuid.UUID, create bool) (Provider, error) {
	p.HasKey = true
	if create {
		p.Revision = 1
		r.items = append(r.items, p)
		return p, nil
	}
	for index, old := range r.items {
		if old.ID == p.ID {
			if old.Revision != p.Revision {
				return Provider{}, ErrConflict
			}
			p.Revision++
			r.items[index] = p
			return p, nil
		}
	}
	return Provider{}, ErrConflict
}
func (r *memoryRepo) Delete(_ context.Context, id uuid.UUID, revision int64) error {
	for index, p := range r.items {
		if p.ID == id && p.Revision == revision {
			r.items = append(r.items[:index], r.items[index+1:]...)
			return nil
		}
	}
	return ErrConflict
}

type captureSink struct{ events []httpx.ErrorEvent }

func (s *captureSink) ReportError(_ context.Context, event httpx.ErrorEvent) {
	s.events = append(s.events, event)
}
func encryptionKey(char byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{char}, 32))
}
func testCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(encryptionKey(1), "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func input() Input {
	return Input{Name: "Provider", Endpoint: "https://api.example.com/v1/chat/completions", Model: "model", Scopes: []string{"assistant", "writing", "speaking"}, Enabled: true, Priority: 10, TimeoutSeconds: 20, APIKey: "secret-provider-key"}
}
func testLogger(out io.Writer) *slog.Logger { return slog.New(slog.NewJSONHandler(out, nil)) }

func TestCipherAuthenticationAndRotation(t *testing.T) {
	first := testCipher(t)
	secret := "sk-test-only"
	aad := "row|https://example.com/v1/chat/completions"
	sealed, err := first.Seal(secret, aad)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := first.Seal(secret, aad)
	if bytes.Equal(sealed, second) || bytes.Contains(sealed, []byte(secret)) {
		t.Fatal("ciphertext is deterministic or plaintext")
	}
	for _, badAAD := range []string{"other-row|https://example.com/v1/chat/completions", "row|https://attacker.example/v1/chat/completions"} {
		if _, err := first.Open(sealed, badAAD); !errors.Is(err, ErrEncryption) {
			t.Fatal("AAD tampering accepted")
		}
	}
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 1
	if _, err := first.Open(tampered, aad); !errors.Is(err, ErrEncryption) {
		t.Fatal("ciphertext tampering accepted")
	}
	rotated, err := NewCipher(encryptionKey(2), encryptionKey(1))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := rotated.Open(sealed, aad); err != nil || plain != secret {
		t.Fatal("previous key cannot decrypt")
	}
	resealed, _ := rotated.Seal(secret, aad)
	currentOnly, _ := NewCipher(encryptionKey(2), "")
	if _, err := currentOnly.Open(sealed, aad); !errors.Is(err, ErrEncryption) {
		t.Fatal("unknown key accepted")
	}
	if plain, err := currentOnly.Open(resealed, aad); err != nil || plain != secret {
		t.Fatal("rotation did not use current key")
	}
	for _, bad := range []string{"not-base64", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := NewCipher(bad, ""); !errors.Is(err, ErrEncryption) {
			t.Fatal("invalid master accepted")
		}
	}
}

func TestSavedSecretsNeverReturnAndBlankKeyPreservesSecret(t *testing.T) {
	repo := &memoryRepo{}
	cipher := testCipher(t)
	service := NewService(repo, cipher, Provider{}, testLogger(io.Discard))
	p, err := service.Save(context.Background(), uuid.Nil, uuid.New(), input())
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(p)
	if strings.Contains(string(serialized), "secret-provider-key") || strings.Contains(string(serialized), "ciphertext") || strings.Contains(string(serialized), "apiKey") || !p.HasKey {
		t.Fatal("public provider reveals key or loses hasKey")
	}
	changed := input()
	changed.APIKey = ""
	changed.Revision = p.Revision
	changed.Endpoint = "https://other.example.com/v1/chat/completions"
	updated, err := service.Save(context.Background(), p.ID, uuid.New(), changed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Open(updated.Ciphertext, updated.aad())
	if err != nil || plain != "secret-provider-key" {
		t.Fatal("blank-key update lost existing key")
	}
	if _, err := cipher.Open(updated.Ciphertext, p.aad()); !errors.Is(err, ErrEncryption) {
		t.Fatal("destination binding not updated")
	}
	if _, err := service.Save(context.Background(), p.ID, uuid.New(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision overwrote provider")
	}
	if err := service.Delete(context.Background(), p.ID, p.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete accepted")
	}
	if err := service.Delete(context.Background(), p.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestChainScopesFailuresAndEnvironmentReserve(t *testing.T) {
	var logs bytes.Buffer
	sink := &captureSink{}
	httpx.SetErrorReporter(sink.ReportError)
	defer httpx.SetErrorReporter(nil)
	repo := &memoryRepo{}
	service := NewService(repo, testCipher(t), Provider{Endpoint: "http://trusted-env.test/completions", Model: "env-model", APIKey: "env-secret", TimeoutSeconds: 45}, testLogger(&logs))
	first, _ := service.Save(context.Background(), uuid.Nil, uuid.New(), input())
	next := input()
	next.Model = "next-model"
	next.Name = "Next"
	next.Priority = 20
	second, _ := service.Save(context.Background(), uuid.Nil, uuid.New(), next)
	disabled := input()
	disabled.Enabled = false
	service.Save(context.Background(), uuid.Nil, uuid.New(), disabled)
	assistantOnly := input()
	assistantOnly.Scopes = []string{"assistant"}
	service.Save(context.Background(), uuid.Nil, uuid.New(), assistantOnly)
	var calls []string
	err := service.Run(context.Background(), "writing", func(_ context.Context, p Provider) error {
		calls = append(calls, p.Model)
		if p.ID == first.ID {
			return &HTTPError{Status: 429}
		}
		if p.ID == second.ID {
			return errors.New("provider echoed secret-provider-key env-secret raw-model-answer")
		}
		if !p.FromEnv || p.APIKey != "env-secret" {
			t.Fatal("wrong environment reserve")
		}
		return nil
	})
	if err != nil || strings.Join(calls, ",") != "model,next-model,env-model" {
		t.Fatalf("chain=%v err=%v", calls, err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("recovered failures not reported: %d", len(sink.events))
	}
	for _, event := range sink.events {
		if strings.Contains(event.Error.Error(), "secret") || event.Origin != "ai_provider" || event.AIPurpose != "writing" {
			t.Fatal("unsafe/incomplete telemetry")
		}
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "raw-model-answer") {
		t.Fatal("logs contain provider secrets or output")
	}
	failure := sink.events[0].Error.(*Failure)
	if failure.Status != 429 || failure.Code != "http_error" || failure.ProviderID != first.ID {
		t.Fatalf("missing failure metadata: %+v", failure)
	}
}

func TestChainTimeoutStillTriesNextAndCancellationStops(t *testing.T) {
	sink := &captureSink{}
	httpx.SetErrorReporter(sink.ReportError)
	defer httpx.SetErrorReporter(nil)
	repo := &memoryRepo{}
	service := NewService(repo, testCipher(t), Provider{Endpoint: "https://env.test/completions", Model: "env", APIKey: "env-key", TimeoutSeconds: 5}, testLogger(io.Discard))
	service.Save(context.Background(), uuid.Nil, uuid.New(), input())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	calls := 0
	err := service.Run(ctx, "assistant", func(ctx context.Context, p Provider) error {
		calls++
		if !p.FromEnv {
			<-ctx.Done()
			return errors.New("evaluator wrapped timeout")
		}
		return nil
	})
	if err != nil || calls != 2 || len(sink.events) != 1 || sink.events[0].Error.(*Failure).Code != "timeout" {
		t.Fatalf("timeout did not fall back: calls=%d err=%v events=%v", calls, err, sink.events)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	calls = 0
	if err := service.Run(canceled, "assistant", func(context.Context, Provider) error { calls++; return nil }); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled request called a provider")
	}
}

func TestTamperedDestinationCannotReceiveKeyAndDatabaseOutageFallsBack(t *testing.T) {
	repo := &memoryRepo{}
	service := NewService(repo, testCipher(t), Provider{Endpoint: "https://env.test/completions", Model: "env", APIKey: "env-key", TimeoutSeconds: 5}, testLogger(io.Discard))
	service.Save(context.Background(), uuid.Nil, uuid.New(), input())
	repo.items[0].Endpoint = "https://attacker.example/completions"
	calls := 0
	call := func(_ context.Context, p Provider) error {
		calls++
		if !p.FromEnv {
			t.Fatal("tampered destination received decrypted key")
		}
		return nil
	}
	if err := service.Run(context.Background(), "assistant", call); err != nil || calls != 1 {
		t.Fatal(err)
	}
	repo.listErr = errors.New("database echoed secret")
	calls = 0
	if err := service.Run(context.Background(), "assistant", call); err != nil || calls != 1 {
		t.Fatal("database outage lost .env reserve")
	}
}

func TestEndpointSSRFProtection(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/completions", "https://user:secret@example.com/completions", "https://example.com:8443/completions", "https://example.com/completions?key=secret", "https://example.com/completions#fragment", "https://localhost/completions", "https://service.local/completions", "https://127.0.0.1/completions", "https://169.254.169.254/completions", "https://10.0.0.1/completions", "https://100.64.0.1/completions", "https://[::1]/completions", "https://[::ffff:127.0.0.1]/completions", "https://[64:ff9b::a00:1]/completions", "https://example.com/"} {
		if err := ValidateEndpoint(endpoint); !errors.Is(err, ErrValidation) {
			t.Errorf("unsafe URL accepted: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"https://api.openai.com/v1/chat/completions", "https://openrouter.ai/api/v1/chat/completions", "https://8.8.8.8:443/v1/chat/completions"} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Errorf("public URL rejected: %s", endpoint)
		}
	}
	for _, address := range []string{"127.0.0.1:443", "10.0.0.1:443", "[::1]:443", "169.254.169.254:443"} {
		if conn, err := safeDial(context.Background(), "tcp", address); err == nil {
			conn.Close()
			t.Fatal("private IP was dialed")
		}
	}
	if publicIP(net.IPv4(10, 0, 0, 1).To4()) || !publicIP(net.IPv4(8, 8, 8, 8).To4()) {
		t.Fatal("4-byte IPv4 classification failed")
	}
	client := newClient(true)
	if client.Transport.(*http.Transport).Proxy != nil || !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("proxy or redirect bypass enabled")
	}
}
