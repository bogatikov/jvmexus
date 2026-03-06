package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/indexer"
	"github.com/bgtkv/jvmexus/internal/store"
)

func main() {
	path := flag.String("path", ".", "project path to index")
	force := flag.Bool("force", false, "force full reindex")
	flag.Parse()

	cfg := config.FromEnv()

	s, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		log.Fatalf("migrate store: %v", err)
	}

	ix := indexer.New(s, cfg)
	result, err := ix.IndexProject(ctx, *path, indexer.Options{
		Force: *force,
		Progress: func(message string) {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), message)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "index failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("indexed project=%s modules=%d files=%d warnings=%d\n", result.ProjectName, result.ModuleCount, result.FileCount, len(result.Warnings))
}
