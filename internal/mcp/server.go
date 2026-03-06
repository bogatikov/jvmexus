package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/indexer"
	"github.com/bgtkv/jvmexus/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewServer(cfg config.Config, st *store.Store) *server.MCPServer {
	srv := server.NewMCPServer(
		"JVMexus",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	registerListProjectsTool(srv, st)
	registerIndexProjectTool(srv, st, cfg)
	registerGetDependenciesTool(srv, st)
	registerGetBuildGraphTool(srv)
	registerQueryCodeTool(srv)
	registerGetSymbolContextTool(srv)
	registerResources(srv, st)

	return srv
}

func registerResources(srv *server.MCPServer, st *store.Store) {
	projectsResource := mcp.NewResource(
		"jvminfo://projects",
		"Indexed Projects",
		mcp.WithResourceDescription("List of indexed JVM projects"),
		mcp.WithMIMEType("application/json"),
	)
	srv.AddResource(projectsResource, func(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		projects, err := st.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		payload, err := toJSON(map[string]any{"projects": projects})
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "jvminfo://projects",
				MIMEType: "application/json",
				Text:     payload,
			},
		}, nil
	})

	summaryTemplate := mcp.NewResourceTemplate(
		"jvminfo://project/{name}/summary",
		"Project Summary",
		mcp.WithTemplateDescription("Summary including dependency source attachment stats"),
		mcp.WithTemplateMIMEType("application/json"),
	)
	srv.AddResourceTemplate(summaryTemplate, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		name := templateValue(request.Params.URI, summaryURI)
		if name == "" {
			return nil, fmt.Errorf("invalid summary uri: %s", request.Params.URI)
		}
		project, err := st.FindProject(ctx, name)
		if err != nil {
			return nil, err
		}
		dependencies, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, false)
		if err != nil {
			return nil, err
		}
		sourceStats := map[string]int{}
		for _, dep := range dependencies {
			sourceStats[dep.SourceStatus]++
		}
		payload, err := toJSON(map[string]any{
			"project":     project,
			"totalDeps":   len(dependencies),
			"sourceStats": sourceStats,
		})
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     payload,
			},
		}, nil
	})

	depsTemplate := mcp.NewResourceTemplate(
		"jvminfo://project/{name}/dependencies",
		"Project Dependencies",
		mcp.WithTemplateDescription("Dependencies with source attachment metadata"),
		mcp.WithTemplateMIMEType("application/json"),
	)
	srv.AddResourceTemplate(depsTemplate, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		name := templateValue(request.Params.URI, depsURI)
		if name == "" {
			return nil, fmt.Errorf("invalid dependencies uri: %s", request.Params.URI)
		}
		project, err := st.FindProject(ctx, name)
		if err != nil {
			return nil, err
		}
		dependencies, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, false)
		if err != nil {
			return nil, err
		}
		payload, err := toJSON(map[string]any{
			"project":      project,
			"total":        len(dependencies),
			"dependencies": dependencies,
		})
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      request.Params.URI,
				MIMEType: "application/json",
				Text:     payload,
			},
		}, nil
	})
}

var (
	summaryURI = regexp.MustCompile(`^jvminfo://project/([^/]+)/summary$`)
	depsURI    = regexp.MustCompile(`^jvminfo://project/([^/]+)/dependencies$`)
)

func templateValue(uri string, re *regexp.Regexp) string {
	match := re.FindStringSubmatch(uri)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func toJSON(value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func registerGetBuildGraphTool(srv *server.MCPServer) {
	tool := mcp.NewTool(
		"get_build_graph",
		mcp.WithDescription("Return build graph nodes/edges (placeholder for upcoming phase)"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
	)
	srv.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		payload := map[string]any{
			"project": projectArg,
			"status":  "not_implemented",
			"phase":   "Phase 4 - Build Graph and Cross-Graph Linking",
		}
		return jsonToolResult(payload)
	})
}

func registerQueryCodeTool(srv *server.MCPServer) {
	tool := mcp.NewTool(
		"query_code",
		mcp.WithDescription("Hybrid code query over BM25 + vectors (placeholder for upcoming phase)"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language query")),
	)
	srv.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		payload := map[string]any{
			"project": projectArg,
			"query":   query,
			"status":  "not_implemented",
			"phase":   "Phase 5 - Local Embeddings + Hybrid Retrieval",
		}
		return jsonToolResult(payload)
	})
}

func registerGetSymbolContextTool(srv *server.MCPServer) {
	tool := mcp.NewTool(
		"get_symbol_context",
		mcp.WithDescription("Return symbol context with callers/callees/importers (placeholder for upcoming phase)"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol or fully-qualified name")),
	)
	srv.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		symbol, err := req.RequireString("symbol")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		payload := map[string]any{
			"project": projectArg,
			"symbol":  symbol,
			"status":  "not_implemented",
			"phase":   "Phase 3 - Java/Kotlin Symbol Extraction",
		}
		return jsonToolResult(payload)
	})
}

func registerListProjectsTool(srv *server.MCPServer, st *store.Store) {
	tool := mcp.NewTool(
		"list_projects",
		mcp.WithDescription("List indexed projects"),
	)

	srv.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projects, err := st.ListProjects(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list projects: %v", err)), nil
		}
		return jsonToolResult(map[string]any{"projects": projects})
	})
}

func registerIndexProjectTool(srv *server.MCPServer, st *store.Store, cfg config.Config) {
	tool := mcp.NewTool(
		"index_project",
		mcp.WithDescription("Index or update a project path"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative project path")),
		mcp.WithBoolean("force", mcp.Description("Force full reindex")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		force := false
		if rawForce, ok := req.GetArguments()["force"]; ok {
			if value, ok := rawForce.(bool); ok {
				force = value
			}
		}

		ix := indexer.New(st, cfg)
		result, err := ix.IndexProject(ctx, path, indexer.Options{Force: force})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("index project: %v", err)), nil
		}
		return jsonToolResult(result)
	})
}

func registerGetDependenciesTool(srv *server.MCPServer, st *store.Store) {
	tool := mcp.NewTool(
		"get_dependencies",
		mcp.WithDescription("Return project dependencies with source attachment metadata"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithBoolean("includeTransitive", mcp.Description("Include transitive dependencies in addition to direct ones")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		project, err := st.FindProject(ctx, projectArg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		includeTransitive := req.GetBool("includeTransitive", false)

		dependencies, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, includeTransitive)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("load dependencies: %v", err)), nil
		}

		stats := map[string]int{}
		for _, dep := range dependencies {
			stats[dep.SourceStatus]++
		}

		payload := map[string]any{
			"project":           project,
			"total":             len(dependencies),
			"includeTransitive": includeTransitive,
			"sourceStats":       stats,
			"dependencies":      dependencies,
		}
		return jsonToolResult(payload)
	})
}

func jsonToolResult(value any) (*mcp.CallToolResult, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(strings.TrimSpace(string(raw))), nil
}
