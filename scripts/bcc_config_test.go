package scripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBCCDeploymentDotenvRoundTrip(t *testing.T) {
	composeBinary, _ := exec.LookPath("docker-compose")
	if composeBinary == "" {
		home, _ := os.UserHomeDir()
		for _, candidate := range []string{
			filepath.Join(home, ".docker/cli-plugins/docker-compose"),
			"/usr/libexec/docker/cli-plugins/docker-compose",
			"/usr/lib/docker/cli-plugins/docker-compose",
			"/usr/local/lib/docker/cli-plugins/docker-compose",
			"/Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose",
		} {
			if info, err := os.Stat(candidate); err == nil && info.Mode()&0111 != 0 {
				composeBinary = candidate
				break
			}
		}
	}
	if composeBinary == "" {
		t.Skip("Docker Compose CLI is not installed")
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(source))
	b, err := os.ReadFile(filepath.Join(root, ".github/workflows/deploy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, script, ok := strings.Cut(string(b), "            python3 - <<'PY'\n")
	if !ok {
		t.Fatal("deployment dotenv writer not found")
	}
	script, _, ok = strings.Cut(script, "          PY\n")
	if !ok {
		t.Fatal("deployment dotenv writer terminator not found")
	}
	lines := strings.Split(script, "\n")
	for i := range lines {
		lines[i] = strings.TrimPrefix(lines[i], "          ")
	}
	script = strings.Join(lines, "\n")
	for _, value := range []string{
		`long-trailing-backslash\`,
		`long-backslash-before-quote\'suffix`,
		`long-backslash-before-double-quote\"suffix`,
		`long-quote'and-double"and-hash#suffix`,
		`long-dollar-${HOME}-$UNSET-$$-suffix`,
		"long-unicode-Алматы-\t-suffix",
	} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			// No real credentials, dotenv files, Docker config or daemon required.
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "DOCKER_CONFIG=" + dir, "BCC_NOTIFY_USER=" + value, "BCC_NOTIFY_PASSWORD=" + value, "BCC_MERCHANT_NAME=AFTER CREDENTIALS"}
			writer := exec.Command("python3", "-c", script)
			writer.Env = env
			content, err := writer.Output()
			if err != nil {
				t.Fatal(err)
			}
			envFile := filepath.Join(dir, "generated.env")
			if err := os.WriteFile(envFile, content, 0600); err != nil {
				t.Fatal(err)
			}
			compose := filepath.Join(dir, "compose.yml")
			if err := os.WriteFile(compose, []byte("services:\n  app:\n    image: scratch\n    environment:\n      BCC_NOTIFY_USER: ${BCC_NOTIFY_USER}\n      BCC_NOTIFY_PASSWORD: ${BCC_NOTIFY_PASSWORD}\n      BCC_MERCHANT_NAME: ${BCC_MERCHANT_NAME}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(composeBinary, "-p", "bcc-dotenv-test", "--env-file", envFile, "-f", compose, "config", "--format", "json")
			cmd.Env = env[:3] // Read the generated file, not the writer's environment.
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Compose parse failed: %v: %s", err, output)
			}
			var parsed struct {
				Services map[string]struct{ Environment map[string]string }
			}
			if err := json.Unmarshal(output, &parsed); err != nil {
				t.Fatal(err)
			}
			got := parsed.Services["app"].Environment
			// Compose re-escapes literal dollars when serializing a reusable model.
			serialized := strings.ReplaceAll(value, "$", "$$")
			if got["BCC_NOTIFY_USER"] != serialized || got["BCC_NOTIFY_PASSWORD"] != serialized || got["BCC_MERCHANT_NAME"] != "AFTER CREDENTIALS" {
				t.Fatalf("dotenv changed synthetic values: %+v", got)
			}
			cmd = exec.Command(composeBinary, "-p", "bcc-dotenv-test", "--env-file", envFile, "-f", compose, "config", "--environment")
			cmd.Env = env[:3]
			output, err = cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Compose environment parse: %v", err)
			}
			actual := map[string]string{}
			for _, line := range strings.Split(string(output), "\n") {
				key, v, _ := strings.Cut(line, "=")
				actual[key] = v
			}
			if actual["BCC_NOTIFY_USER"] != value || actual["BCC_NOTIFY_PASSWORD"] != value {
				t.Fatal("dotenv parser changed the original synthetic credentials")
			}
		})
	}
}

func TestBCCConfigurationReachesComposeAndDeployment(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(source))
	keys := []string{"BCC_GATEWAY", "BCC_TERMINAL", "BCC_MERCHANT", "BCC_MERCHANT_NAME", "BCC_MAC_KEY", "BCC_NOTIFY_URL", "BCC_NOTIFY_USER", "BCC_NOTIFY_PASSWORD", "BCC_PUBLIC_BASE_URL", "BCC_BRIDGE_KEY", "BCC_BRIDGE_TTL", "BCC_LANGUAGE", "BCC_TRUSTED_PROXY_CIDRS"}
	for _, file := range []string{".env.example", "docker-compose.yml", "docker-compose.production.yml", ".github/workflows/deploy.yml"} {
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			if !strings.Contains(string(b), key) {
				t.Errorf("%s omits %s", file, key)
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".github/workflows/deploy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(b)
	for _, key := range []string{"BCC_MAC_KEY", "BCC_NOTIFY_USER", "BCC_NOTIFY_PASSWORD", "BCC_BRIDGE_KEY"} {
		if !strings.Contains(workflow, "${{ secrets."+key+" }}") {
			t.Errorf("%s is not sourced from secrets", key)
		}
	}
	// Acquiring must never switch on by itself: either the workflow defaults
	// PAYMENT_PROVIDER to none, or (as on dev) it does not pass it and the
	// production manifest's own default of none applies.
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.production.yml"))
	if err != nil {
		t.Fatal(err)
	}
	explicitNone := strings.Contains(workflow, "PAYMENT_PROVIDER: ${{ vars.PAYMENT_PROVIDER || 'none' }}")
	composeDefault := !strings.Contains(workflow, "PAYMENT_PROVIDER") && strings.Contains(string(compose), "PAYMENT_PROVIDER: ${PAYMENT_PROVIDER:-none}")
	if !explicitNone && !composeDefault {
		t.Fatal("production automatically enables acquiring")
	}
	for _, key := range keys {
		if !strings.Contains(workflow, "'"+key+"'") {
			t.Errorf("%s is not written into the deploy env file", key)
		}
	}
}
