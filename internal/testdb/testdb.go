package testdb

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"hectix/backend/internal/dbx"
)

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	if dbURL := os.Getenv("TEST_DATABASE_URL"); dbURL != "" {
		if !strings.Contains(dbURL, "test") {
			t.Fatalf("testdb: TEST_DATABASE_URL must target a test database (containing 'test' in name), got: %s", dbURL)
		}
		adminPool, err := dbx.Open(ctx, dbURL)
		if err != nil {
			t.Fatalf("testdb: connect: %v", err)
		}
		if _, err := adminPool.Exec(ctx, "drop schema public cascade; create schema public;"); err != nil {
			adminPool.Close()
			t.Fatalf("testdb: reset schema: %v", err)
		}
		adminPool.Close()

		if err := dbx.Migrate(dbURL); err != nil {
			t.Fatalf("testdb: migrate: %v", err)
		}
		pool, err := dbx.Open(ctx, dbURL)
		if err != nil {
			t.Fatalf("testdb: open: %v", err)
		}
		t.Cleanup(pool.Close)
		return pool
	}

	c, err := tcpg.Run(ctx, "postgres:17",
		tcpg.WithDatabase("hectix"), tcpg.WithUsername("hectix"), tcpg.WithPassword("hectix"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testcontainers.TerminateContainer(c) })
	url := c.MustConnectionString(ctx, "sslmode=disable", "timezone=UTC")
	if err := dbx.Migrate(url); err != nil {
		t.Fatal(err)
	}
	pool, err := dbx.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
