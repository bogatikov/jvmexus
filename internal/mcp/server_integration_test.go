package mcp

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/gradle"
	"github.com/bgtkv/jvmexus/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestServer_IndexProjectAndGetDependencies(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repo := filepath.Join(tmp, "sample-repo")
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatalf("create module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "settings.gradle.kts"), []byte("include(\":app\")\n"), 0o644); err != nil {
		t.Fatalf("write settings file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "build.gradle.kts"), []byte(`
dependencies {
  implementation("org.slf4j:slf4j-api:2.0.13")
}
`), 0o644); err != nil {
		t.Fatalf("write build file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "app", "src", "main", "java", "com", "example"), 0o755); err != nil {
		t.Fatalf("create source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "src", "main", "java", "com", "example", "Bot.java"), []byte(`
package com.example;

import java.util.List;

public class Bot {
  public String ping() { return Util.helper(); }
}
`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "src", "main", "java", "com", "example", "Util.java"), []byte(`
package com.example;

public class Util {
  public static String helper() { return "pong"; }
}
`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	gradleHome := filepath.Join(tmp, "gradle-home")
	t.Setenv("GRADLE_USER_HOME", gradleHome)
	cachePath := filepath.Join(gradleHome, "caches", "modules-2", "files-2.1", "org.slf4j", "slf4j-api", "2.0.13", "hash")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatalf("create gradle cache: %v", err)
	}
	binaryJar := filepath.Join(cachePath, "slf4j-api-2.0.13.jar")
	sourceJar := filepath.Join(cachePath, "slf4j-api-2.0.13-sources.jar")
	if err := os.WriteFile(binaryJar, []byte("bin"), 0o644); err != nil {
		t.Fatalf("write binary jar: %v", err)
	}
	sourceFile, err := os.Create(sourceJar)
	if err != nil {
		t.Fatalf("create source jar: %v", err)
	}
	zipWriter := zip.NewWriter(sourceFile)
	entry, err := zipWriter.Create("org/slf4j/Logger.java")
	if err != nil {
		_ = sourceFile.Close()
		t.Fatalf("create source entry: %v", err)
	}
	if _, err := entry.Write([]byte("package org.slf4j; public interface Logger { void info(String msg); }")); err != nil {
		_ = sourceFile.Close()
		t.Fatalf("write source entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		_ = sourceFile.Close()
		t.Fatalf("close source zip writer: %v", err)
	}
	if err := sourceFile.Close(); err != nil {
		t.Fatalf("close source jar: %v", err)
	}

	dbPath := filepath.Join(tmp, "index.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	cfg := testConfig(tmp, dbPath)
	srv := NewServer(cfg, st)

	indexResult := mustCallTool(t, srv, "index_project", map[string]any{"path": repo})
	if indexResult["projectName"] == "" {
		t.Fatalf("index response missing projectName: %#v", indexResult)
	}
	if mode, _ := indexResult["mode"].(string); mode == "" {
		t.Fatalf("index response missing mode: %#v", indexResult)
	}
	for _, key := range []string{"changedFiles", "deletedFiles", "skippedFiles"} {
		if _, ok := indexResult[key]; !ok {
			t.Fatalf("index response missing %s: %#v", key, indexResult)
		}
	}

	projectName := filepath.Base(repo)
	depsResult := mustCallTool(t, srv, "get_dependencies", map[string]any{"project": projectName})
	if intFromAny(depsResult["total"]) != 1 {
		t.Fatalf("expected total dependencies = 1, got %#v", depsResult["total"])
	}

	stats, ok := depsResult["sourceStats"].(map[string]any)
	if !ok {
		t.Fatalf("sourceStats is not an object: %#v", depsResult["sourceStats"])
	}
	if intFromAny(stats[gradle.SourceStatusAttached]) != 1 {
		t.Fatalf("expected sourceStats[%s] = 1, got %#v", gradle.SourceStatusAttached, stats[gradle.SourceStatusAttached])
	}

	buildGraph := mustCallTool(t, srv, "get_build_graph", map[string]any{"project": projectName})
	if intFromAny(buildGraph["nodeCount"]) < 2 {
		t.Fatalf("expected build graph nodeCount >= 2, got %#v", buildGraph["nodeCount"])
	}
	if intFromAny(buildGraph["edgeCount"]) < 1 {
		t.Fatalf("expected build graph edgeCount >= 1, got %#v", buildGraph["edgeCount"])
	}

	resource := mustReadResource(t, srv, "jvminfo://project/"+projectName+"/dependencies")
	if intFromAny(resource["total"]) != 1 {
		t.Fatalf("resource total should be 1, got %#v", resource["total"])
	}
	dependencies, ok := resource["dependencies"].([]any)
	if !ok || len(dependencies) != 1 {
		t.Fatalf("resource dependencies malformed: %#v", resource["dependencies"])
	}
	dep, ok := dependencies[0].(map[string]any)
	if !ok {
		t.Fatalf("dependency entry is malformed: %#v", dependencies[0])
	}
	if dep["sourceStatus"] != gradle.SourceStatusAttached {
		t.Fatalf("expected sourceStatus %q, got %#v", gradle.SourceStatusAttached, dep["sourceStatus"])
	}

	buildGraphResource := mustReadResource(t, srv, "jvminfo://project/"+projectName+"/build-graph")
	if intFromAny(buildGraphResource["nodeCount"]) < 2 {
		t.Fatalf("expected build-graph resource nodeCount >= 2, got %#v", buildGraphResource["nodeCount"])
	}

	queryResult := mustCallTool(t, srv, "query_code", map[string]any{"project": projectName, "query": "Util helper", "limit": 5})
	if intFromAny(queryResult["total"]) < 1 {
		t.Fatalf("expected query_code total >= 1, got %#v", queryResult)
	}

	libraryQuery := mustCallTool(t, srv, "query_code", map[string]any{"project": projectName, "query": "Logger info", "scope": "libraries", "limit": 5})
	if intFromAny(libraryQuery["total"]) < 1 {
		t.Fatalf("expected library query_code total >= 1, got %#v", libraryQuery)
	}
	libraryResults, ok := libraryQuery["results"].([]any)
	if !ok || len(libraryResults) == 0 {
		t.Fatalf("expected non-empty library query results, got %#v", libraryQuery["results"])
	}
	firstLibraryResult, ok := libraryResults[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first library result object, got %#v", libraryResults[0])
	}
	if firstLibraryResult["sourceOrigin"] != "library_source" {
		t.Fatalf("expected sourceOrigin=library_source, got %#v", firstLibraryResult["sourceOrigin"])
	}

	symbolResult := mustCallTool(t, srv, "get_symbol_context", map[string]any{"project": projectName, "symbol": "ping"})
	if intFromAny(symbolResult["count"]) < 1 {
		t.Fatalf("expected at least one symbol context for ping, got %#v", symbolResult)
	}

	contexts, ok := symbolResult["contexts"].([]any)
	if !ok || len(contexts) == 0 {
		t.Fatalf("expected contexts array, got %#v", symbolResult["contexts"])
	}
	first, ok := contexts[0].(map[string]any)
	if !ok {
		t.Fatalf("expected context object, got %#v", contexts[0])
	}
	callees, ok := first["callees"].([]any)
	if !ok || len(callees) == 0 {
		t.Fatalf("expected resolved callees for ping, got %#v", first["callees"])
	}
}

func TestServer_GetDependenciesReportsNotFoundWhenSourcesMissing(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	repo := filepath.Join(tmp, "missing-sources-repo")
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatalf("create module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "settings.gradle.kts"), []byte("include(\":app\")\n"), 0o644); err != nil {
		t.Fatalf("write settings file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "build.gradle.kts"), []byte(`
dependencies {
  implementation("org.slf4j:slf4j-api:2.0.13")
}
`), 0o644); err != nil {
		t.Fatalf("write build file: %v", err)
	}

	gradleHome := filepath.Join(tmp, "gradle-home")
	t.Setenv("GRADLE_USER_HOME", gradleHome)
	if err := os.MkdirAll(filepath.Join(gradleHome, "caches", "modules-2", "files-2.1"), 0o755); err != nil {
		t.Fatalf("create empty gradle cache root: %v", err)
	}

	dbPath := filepath.Join(tmp, "index.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	cfg := testConfig(tmp, dbPath)
	srv := NewServer(cfg, st)

	indexResult := mustCallTool(t, srv, "index_project", map[string]any{"path": repo})
	if indexResult["projectName"] == "" {
		t.Fatalf("index response missing projectName: %#v", indexResult)
	}

	projectName := filepath.Base(repo)
	depsResult := mustCallTool(t, srv, "get_dependencies", map[string]any{"project": projectName})
	if intFromAny(depsResult["total"]) != 1 {
		t.Fatalf("expected total dependencies = 1, got %#v", depsResult["total"])
	}

	stats, ok := depsResult["sourceStats"].(map[string]any)
	if !ok {
		t.Fatalf("sourceStats is not an object: %#v", depsResult["sourceStats"])
	}
	if intFromAny(stats[gradle.SourceStatusNotFound]) != 1 {
		t.Fatalf("expected sourceStats[%s] = 1, got %#v", gradle.SourceStatusNotFound, stats[gradle.SourceStatusNotFound])
	}

	dependencies, ok := depsResult["dependencies"].([]any)
	if !ok || len(dependencies) != 1 {
		t.Fatalf("dependencies malformed: %#v", depsResult["dependencies"])
	}
	dep, ok := dependencies[0].(map[string]any)
	if !ok {
		t.Fatalf("dependency entry malformed: %#v", dependencies[0])
	}
	if dep["sourceStatus"] != gradle.SourceStatusNotFound {
		t.Fatalf("expected sourceStatus %q, got %#v", gradle.SourceStatusNotFound, dep["sourceStatus"])
	}
	if path, _ := dep["sourceJarPath"].(string); path != "" {
		t.Fatalf("expected empty sourceJarPath, got %q", path)
	}
}

func testConfig(tmp, dbPath string) config.Config {
	return config.Config{
		DatabasePath:           dbPath,
		EmbeddingsProvider:     "local",
		EmbeddingModelID:       "Snowflake/snowflake-arctic-embed-xs",
		ModelCacheDir:          filepath.Join(tmp, "models"),
		GradleTimeoutSeconds:   5,
		FetchMissingSources:    true,
		Offline:                true,
		SourcesDownloadTimeout: 5,
	}
}

func mustCallTool(t *testing.T, srv interface {
	HandleMessage(context.Context, json.RawMessage) mcp.JSONRPCMessage
}, name string, args map[string]any) map[string]any {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  string(mcp.MethodToolsCall),
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	resultRaw := mustRPC(t, srv, req)
	callResult, err := mcp.ParseCallToolResult(&resultRaw)
	if err != nil {
		t.Fatalf("parse call tool result: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("tool %s returned error result", name)
	}
	text := firstToolText(callResult)
	if text == "" {
		t.Fatalf("tool %s returned no text content", name)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode tool %s payload json: %v", name, err)
	}
	return payload
}

func mustReadResource(t *testing.T, srv interface {
	HandleMessage(context.Context, json.RawMessage) mcp.JSONRPCMessage
}, uri string) map[string]any {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  string(mcp.MethodResourcesRead),
		"params": map[string]any{
			"uri": uri,
		},
	}
	resultRaw := mustRPC(t, srv, req)
	readResult, err := mcp.ParseReadResourceResult(&resultRaw)
	if err != nil {
		t.Fatalf("parse read resource result: %v", err)
	}
	if len(readResult.Contents) == 0 {
		t.Fatalf("resource %s returned empty contents", uri)
	}
	textRes, ok := mcp.AsTextResourceContents(readResult.Contents[0])
	if !ok {
		t.Fatalf("resource %s first content is not text", uri)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(textRes.Text), &payload); err != nil {
		t.Fatalf("decode resource payload json: %v", err)
	}
	return payload
}

func mustRPC(t *testing.T, srv interface {
	HandleMessage(context.Context, json.RawMessage) mcp.JSONRPCMessage
}, req map[string]any) json.RawMessage {
	t.Helper()
	rawReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	msg := srv.HandleMessage(context.Background(), rawReq)
	rawResp, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  map[string]any  `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &envelope); err != nil {
		t.Fatalf("decode response envelope: %v", err)
	}
	if len(envelope.Error) > 0 {
		t.Fatalf("rpc returned error: %#v", envelope.Error)
	}
	if len(envelope.Result) == 0 {
		t.Fatalf("rpc returned empty result envelope: %s", string(rawResp))
	}
	return envelope.Result
}

func firstToolText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			return text.Text
		}
	}
	return ""
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
