package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

// InitDatabase initializes the SQLite database
func InitDatabase(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create tables
	if err := CreateTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	log.Println("Database initialized successfully")
	return db, nil
}

// CreateTables creates all necessary tables
func CreateTables(db *sql.DB) error {
	tables := []struct {
		name string
		fn   func(*sql.DB) error
	}{  
		{"just", createJustTable},
		{"users", createUsersTable},
	}

	for _, table := range tables {
		log.Printf("Creating table: %s", table.name)
		if err := table.fn(db); err != nil {
			return fmt.Errorf("create %s table: %w", table.name, err)
		}
	}

	log.Println("All tables created successfully")
	return nil
}

// createJustTable creates the just table (existing)
func createJustTable(db *sql.DB) error {
	const stmt = `
	CREATE TABLE IF NOT EXISTS just (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		id_user BIGINT NOT NULL UNIQUE,
		userName VARCHAR(255) NOT NULL,
		dataRegistred VARCHAR(50) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.Exec(stmt)
	return err
}

func createUsersTable(db *sql.DB) error {
	const stmt = `
	CREATE TABLE IF NOT EXISTS users (
		id           TEXT PRIMARY KEY,
		user_id      INTEGER NOT NULL UNIQUE,
		nickname     TEXT NOT NULL,
		sex          TEXT NOT NULL,
		age          INTEGER NOT NULL,
		latitude     REAL,
		longitude    REAL,
		about_user   TEXT,
		avatar_path  TEXT,
		created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_users_user_id ON users(user_id);
	CREATE INDEX IF NOT EXISTS idx_users_lat_lon ON users(latitude, longitude);
	CREATE TRIGGER IF NOT EXISTS trg_users_updated_at
	AFTER UPDATE ON users
	FOR EACH ROW
	BEGIN
	  UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
	END;
	`
	_, err := db.Exec(stmt)
	return err
}
