package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"hectix/backend/internal/dbx"
)

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
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
