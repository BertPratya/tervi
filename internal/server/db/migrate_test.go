package db_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
)

func TestMigrateConcurrent(t *testing.T) {
	pool, _ := dbtest.NewEmpty(t)
	start := make(chan struct{})
	versions := make(chan int64, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			version, err := db.Migrate(context.Background(), pool)
			if err != nil {
				errs <- err
				return
			}
			versions <- version
		}()
	}
	close(start)
	wg.Wait()
	close(versions)
	close(errs)
	for err := range errs {
		t.Errorf("Migrate() error = %v", err)
	}
	successes := 0
	for version := range versions {
		successes++
		if version != 1 {
			t.Errorf("Migrate() version = %d, want 1", version)
		}
	}
	if successes != 2 {
		t.Fatalf("successful Migrate() calls = %d, want 2", successes)
	}
	var duplicates int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM (
		SELECT version_id FROM goose_db_version WHERE is_applied GROUP BY version_id HAVING count(*) > 1
	) duplicates`).Scan(&duplicates); err != nil {
		t.Fatalf("check migration records: %v", err)
	}
	if duplicates != 0 {
		t.Fatalf("found %d duplicate applied migration versions", duplicates)
	}
}

func TestCheckVersion(t *testing.T) {
	t.Run("migrated", func(t *testing.T) {
		pool := dbtest.New(t)
		if err := db.CheckVersion(context.Background(), pool); err != nil {
			t.Fatalf("CheckVersion() error = %v, want nil", err)
		}
	})

	t.Run("empty database", func(t *testing.T) {
		pool, _ := dbtest.NewEmpty(t)
		err := db.CheckVersion(context.Background(), pool)
		if !errors.Is(err, db.ErrNotMigrated) {
			t.Fatalf("CheckVersion() error = %v, want ErrNotMigrated", err)
		}
		var tableCount int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema()`).Scan(&tableCount); err != nil {
			t.Fatalf("count tables after CheckVersion: %v", err)
		}
		if tableCount != 0 {
			t.Fatalf("CheckVersion() created %d tables", tableCount)
		}
	})

	t.Run("newer database", func(t *testing.T) {
		pool := dbtest.New(t)
		if _, err := pool.Exec(context.Background(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (2, true)`); err != nil {
			t.Fatalf("insert newer migration version: %v", err)
		}
		err := db.CheckVersion(context.Background(), pool)
		if !errors.Is(err, db.ErrVersionMismatch) {
			t.Fatalf("CheckVersion() error = %v, want ErrVersionMismatch", err)
		}
		if want := "database is at version 2, newer than this program (version 1): use a newer program"; err.Error() != want {
			t.Fatalf("CheckVersion() error = %q, want %q", err, want)
		}
	})
}
