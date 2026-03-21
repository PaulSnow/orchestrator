package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PaulSnow/orchestrator/internal/daemon"
)

func main() {
	port := flag.Int("port", daemon.DefaultPort, "Port to listen on")
	flag.Parse()

	fmt.Println("=========================================")
	fmt.Println("  Orchestrator Registration Daemon")
	fmt.Println("=========================================")
	fmt.Println()

	d := daemon.New(*port)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start cleanup goroutine to periodically remove dead orchestrators
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if cleaned := d.CleanupStale(); cleaned > 0 {
				fmt.Printf("Cleaned up %d stale orchestrator(s)\n", cleaned)
			}
		}
	}()

	// Start daemon in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start()
	}()

	// Wait for signal or error
	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived %s, shutting down...\n", sig)
		if err := d.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
		}
		fmt.Println("Daemon stopped.")
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
			os.Exit(1)
		}
	}
}
