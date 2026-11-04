package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the real deploy flow against a fake Docker CLI: no production
// configuration, credentials, containers or network connections are used.
func TestProductionDeployRollsOutMonitoring(t *testing.T) {
	for _, tc := range []struct {
		name, failService string
		operational       bool
	}{
		{name: "base stack"},
		{name: "operational stack", operational: true},
		{name: "monitoring failure stops deploy", operational: true, failService: "alertbridge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, source, _, _ := runtime.Caller(0)
			script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "deploy-production.sh"))
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			write := func(name, content string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("scripts/deploy-production.sh", string(script), 0755)
			write("production.env", "TEST_FIXTURE=1\n", 0600)
			if tc.operational {
				for _, name := range []string{"operations.env", "docker-compose.observability.yml", "docker-compose.pgstats.yml", "docker-compose.server.yml"} {
					write(name, "", 0600)
				}
			}
			write("bin/docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DEPLOY_TEST_LOG"
case "$*" in
  *"up -d --no-deps --build --force-recreate prometheus alertbridge"*)
    if [ "$DEPLOY_TEST_FAIL" = alertbridge ]; then exit 1; fi;;
  *"ps -q worker"*) printf '%s\n' worker-fixture;;
  *"inspect -f"*) printf '%s\n' true;;
esac
`, 0755)
			logPath := filepath.Join(root, "docker.log")
			cmd := exec.Command("bash", filepath.Join(root, "scripts/deploy-production.sh"))
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "DEPLOY_TEST_LOG="+logPath, "DEPLOY_TEST_FAIL="+tc.failService)
			output, runErr := cmd.CombinedOutput()
			if (runErr != nil) != (tc.failService != "") {
				t.Fatalf("unexpected deploy result: %v\n%s", runErr, output)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			rolledOut := strings.Contains(string(log), "up -d --no-deps --build --force-recreate prometheus alertbridge")
			if rolledOut != tc.operational {
				t.Fatalf("monitoring rollout=%v, operational=%v\n%s", rolledOut, tc.operational, log)
			}
		})
	}
}
