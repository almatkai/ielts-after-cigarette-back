package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/almatkai/ielts-after-cigarette-back/internal/database"
	"github.com/almatkai/ielts-after-cigarette-back/internal/listening"
	"github.com/almatkai/ielts-after-cigarette-back/internal/objectstorage"
)

func loadEnvFile(path string) map[string]string {
	res := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return res
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			v = strings.Trim(v, `"'`)
			res[k] = v
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
	return res
}

func main() {
	testIDFlag := flag.String("test-id", "", "Specific test ID to transcribe (or empty for all untranscribed tests)")
	envPath := flag.String("env", ".env", "Path to .env file")
	flag.Parse()

	env := loadEnvFile(*envPath)

	dbURL := env["DATABASE_URL"]
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}

	sttURL := env["STT_API_URL"]
	sttKey := env["STT_API_KEY"]
	aiURL := env["AI_CHAT_COMPLETIONS_URL"]
	aiKey := env["AI_API_KEY"]
	aiModel := env["AI_MODEL"]
	if aiModel == "" {
		aiModel = "gemma4"
	}

	actorIDStr := "a7770e63-204a-485c-8a00-1512fa7dd7bd" // kairatovalmat2003@gmail.com
	actorID, _ := uuid.Parse(actorIDStr)

	ctx := context.Background()

	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to db: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	minioStore, err := objectstorage.NewMinIOStore(objectstorage.MinIOConfig{
		Endpoint:  env["OBJECT_STORAGE_ENDPOINT"],
		AccessKey: env["OBJECT_STORAGE_ACCESS_KEY"],
		SecretKey: env["OBJECT_STORAGE_SECRET_KEY"],
		Bucket:    env["OBJECT_STORAGE_BUCKET"],
		Region:    env["OBJECT_STORAGE_REGION"],
		UseSSL:    env["OBJECT_STORAGE_USE_SSL"] == "true",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure MinIO: %v\n", err)
		os.Exit(1)
	}

	repo := listening.NewPostgresRepository(pool)
	svc := listening.NewServiceWithStorage(repo, minioStore)

	sttService := listening.NewSTTService(sttURL, sttKey, aiURL, aiKey, aiModel)
	svc.WithSTTService(sttService)

	type TestTarget struct {
		ID    uuid.UUID
		Title string
	}

	var targets []TestTarget

	if *testIDFlag != "" {
		id, err := uuid.Parse(*testIDFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid test ID: %v\n", err)
			os.Exit(1)
		}
		var title string
		_ = pool.QueryRow(ctx, "SELECT title FROM listening_test_versions v JOIN listening_tests t ON t.current_version_id = v.id WHERE t.id = $1", id).Scan(&title)
		targets = append(targets, TestTarget{ID: id, Title: title})
	} else {
		rows, err := pool.Query(ctx, `
			SELECT t.id, v.title
			FROM listening_tests t
			JOIN listening_test_versions v ON v.id = t.current_version_id
			JOIN listening_parts p ON p.test_version_id = v.id
			GROUP BY t.id, v.title
			HAVING COUNT(CASE WHEN length(coalesce(p.transcript, '')) > 0 THEN 1 END) = 0
			ORDER BY v.title;
		`)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list tests: %v\n", err)
			os.Exit(1)
		}
		defer rows.Close()

		for rows.Next() {
			var t TestTarget
			if err := rows.Scan(&t.ID, &t.Title); err != nil {
				fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
				os.Exit(1)
			}
			targets = append(targets, t)
		}
	}

	fmt.Printf("Found %d test(s) to process:\n", len(targets))
	for i, t := range targets {
		fmt.Printf("  %2d) %s (%s)\n", i+1, t.Title, t.ID)
	}

	for idx, t := range targets {
		fmt.Printf("\n==================================================\n")
		fmt.Printf("[%d/%d] Starting: %s (%s)\n", idx+1, len(targets), t.Title, t.ID)
		start := time.Now()

		updated, err := svc.TranscribeTest(ctx, t.ID, actorID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR transcribing %s: %v\n", t.Title, err)
			continue
		}

		// Count timestamps and quotes
		var totalQ, withTimestamps, withQuote, withHint int
		for _, p := range updated.Parts {
			for _, g := range p.Groups {
				for _, q := range g.Questions {
					totalQ++
					if _, ok := q.Content["timestampStart"]; ok {
						withTimestamps++
					}
					if _, ok := q.Content["quote"]; ok {
						withQuote++
					}
					if _, ok := q.Content["hint"]; ok {
						withHint++
					}
				}
			}
		}

		fmt.Printf("  Transcribed in %.1fs: parts=%d, questions=%d (timestamps=%d, quotes=%d, hints=%d)\n",
			time.Since(start).Seconds(), len(updated.Parts), totalQ, withTimestamps, withQuote, withHint)

		// Publish the test
		pubStart := time.Now()
		published, valErrors, err := svc.Publish(ctx, t.ID, actorID, updated.Revision)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR publishing %s: %v\n", t.Title, err)
			continue
		}
		if len(valErrors) > 0 {
			fmt.Fprintf(os.Stderr, "  VALIDATION ERRORS publishing %s: %v\n", t.Title, valErrors)
			continue
		}

		// Also update any previous attempts for this material_id to use the new published version
		cmd, err := pool.Exec(ctx, `
			UPDATE attempts
			SET material_version_id = (SELECT published_version_id FROM listening_tests WHERE id = $1)
			WHERE material_id = $1 AND material_version_id != (SELECT published_version_id FROM listening_tests WHERE id = $1)
		`, t.ID)
		if err != nil {
			fmt.Printf("  Warning updating existing attempts: %v\n", err)
		} else {
			fmt.Printf("  Updated %d previous attempt(s) to new version\n", cmd.RowsAffected())
		}

		fmt.Printf("  Published revision %d in %.1fs (new version ID: %s)\n",
			published.Revision, time.Since(pubStart).Seconds(), published.ID)
	}

	fmt.Printf("\nAll processing complete!\n")
}

var _ = slog.Info
