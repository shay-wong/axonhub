package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/migrate"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/ent/migrate/schemahook"
	_ "github.com/looplj/axonhub/internal/ent/runtime"
	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
)

const defaultSQLiteBusyTimeoutMs = 5000

// NewEntClient creates an Ent client. When read_replica.read_dsn is configured,
// SELECT/WITH queries are automatically routed to the replica; all writes go to master.
// Transactions always run on master. If read_dsn is empty, all queries go to master.
func NewEntClient(cfg Config) *ent.Client {
	var opts []ent.Option
	if cfg.Debug {
		opts = append(opts, ent.Debug())
	}

	masterDSN := ensureSQLiteDSN(cfg.Dialect, cfg.DSN, cfg.DisableSQLiteAutoWAL)
	dbDialect, masterDB, err := openDB(cfg.Dialect, masterDSN,
		cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)
	if err != nil {
		panic(err)
	}

	var drv dialect.Driver
	if cfg.ReadReplica.DSN != "" {
		replicaDSN := ensureSQLiteDSN(cfg.Dialect, cfg.ReadReplica.DSN, cfg.DisableSQLiteAutoWAL)
		readDialect, replicaDB, err := openDB(cfg.Dialect, replicaDSN,
			cfg.ReadReplica.MaxOpenConns, cfg.ReadReplica.MaxIdleConns,
			cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)
		if err != nil {
			panic(err)
		}
		if readDialect != dbDialect {
			panic(fmt.Errorf("read replica dialect mismatch: got %s, want %s", readDialect, dbDialect))
		}
		masterDriver := entsql.OpenDB(dbDialect, masterDB)
		replicaDriver := entsql.OpenDB(dbDialect, replicaDB)
		drv = newRouterDriver(masterDriver, replicaDriver)
	} else {
		drv = entsql.OpenDB(dbDialect, masterDB)
	}

	opts = append(opts, ent.Driver(drv))
	client := ent.NewClient(opts...)

	if !cfg.DisableAutoMigration {
		err = client.Schema.Create(
			context.Background(),
			migrate.WithGlobalUniqueID(false),
			migrate.WithForeignKeys(false),
			migrate.WithDropIndex(true),
			migrate.WithDropColumn(true),
			schema.WithHooks(schemahook.V0_3_0),
		)
		if err != nil {
			panic(err)
		}

		migrator := datamigrate.NewMigrator(client)
		if err := migrator.Run(context.Background()); err != nil {
			panic(err)
		}
	}

	if err := backfillRequestExecutionSource(context.Background(), client); err != nil {
		panic(err)
	}

	return client
}

func backfillRequestExecutionSource(ctx context.Context, client *ent.Client) error {
	hasSourceColumn, err := requestExecutionsSourceColumnExists(ctx, client)
	if err != nil {
		return err
	}
	if !hasSourceColumn {
		return nil
	}

	if client.Driver().Dialect() == dialect.MySQL {
		return client.Driver().Exec(ctx, `
UPDATE request_executions re
JOIN requests r ON re.request_id = r.id
SET re.source = r.source
WHERE re.source <> r.source`, []any{}, nil)
	}

	return client.Driver().Exec(ctx, `
UPDATE request_executions
SET source = requests.source
FROM requests
WHERE request_executions.request_id = requests.id
    AND request_executions.source <> requests.source`, []any{}, nil)
}

func requestExecutionsSourceColumnExists(ctx context.Context, client *ent.Client) (bool, error) {
	sqlDB, ok := unwrapSQLDriver(client.Driver())
	if !ok {
		return true, nil
	}

	switch client.Driver().Dialect() {
	case dialect.MySQL:
		var count int
		err := sqlDB.DB().QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = DATABASE()
    AND table_name = 'request_executions'
    AND column_name = 'source'`).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("failed to inspect request_executions source column: %w", err)
		}

		return count > 0, nil
	case dialect.Postgres:
		var count int
		err := sqlDB.DB().QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = ANY (current_schemas(false))
    AND table_name = 'request_executions'
    AND column_name = 'source'`).Scan(&count)
		if err != nil {
			return false, fmt.Errorf("failed to inspect request_executions source column: %w", err)
		}

		return count > 0, nil
	default:
		rows, err := sqlDB.DB().QueryContext(ctx, `PRAGMA table_info(request_executions)`)
		if err != nil {
			return false, fmt.Errorf("failed to inspect request_executions columns: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				cid        int
				name       string
				columnType string
				notNull    int
				defaultVal any
				pk         int
			)
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
				return false, fmt.Errorf("failed to scan request_executions column metadata: %w", err)
			}
			if name == "source" {
				return true, nil
			}
		}
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("failed to iterate request_executions columns: %w", err)
		}

		return false, nil
	}
}

func unwrapSQLDriver(driver dialect.Driver) (*entsql.Driver, bool) {
	switch drv := driver.(type) {
	case *entsql.Driver:
		return drv, true
	case *dialect.DebugDriver:
		return unwrapSQLDriver(drv.Driver)
	case *routerDriver:
		return unwrapSQLDriver(drv.master)
	default:
		return nil, false
	}
}

// ensureSQLiteDSN appends SQLite PRAGMA DSN parameters for modernc.org/sqlite when absent.
// Users can override any pragma by setting it explicitly in the DSN.
func ensureSQLiteDSN(dialectName, dsn string, disableWAL bool) string {
	switch dialectName {
	case "sqlite3", "sqlite":
		if !disableWAL && !strings.Contains(dsn, "journal_mode") {
			dsn = appendSQLiteDSNParam(dsn, "_pragma=journal_mode(WAL)")
		}
		if !strings.Contains(dsn, "busy_timeout") {
			dsn = appendSQLiteDSNParam(dsn, fmt.Sprintf("_pragma=busy_timeout(%d)", defaultSQLiteBusyTimeoutMs))
		}
	}
	return dsn
}

func appendSQLiteDSNParam(dsn, param string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + param
	}

	return dsn + "?" + param
}

// openDB opens a sql.DB for the given dialect and DSN, applies pool settings,
// and returns the ent dialect string along with the DB handle.
func openDB(dialectName, dsn string, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) (string, *sql.DB, error) {
	ed, err := entDialect(dialectName)
	if err != nil {
		return "", nil, err
	}

	drvName, err := driverName(dialectName)
	if err != nil {
		return "", nil, err
	}

	sqlDB, err := sql.Open(drvName, dsn)
	if err != nil {
		return "", nil, err
	}

	if maxOpen > 0 {
		sqlDB.SetMaxOpenConns(maxOpen)
	}
	if maxIdle > 0 {
		sqlDB.SetMaxIdleConns(maxIdle)
	}
	if maxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(maxLifetime)
	}
	if maxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(maxIdleTime)
	}

	return ed, sqlDB, nil
}

func driverName(dialectName string) (string, error) {
	switch dialectName {
	case "postgres", "pgx", "postgresdb", "pg", "postgresql":
		return "pgx", nil
	case "sqlite3", "sqlite":
		return "sqlite3", nil
	case "mysql", "tidb":
		return "mysql", nil
	default:
		return "", fmt.Errorf("invalid dialect: %s", dialectName)
	}
}

func entDialect(dialectName string) (string, error) {
	switch dialectName {
	case "postgres", "pgx", "postgresdb", "pg", "postgresql":
		return dialect.Postgres, nil
	case "sqlite3", "sqlite":
		return dialect.SQLite, nil
	case "mysql", "tidb":
		return dialect.MySQL, nil
	default:
		return "", fmt.Errorf("invalid dialect: %s", dialectName)
	}
}
