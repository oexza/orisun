package db

import (
	"database/sql"
	"fmt"
	config "orisun/src/orisun/config"

	_ "github.com/lib/pq"
)

func getDb(config *config.AppConfig) (*sql.DB, error) {
	// Connect to the database
	connStr := fmt.Sprintf("user=%s dbname=%s password=%s sslmode=disable host=%s port=%s",
		config.Postgres.User, config.Postgres.Name, config.Postgres.Password, config.Postgres.Host, config.Postgres.Port)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	fmt.Println("Connected to the database")

	return db, nil
}
