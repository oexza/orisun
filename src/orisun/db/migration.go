package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed migrations/*/*.sql
var sqlScripts embed.FS

func RunDbScripts(db *sql.DB, schema string, isAdminSchema bool, ctx context.Context) error {
	// First run common migrations
	if err := runMigrationsInFolder(db, "common", schema, ctx); err != nil {
		return fmt.Errorf("failed to run common migrations: %w", err)
	}

	// If this schema is also the admin schema, run admin migrations
	if isAdminSchema {
		if err := runMigrationsInFolder(db, "admin", schema, ctx); err != nil {
			return fmt.Errorf("failed to run admin migrations: %w", err)
		}
	}

	return nil
}

func runMigrationsInFolder(db *sql.DB, folder, schema string, ctx context.Context) error {
	// Create schema if it doesn't exist and it's not public
	if schema != "public" {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
			return fmt.Errorf("failed to create schema %s: %w", schema, err)
		}
	}

	// Read all SQL files from the specific folder
	files, err := sqlScripts.ReadDir(path.Join("migrations", folder))
	if err != nil {
		return fmt.Errorf("failed to read migrations directory %s: %w", folder, err)
	}

	var combinedScript strings.Builder
	
	// Set search path for this batch of migrations
	combinedScript.WriteString(fmt.Sprintf("SET search_path TO %s;\n", schema))

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		content, err := sqlScripts.ReadFile(path.Join("migrations", folder, file.Name()))
		if err != nil {
			return fmt.Errorf("failed to read script %s: %w", file.Name(), err)
		}
		combinedScript.WriteString(string(content))
		combinedScript.WriteString("\n")
	}

	// Execute all migrations in a single transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, combinedScript.String()); err != nil {
		return fmt.Errorf("failed to execute migrations for %s: %w", folder, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
