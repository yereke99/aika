package config

import (
	"os"
)

type Config struct {
	Token  string
	Port   string
	DBPath string
}

func NewConfig() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		token = "8159719184:AAF-uQXUxzPXFjTS0A8-HR--FkEbsIvqtRg"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./aika.db"
	}

	return &Config{
		Token:  token,
		Port:   port,
		DBPath: dbPath,
	}, nil
}
