package database

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrationVersionsAreUniqueAndPaired(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../migrations")
	up, err := filepath.Glob(filepath.Join(root, "*.up.sql"))
	if err != nil || len(up) == 0 {
		t.Fatalf("migrations missing: %v", err)
	}
	down, err := filepath.Glob(filepath.Join(root, "*.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != len(down) {
		t.Fatalf("migration direction counts differ: %d up, %d down", len(up), len(down))
	}
	versions := make(map[string]string)
	for _, path := range up {
		name := filepath.Base(path)
		version, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Fatalf("migration has no version: %s", name)
		}
		if previous, exists := versions[version]; exists {
			t.Fatalf("duplicate migration version %s: %s and %s", version, previous, name)
		}
		versions[version] = name
		pair := strings.TrimSuffix(path, ".up.sql") + ".down.sql"
		matches, err := filepath.Glob(pair)
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %s has no matching rollback: %v", name, err)
		}
	}
}
