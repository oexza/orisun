package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func RunDbScripts(db *sql.DB) error {
	scriptsDir := "../db_scripts"
	files, err := os.ReadDir(scriptsDir)
	if err != nil {
		return fmt.Errorf("failed to read db_scripts directory: %w", err)
	}

	var combinedScript strings.Builder

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".sql" {
			scriptPath := filepath.Join(scriptsDir, file.Name())
			content, err := os.ReadFile(scriptPath)
			if err != nil {
				return fmt.Errorf("failed to read script %s: %w", file.Name(), err)
			}
			combinedScript.WriteString(string(content))
			combinedScript.WriteString("\n")
		}
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