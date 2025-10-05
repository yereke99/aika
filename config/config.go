package config

import (
	"os"
)

type Config struct {
	Token       string
	Port        string
	DBPath      string
	ChannelName string
	MiniAppURL  string
}

func NewConfig() (*Config, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		token = "7694748047:AAFwsmM57F2mUdBzqinu3cc7IOBkw1ZcDDk"
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
		Token:       token,
		Port:        port,
		DBPath:      dbPath,
		ChannelName: "@jaiAngmeAitamyz",
		MiniAppURL:  "https://erek001.bnna.dev",
	}, nil
}
