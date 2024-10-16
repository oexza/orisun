package db

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

//go:embed migrations/*.sql
var sqlScripts embed.FS

func RunDbScripts(db *sql.DB) error {
	var combinedScript strings.Builder

	// Read all SQL files from the embedded filesystem
	files, err := sqlScripts.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue // Skip directories
		}

		content, err := sqlScripts.ReadFile("migrations/" + file.Name())
		if err != nil {
			return fmt.Errorf("failed to read script %s: %w", file.Name(), err)
		}
		combinedScript.WriteString(string(content))
		combinedScript.WriteString("\n")
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // This will be a no-op if the transaction is committed

	_, err = tx.Exec(combinedScript.String())
	if err != nil {
		return fmt.Errorf("failed to execute combined script: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Println("All scripts executed successfully in a single transaction")
	return nil
}