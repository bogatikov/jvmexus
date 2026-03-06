package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/mcp"
	"github.com/bgtkv/jvmexus/internal/store"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	ctx := context.Background()
	cfg := config.FromEnv()

	s, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer s.Close()

	if err := s.Migrate(ctx); err != nil {
		log.Fatalf("migrate store: %v", err)
	}

	mcpServer := mcp.NewServer(cfg, s)

	if err := server.ServeStdio(mcpServer); err != nil {
		fmt.Fprintf(os.Stderr, "mcp server error: %v\n", err)
		os.Exit(1)
	}
}
