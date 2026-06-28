package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/looplj/axonhub/internal/pkg/sqlite"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
)

func TestEnsureSQLiteDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dialect    string
		dsn        string
		disableWAL bool
		want       string
	}{
		{
			name:    "postgres unchanged",
			dialect: "postgres",
			dsn:     "postgres://localhost/axonhub",
			want:    "postgres://localhost/axonhub",
		},
		{
			name:    "sqlite adds wal and busy timeout",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?cache=shared&_fk=1",
			want:    "file:axonhub.db?cache=shared&_fk=1&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name:    "sqlite without query params",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db",
			want:    "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name:       "wal disabled still adds busy timeout",
			dialect:    "sqlite3",
			dsn:        "file:axonhub.db",
			disableWAL: true,
			want:       "file:axonhub.db?_pragma=busy_timeout(5000)",
		},
		{
			name:    "existing wal preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=journal_mode(DELETE)",
			want:    "file:axonhub.db?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)",
		},
		{
			name:    "existing busy timeout preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=busy_timeout(10000)",
			want:    "file:axonhub.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)",
		},
		{
			name:    "both pragmas preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)",
			want:    "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ensureSQLiteDSN(tt.dialect, tt.dsn, tt.disableWAL)
			if got != tt.want {
				t.Fatalf("ensureSQLiteDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackfillRequestExecutionSource(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = authz.WithTestBypass(ctx)

	req, err := client.Request.Create().
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusFailed).
		SetSource(request.SourceTest).
		Save(ctx)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	exec, err := client.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(1).
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusFailed).
		Save(ctx)
	if err != nil {
		t.Fatalf("create request execution: %v", err)
	}
	if exec.Source != requestexecution.SourceAPI {
		t.Fatalf("new execution default source = %q, want %q", exec.Source, requestexecution.SourceAPI)
	}

	if err := backfillRequestExecutionSource(ctx, client); err != nil {
		t.Fatalf("backfillRequestExecutionSource() error = %v", err)
	}

	got, err := client.RequestExecution.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("get request execution: %v", err)
	}
	if got.Source != requestexecution.SourceTest {
		t.Fatalf("backfilled source = %q, want %q", got.Source, requestexecution.SourceTest)
	}
}

func TestNewEntClientBackfillsRequestExecutionSourceWhenAutoMigrationDisabled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "axonhub.db")
	dsn := "file:" + dbPath + "?_fk=0"

	seedClient := enttest.NewEntClient(t, "sqlite3", dsn)
	ctx := context.Background()
	ctx = ent.NewContext(ctx, seedClient)
	ctx = authz.WithTestBypass(ctx)

	req, err := seedClient.Request.Create().
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(request.StatusFailed).
		SetSource(request.SourceTest).
		Save(ctx)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	exec, err := seedClient.RequestExecution.Create().
		SetRequestID(req.ID).
		SetChannelID(1).
		SetModelID("gpt-4").
		SetRequestBody(objects.JSONRawMessage(`{}`)).
		SetStatus(requestexecution.StatusFailed).
		Save(ctx)
	if err != nil {
		t.Fatalf("create request execution: %v", err)
	}
	if err := seedClient.Close(); err != nil {
		t.Fatalf("close seed client: %v", err)
	}

	client := NewEntClient(Config{
		Dialect:              "sqlite3",
		DSN:                  dsn,
		DisableAutoMigration: true,
		DisableSQLiteAutoWAL: true,
	})
	defer client.Close()

	ctx = ent.NewContext(context.Background(), client)
	ctx = authz.WithTestBypass(ctx)

	got, err := client.RequestExecution.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("get request execution: %v", err)
	}
	if got.Source != requestexecution.SourceTest {
		t.Fatalf("backfilled source with auto migration disabled = %q, want %q", got.Source, requestexecution.SourceTest)
	}
}

func TestNewEntClientSkipsRequestExecutionSourceBackfillWhenColumnMissing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "axonhub-old.db")
	dsn := "file:" + dbPath + "?_fk=0"

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.Exec(`
CREATE TABLE requests (
    id INTEGER PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'api'
);
CREATE TABLE request_executions (
    id INTEGER PRIMARY KEY,
    request_id INTEGER NOT NULL
);
INSERT INTO requests (id, source) VALUES (1, 'test');
INSERT INTO request_executions (id, request_id) VALUES (1, 1);`)
	if err != nil {
		t.Fatalf("create old schema fixture: %v", err)
	}

	client := NewEntClient(Config{
		Dialect:              "sqlite3",
		DSN:                  dsn,
		DisableAutoMigration: true,
		DisableSQLiteAutoWAL: true,
	})
	defer client.Close()
}
