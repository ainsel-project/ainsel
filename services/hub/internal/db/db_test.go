package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/dbtest"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Main(m))
}

func TestOpenPingsTheDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := dbtest.OpenEmpty(t)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
