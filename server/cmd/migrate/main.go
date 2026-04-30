package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDatabaseURL = "postgres://gatepilot:gatepilot_dev@127.0.0.1:5432/gatepilot?sslmode=disable"

type migration struct {
	Version  string
	Name     string
	UpPath   string
	DownPath string
}

func main() {
	log.SetFlags(0)
	if len(os.Args) != 2 {
		log.Fatalf("usage: migrate <up|down|status>")
	}

	if err := run(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}

func run(command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	migrations, err := discoverMigrations(migrationsDir)
	if err != nil {
		return err
	}

	db, err := sql.Open("pgx", databaseURL())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	if err := ensureMigrationTable(ctx, db); err != nil {
		return err
	}

	switch command {
	case "up":
		return migrateUp(ctx, db, migrations)
	case "down":
		return migrateDown(ctx, db, migrations)
	case "status":
		return migrationStatus(ctx, db, migrations)
	default:
		return fmt.Errorf("unknown command %q, want up, down, or status", command)
	}
}

func resolveMigrationsDir() (string, error) {
	if dir := os.Getenv("GATEPILOT_MIGRATIONS_DIR"); dir != "" {
		return dir, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	candidates := []string{
		filepath.Join(wd, "migrations"),
		filepath.Join(wd, "server", "migrations"),
		filepath.Join(wd, "..", "..", "migrations"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("migration directory not found; set GATEPILOT_MIGRATIONS_DIR")
}

func discoverMigrations(dir string) ([]migration, error) {
	upFiles, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return nil, err
	}
	if len(upFiles) == 0 {
		return nil, fmt.Errorf("no up migrations found in %s", dir)
	}

	migrations := make([]migration, 0, len(upFiles))
	for _, upPath := range upFiles {
		base := filepath.Base(upPath)
		name := strings.TrimSuffix(base, ".up.sql")
		version := strings.SplitN(name, "_", 2)[0]
		downPath := filepath.Join(dir, name+".down.sql")
		if _, err := os.Stat(downPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("missing down migration for %s", base)
			}
			return nil, err
		}
		migrations = append(migrations, migration{
			Version:  version,
			Name:     name,
			UpPath:   upPath,
			DownPath: downPath,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Name < migrations[j].Name
	})
	return migrations, nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version varchar(32) PRIMARY KEY,
    name varchar(255) NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

func migrateUp(ctx context.Context, db *sql.DB, migrations []migration) error {
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}

	for _, item := range migrations {
		if applied[item.Version] {
			fmt.Printf("skip %s\n", item.Name)
			continue
		}
		if err := applyMigration(ctx, db, item.UpPath, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name) VALUES ($1, $2)", item.Version, item.Name)
			return err
		}); err != nil {
			return fmt.Errorf("apply %s: %w", item.Name, err)
		}
		fmt.Printf("applied %s\n", item.Name)
	}
	return nil
}

func migrateDown(ctx context.Context, db *sql.DB, migrations []migration) error {
	applied, err := appliedMigrationNames(ctx, db)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("no applied migrations")
		return nil
	}

	latest := applied[len(applied)-1]
	for i := len(migrations) - 1; i >= 0; i-- {
		if migrations[i].Version != latest.version {
			continue
		}
		item := migrations[i]
		if err := applyMigration(ctx, db, item.DownPath, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = $1", item.Version)
			return err
		}); err != nil {
			return fmt.Errorf("rollback %s: %w", item.Name, err)
		}
		fmt.Printf("rolled back %s\n", item.Name)
		return nil
	}
	return fmt.Errorf("applied migration %s has no local down file", latest.name)
}

func migrationStatus(ctx context.Context, db *sql.DB, migrations []migration) error {
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	for _, item := range migrations {
		state := "pending"
		if applied[item.Version] {
			state = "applied"
		}
		fmt.Printf("%s %s\n", state, item.Name)
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, path string, afterSQL func(*sql.Tx) error) error {
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
		return err
	}
	if err := afterSQL(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

type appliedMigration struct {
	version string
	name    string
}

func appliedMigrationNames(ctx context.Context, db *sql.DB) ([]appliedMigration, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, name FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []appliedMigration{}
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.version, &item.name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func databaseURL() string {
	if value := os.Getenv("GATEPILOT_DATABASE_URL"); value != "" {
		return value
	}
	return getenv("DATABASE_URL", defaultDatabaseURL)
}
