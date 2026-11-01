package main

import (
	"context"
	"testing"

	"hectix/backend/internal/config"
	"hectix/backend/internal/testdb"
)

func TestOrderStatusJobRejectsBadPayload(t *testing.T) {
	h := jobHandlers(config.Config{}, nil)["order.status"]
	if h == nil {
		t.Fatal("no order.status handler")
	}
	for _, p := range []string{`{"status":"paid"}`, `{"orderId":"o1"}`, `{"orderId":"","status":"paid"}`, `not json`} {
		if err := h(context.Background(), []byte(p)); err == nil {
			t.Fatalf("%s: want error", p)
		}
	}
}

func TestOrderStatusJobAppliesStatus(t *testing.T) {
	db := testdb.New(t)
	h := jobHandlers(config.Config{}, db)["order.status"]
	if err := h(context.Background(), []byte(`{"orderId":"missing","status":"expired"}`)); err != nil {
		t.Fatal(err)
	}
	if err := h(context.Background(), []byte(`{"orderId":"missing","status":"refunded"}`)); err == nil {
		t.Fatal("want error")
	}
}
