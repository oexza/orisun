package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

//go:embed scripts/common/*.sql
var sqlScripts embed.FS

//go:embed scripts/admin/*.sql
var adminSqlScripts embed.FS

func RunDbScripts(db *sql.DB, schema string, isAdminSchema bool, ctx context.Context) error {
	// First run common migrations
	if err := runMigrationsInFolder(db, sqlScripts, schema, ctx); err != nil {
		return fmt.Errorf("failed to run common migrations: %w", err)
	}

	// If this is admin schema, run admin migrations
	if isAdminSchema {
		if err := runMigrationsInFolder(db, adminSqlScripts, schema, ctx); err != nil {
			return fmt.Errorf("failed to run admin migrations: %w", err)
		}
	}
	return nil
}

func runMigrationsInFolder(db *sql.DB, scripts embed.FS, schema string, ctx context.Context) error {
	// Schema creation code remains the same
	if schema != "public" {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
			return fmt.Errorf("failed to create schema %s: %w", schema, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit schema creation: %w", err)
		}
	}

	var combinedScript strings.Builder
	combinedScript.WriteString(fmt.Sprintf("SET search_path TO %s;\n", schema))

	// Read and execute all SQL files from the embedded filesystem
	files, err := scripts.ReadDir(".")
	if err != nil {
		return fmt.Errorf("failed to read scripts: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		content, err := scripts.ReadFile(file.Name())
		if err != nil {
			return fmt.Errorf("failed to read script %s: %w", file.Name(), err)
		}
		combinedScript.WriteString(string(content))
		combinedScript.WriteString("\n")
	}

	// Transaction execution remains the same
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, combinedScript.String()); err != nil {
		return fmt.Errorf("failed to execute migrations for %s: %w", scripts, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
