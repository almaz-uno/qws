package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// main.go serves as the entry point and delegates to root.go
// All application logic has been moved to root.go with Cobra integration

func main() {
	// Setup signal-aware context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()

	// Execute cobra command with context
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
