package db

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	config "orisun/config"
)

func getDb(config *config.AppConfig) (*sql.DB, error) {
	// Connect to the database
	connStr := fmt.Sprintf("user=%s dbname=%s password=%s sslmode=disable host=%s port=%s",
		config.DB.User, config.DB.Name, config.DB.Password, config.DB.Host, config.DB.Port)
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
