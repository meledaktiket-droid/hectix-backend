package testdb

import (
	"context"
	"testing"
)

func TestMigratedSchemaHasCheckConstraint(t *testing.T) {
	pool := New(t)
	var n int
	err := pool.QueryRow(context.Background(),
		`select count(*) from pg_constraint where conname = 'ticket_type_sold_within_quantity'`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestSessionTimeZoneIsUTC(t *testing.T) {
	pool := New(t)
	var tz string
	if err := pool.QueryRow(context.Background(), `show timezone`).Scan(&tz); err != nil || tz != "UTC" {
		t.Fatalf("tz=%q err=%v", tz, err)
	}
}
