package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"businessplan/usbvault/internal/app"
)

func main() {
	logger := log.New(os.Stdout, "[usbvault] ", log.LstdFlags|log.Lmicroseconds)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	application, err := app.New(logger)
	if err != nil {
		logger.Fatalf("failed to initialize app: %v", err)
	}
	defer func() {
		if err := application.Close(); err != nil {
			logger.Printf("close error: %v", err)
		}
	}()

	if err := application.Run(ctx); err != nil {
		logger.Fatalf("server error: %v", err)
	}
}
