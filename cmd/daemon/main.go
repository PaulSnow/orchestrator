package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/PaulSnow/orchestrator/internal/orchestrator"
)

func main() {
	port := flag.Int("port", 8100, "Port to listen on")
	flag.Parse()

	fmt.Println("=========================================")
	fmt.Println("  Orchestrator Hub Dashboard")
	fmt.Println("=========================================")
	fmt.Println()

	ds := orchestrator.NewDaemonServer(*port)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start daemon server
	if err := ds.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Dashboard running at http://localhost:%d\n", *port)
	fmt.Println("Press Ctrl+C to stop")

	// Wait for signal
	sig := <-sigCh
	fmt.Printf("\nReceived %s, shutting down...\n", sig)
	ds.Stop()
	fmt.Println("Daemon stopped.")
}
