package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabasePath           string
	EmbeddingsProvider     string
	EmbeddingModelID       string
	ModelCacheDir          string
	GradleTimeoutSeconds   int
	FetchMissingSources    bool
	Offline                bool
	SourcesDownloadTimeout int
}

func FromEnv() Config {
	dbPath := os.Getenv("JVMEXUS_DB_PATH")
	if dbPath == "" {
		dbPath = ".jvmexus/index.db"
	}

	modelCacheDir := os.Getenv("JVMEXUS_MODEL_CACHE_DIR")
	if modelCacheDir == "" {
		modelCacheDir = ".jvmexus/models"
	}

	return Config{
		DatabasePath:           dbPath,
		EmbeddingsProvider:     envOrDefault("EMBEDDINGS_PROVIDER", "local"),
		EmbeddingModelID:       envOrDefault("EMBEDDINGS_MODEL_ID", "Snowflake/snowflake-arctic-embed-xs"),
		ModelCacheDir:          modelCacheDir,
		GradleTimeoutSeconds:   envIntOrDefault("JVMEXUS_GRADLE_TIMEOUT_SECONDS", 120),
		FetchMissingSources:    envBoolOrDefault("JVMEXUS_FETCH_MISSING_SOURCES", true),
		Offline:                envBoolOrDefault("JVMEXUS_OFFLINE", false),
		SourcesDownloadTimeout: envIntOrDefault("JVMEXUS_SOURCES_DOWNLOAD_TIMEOUT_SECONDS", 60),
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envIntOrDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	if raw == "1" || raw == "true" || raw == "yes" || raw == "on" {
		return true
	}
	if raw == "0" || raw == "false" || raw == "no" || raw == "off" {
		return false
	}
	return fallback
}
