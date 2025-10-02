package main

import (
	"aika/config"
	"aika/internal/handler"
	"aika/traits/database"
	"aika/traits/logger"
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-telegram/bot"
	"go.uber.org/zap"
)

func main() {
	zapLogger, err := logger.NewLogger()
	if err != nil {
		panic(err)
	}

	cfg, err := config.NewConfig()
	if err != nil {
		zapLogger.Error("error initializing config", zap.Error(err))
		return
	}

	// Initialize database
	db, err := database.InitDatabase(cfg.DBPath)
	if err != nil {
		zapLogger.Error("error initializing database", zap.Error(err))
		return
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	handl := handler.NewHandler(zapLogger, cfg, ctx, db)
	opts := []bot.Option{
		bot.WithDefaultHandler(handl.DefaultHandler),
	}

	b, err := bot.New(cfg.Token, opts...)
	if err != nil {
		zapLogger.Error("error in start bot", zap.Error(err))
		return
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		zapLogger.Info("Bot stopped successfully")
		cancel()
	}()

	go handl.StartWebServer(ctx, b)
	zapLogger.Info("Starting web server", zap.String("port", cfg.Port))
	zapLogger.Info("Bot started successfully")
	b.Start(ctx)
}
