package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestMigrationsDatabaseURLFallback proves the fallback wiring run() applies
// before calling buildDeps: getenv("MIGRATIONS_DATABASE_URL", databaseURL)
// returns MIGRATIONS_DATABASE_URL's own value when it is set, and
// DATABASE_URL when it is unset.
func TestMigrationsDatabaseURLFallback(t *testing.T) {
	const databaseURL = "postgres://u@pgbouncer.example:6432/customer_ordering?sslmode=disable"

	t.Run("falls back to DATABASE_URL when MIGRATIONS_DATABASE_URL is unset", func(t *testing.T) {
		t.Setenv("MIGRATIONS_DATABASE_URL", "")
		os.Unsetenv("MIGRATIONS_DATABASE_URL")
		if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != databaseURL {
			t.Fatalf("getenv fallback = %q, want the DATABASE_URL value %q", got, databaseURL)
		}
	})

	t.Run("uses MIGRATIONS_DATABASE_URL when set, not DATABASE_URL", func(t *testing.T) {
		const direct = "postgres://u@postgres-postgresql.example:5432/customer_ordering?sslmode=disable"
		t.Setenv("MIGRATIONS_DATABASE_URL", direct)
		if got := getenv("MIGRATIONS_DATABASE_URL", databaseURL); got != direct {
			t.Fatalf("getenv = %q, want the direct MIGRATIONS_DATABASE_URL value %q", got, direct)
		}
	})
}

// TestBuildDeps_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations proves
// buildDeps itself, not just the env read above, threads migrationsDatabaseURL
// into the migration step. The runtime DSN points at a closed port; the
// migrations DSN is a malformed string migrate rejects at once. The first
// attempt's logged cause (the retry loop wraps only ctx.Err() once the
// context ends, so the log line is where the real cause lives) must be the
// parse error of the migrations DSN, never a dial error to the runtime DSN.
func TestBuildDeps_UsesMigrationsDatabaseURLNotDatabaseURLForMigrations(t *testing.T) {
	const (
		bogusMigrationsURL = "not-a-valid-connection-string"
		unreachableAppURL  = "postgres://u@127.0.0.1:1/customer_ordering?sslmode=disable&connect_timeout=1"
	)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if _, _, err := buildDeps(ctx, logger, unreachableAppURL, bogusMigrationsURL); err == nil {
		t.Fatal("a malformed MIGRATIONS_DATABASE_URL must fail boot")
	}
	out := logs.String()
	if !strings.Contains(out, "run migrations") || !strings.Contains(out, "scheme") {
		t.Fatalf("log = %q, want the migrations DSN parse error on the first attempt", out)
	}
	if strings.Contains(out, "127.0.0.1") || strings.Contains(out, "refused") {
		t.Fatalf("log = %q: migrations ran against DATABASE_URL instead of MIGRATIONS_DATABASE_URL", out)
	}
}

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// The binary reads no API key: requests carrying (or lacking) any
// Authorization header behave identically.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t)
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/.well-known/agent-card.json", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("agent card with Authorization=%q = %d, want 200", auth, resp.StatusCode)
		}
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("CO_A2A_TEST_SET", "v")
	t.Setenv("CO_A2A_TEST_EMPTY", "")
	if got := getenv("CO_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("CO_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
