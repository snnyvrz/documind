package main

import (
	"database/sql"
	"embed"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func runMigrations(databaseURL string) error {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		return err
	}
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if owner := os.Getenv("LEGACY_DOCUMENT_OWNER"); owner != "" {
		if _, err := database.Exec("SELECT set_config('app.legacy_document_owner', $1, false)", owner); err != nil {
			return err
		}
	}
	// Goose's advisory lock serializes migration startup across API replicas.
	if err := goose.Up(database, "migrations"); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

func migrateCommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "migrate" {
		return false
	}
	if err := runMigrations(os.Getenv("DATABASE_URL")); err != nil {
		panic(err)
	}
	return true
}
