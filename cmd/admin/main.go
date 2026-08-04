package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	adminservice "github.com/almatkai/ielts-after-cigarette-back/internal/admin"
	"github.com/almatkai/ielts-after-cigarette-back/internal/database"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] != "set-role" {
		fmt.Fprintln(os.Stderr, "usage: admin set-role --email user@example.com --role STUDENT|EDITOR|ADMIN")
		return 2
	}

	flags := flag.NewFlagSet("set-role", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	email := flags.String("email", "", "email of an existing user")
	role := flags.String("role", "", "new role: STUDENT, EDITOR, or ADMIN")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to PostgreSQL: %v\n", err)
		return 1
	}
	defer pool.Close()

	service := adminservice.NewRoleService(adminservice.NewPostgresRoleRepository(pool))
	result, err := service.SetRole(ctx, *email, *role)
	if err != nil {
		if errors.Is(err, adminservice.ErrUserNotFound) {
			fmt.Fprintln(os.Stderr, "user was not found")
			return 3
		}
		fmt.Fprintf(os.Stderr, "set role: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "write result: %v\n", err)
		return 1
	}
	return 0
}
