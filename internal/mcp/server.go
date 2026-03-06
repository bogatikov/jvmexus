package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bgtkv/jvmexus/internal/config"
	"github.com/bgtkv/jvmexus/internal/indexer"
	"github.com/bgtkv/jvmexus/internal/rag"
	"github.com/bgtkv/jvmexus/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewServer(cfg config.Config, st *store.Store) *server.MCPServer {
	searcher := rag.NewSearcher(cfg, st)
	srv := server.NewMCPServer(
		"JVMexus",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)

	registerListProjectsTool(srv, st)
	registerIndexProjectTool(srv, st, cfg)
	registerGetDependenciesTool(srv, st)
	registerGetBuildGraphTool(srv, st)
	registerQueryCodeTool(srv, st, searcher)
	registerGetSymbolContextTool(srv, st)
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
		mcp.WithTemplateDescription("Summary including direct dependency source-attachment stats"),
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
			"total":       len(dependencies),
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
		mcp.WithTemplateDescription("Direct dependencies with source attachment metadata"),
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

	buildGraphTemplate := mcp.NewResourceTemplate(
		"jvminfo://project/{name}/build-graph",
		"Project Build Graph",
		mcp.WithTemplateDescription("Module/dependency build graph for the project"),
		mcp.WithTemplateMIMEType("application/json"),
	)
	srv.AddResourceTemplate(buildGraphTemplate, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		name := templateValue(request.Params.URI, buildGraphURI)
		if name == "" {
			return nil, fmt.Errorf("invalid build-graph uri: %s", request.Params.URI)
		}
		project, err := st.FindProject(ctx, name)
		if err != nil {
			return nil, err
		}
		graph, err := buildGraphPayload(ctx, st, project, false)
		if err != nil {
			return nil, err
		}
		payload, err := toJSON(graph)
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
	summaryURI    = regexp.MustCompile(`^jvminfo://project/([^/]+)/summary$`)
	depsURI       = regexp.MustCompile(`^jvminfo://project/([^/]+)/dependencies$`)
	buildGraphURI = regexp.MustCompile(`^jvminfo://project/([^/]+)/build-graph$`)
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

func registerGetBuildGraphTool(srv *server.MCPServer, st *store.Store) {
	tool := mcp.NewTool(
		"get_build_graph",
		mcp.WithDescription("Return module/dependency build graph for a project"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithBoolean("includeTransitive", mcp.Description("Include transitive dependencies in graph")),
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
		payload, err := buildGraphPayload(ctx, st, project, includeTransitive)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("build graph: %v", err)), nil
		}
		return jsonToolResult(payload)
	})
}

func buildGraphPayload(ctx context.Context, st *store.Store, project store.Project, includeTransitive bool) (map[string]any, error) {
	modules, err := st.ListModulesByProjectID(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	dependencies, err := st.ListDependenciesByProjectIDWithMode(ctx, project.ID, includeTransitive)
	if err != nil {
		return nil, fmt.Errorf("list dependencies: %w", err)
	}

	type node struct {
		ID       string         `json:"id"`
		NodeType string         `json:"nodeType"`
		Label    string         `json:"label"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}
	type edge struct {
		From     string         `json:"from"`
		To       string         `json:"to"`
		EdgeType string         `json:"edgeType"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}

	nodes := make([]node, 0, len(modules)+len(dependencies)+1)
	edges := make([]edge, 0, len(dependencies))
	nodeSeen := map[string]struct{}{}

	projectNodeID := "project:" + project.Name
	nodes = append(nodes, node{ID: projectNodeID, NodeType: "project", Label: project.Name})
	nodeSeen[projectNodeID] = struct{}{}

	for _, module := range modules {
		moduleID := "module:" + module.Name
		if _, ok := nodeSeen[moduleID]; !ok {
			nodeSeen[moduleID] = struct{}{}
			nodes = append(nodes, node{ID: moduleID, NodeType: "module", Label: module.Name, Metadata: map[string]any{"path": module.Path}})
		}
		edges = append(edges, edge{From: projectNodeID, To: moduleID, EdgeType: "CONTAINS"})
	}

	for _, dep := range dependencies {
		depLabel := dep.GroupID + ":" + dep.ArtifactID
		if dep.Version != "" {
			depLabel += ":" + dep.Version
		}
		depID := "dependency:" + depLabel + "|" + dep.Kind + "|" + dep.Scope
		if _, ok := nodeSeen[depID]; !ok {
			nodeSeen[depID] = struct{}{}
			nodes = append(nodes, node{
				ID:       depID,
				NodeType: "dependency",
				Label:    depLabel,
				Metadata: map[string]any{"kind": dep.Kind, "scope": dep.Scope, "type": dep.Type, "resolutionType": dep.ResolutionType, "sourceStatus": dep.SourceStatus},
			})
		}
		moduleID := "module:" + dep.ModuleName
		edges = append(edges, edge{From: moduleID, To: depID, EdgeType: "DEPENDS_ON", Metadata: map[string]any{"scope": dep.Scope, "kind": dep.Kind}})

		if dep.SourceJarPath != "" {
			sourceNodeID := "dependency-source:" + depLabel
			if _, ok := nodeSeen[sourceNodeID]; !ok {
				nodeSeen[sourceNodeID] = struct{}{}
				nodes = append(nodes, node{
					ID:       sourceNodeID,
					NodeType: "dependency_source",
					Label:    depLabel + "-sources",
					Metadata: map[string]any{"sourceJarPath": dep.SourceJarPath, "sourceStatus": dep.SourceStatus},
				})
			}
			edges = append(edges, edge{From: depID, To: sourceNodeID, EdgeType: "HAS_SOURCE"})
		}
	}

	return map[string]any{
		"project":           project,
		"includeTransitive": includeTransitive,
		"nodeCount":         len(nodes),
		"edgeCount":         len(edges),
		"nodes":             nodes,
		"edges":             edges,
	}, nil
}

func registerQueryCodeTool(srv *server.MCPServer, st *store.Store, searcher *rag.Searcher) {
	tool := mcp.NewTool(
		"query_code",
		mcp.WithDescription("Search indexed code/build chunks using hybrid retrieval (FTS5 + local vector rerank)"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural-language query")),
		mcp.WithString("scope", mcp.Description("Search scope: all, project, or libraries (default all)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of chunks to return (integer, default 10)")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		project, err := st.FindProject(ctx, projectArg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		scope := req.GetString("scope", "all")
		limit := req.GetInt("limit", 10)
		results, err := searcher.SearchWithScope(ctx, project.ID, query, limit, scope)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search chunks: %v", err)), nil
		}
		payload := map[string]any{
			"project":       project,
			"query":         query,
			"scope":         scope,
			"limit":         limit,
			"total":         len(results),
			"results":       results,
			"retrievalMode": "hybrid:fts5+local-vector-rerank",
			"model":         searcher.ModelID(),
			"note":          "local embeddings run via fastembed when available, with hashed fallback for offline robustness",
		}
		return jsonToolResult(payload)
	})
}

func registerGetSymbolContextTool(srv *server.MCPServer, st *store.Store) {
	tool := mcp.NewTool(
		"get_symbol_context",
		mcp.WithDescription("Return symbol context with incoming references (best effort)"),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project name or root path")),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("Symbol or fully-qualified name")),
		mcp.WithString("filePath", mcp.Description("Optional file path filter for disambiguation")),
		mcp.WithNumber("limit", mcp.Description("Maximum symbols to return (integer, default 20)")),
	)
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectArg, err := req.RequireString("project")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		symbol, err := req.RequireString("symbol")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		project, err := st.FindProject(ctx, projectArg)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		filePath := req.GetString("filePath", "")
		limit := req.GetInt("limit", 20)
		symbols, err := st.FindSymbolsWithFilter(ctx, project.ID, symbol, filePath, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("find symbols: %v", err)), nil
		}

		type symbolContext struct {
			Symbol                store.Symbol            `json:"symbol"`
			Incoming              []store.SymbolReference `json:"incoming"`
			Outgoing              []store.SymbolReference `json:"outgoing"`
			Callers               []store.Symbol          `json:"callers"`
			Callees               []store.Symbol          `json:"callees"`
			ImportedSymbols       []store.Symbol          `json:"importedSymbols"`
			UnresolvedCallTargets []string                `json:"unresolvedCallTargets"`
		}
		contexts := make([]symbolContext, 0, len(symbols))
		for _, sym := range symbols {
			incoming, incomingErr := st.ListIncomingReferences(ctx, project.ID, sym, 100)
			if incomingErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("load incoming references for %s: %v", sym.Name, incomingErr)), nil
			}
			outgoing, outgoingErr := st.ListOutgoingReferences(ctx, project.ID, sym, 100)
			if outgoingErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("load outgoing references for %s: %v", sym.Name, outgoingErr)), nil
			}

			callers, resolveIncomingErr := resolveIncomingCallers(ctx, st, project.ID, incoming)
			if resolveIncomingErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("resolve callers for %s: %v", sym.Name, resolveIncomingErr)), nil
			}

			callees, importedSymbols, unresolvedCalls, resolveOutgoingErr := resolveOutgoingTargets(ctx, st, project.ID, outgoing)
			if resolveOutgoingErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("resolve outgoing targets for %s: %v", sym.Name, resolveOutgoingErr)), nil
			}

			contexts = append(contexts, symbolContext{
				Symbol:                sym,
				Incoming:              incoming,
				Outgoing:              outgoing,
				Callers:               callers,
				Callees:               callees,
				ImportedSymbols:       importedSymbols,
				UnresolvedCallTargets: unresolvedCalls,
			})
		}

		disambiguationNeeded := len(contexts) > 1

		payload := map[string]any{
			"project":              project,
			"query":                symbol,
			"count":                len(contexts),
			"filePathFilter":       filePath,
			"disambiguationNeeded": disambiguationNeeded,
			"contexts":             contexts,
			"note":                 "references are heuristic and currently include imports plus lightweight call detection",
		}
		return jsonToolResult(payload)
	})
}

func resolveIncomingCallers(ctx context.Context, st *store.Store, projectID int64, incoming []store.SymbolReference) ([]store.Symbol, error) {
	callers := make([]store.Symbol, 0, 16)
	seen := map[string]struct{}{}
	for _, ref := range incoming {
		if ref.RefType != "CALLS" {
			continue
		}
		candidates, err := st.FindSymbolsByExactName(ctx, projectID, ref.FromName, 5)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			key := candidate.FQName + "|" + candidate.FilePath
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			callers = append(callers, candidate)
		}
	}
	return callers, nil
}

func resolveOutgoingTargets(ctx context.Context, st *store.Store, projectID int64, outgoing []store.SymbolReference) ([]store.Symbol, []store.Symbol, []string, error) {
	callees := make([]store.Symbol, 0, 16)
	imports := make([]store.Symbol, 0, 16)
	unresolved := make([]string, 0, 16)
	seenCallee := map[string]struct{}{}
	seenImport := map[string]struct{}{}
	seenUnresolved := map[string]struct{}{}

	for _, ref := range outgoing {
		if ref.RefType != "CALLS" && ref.RefType != "IMPORTS" {
			continue
		}

		query := ref.ToName
		if ref.ToFQName != "" {
			query = ref.ToFQName
		}
		candidates, err := st.FindSymbolsByExactName(ctx, projectID, query, 10)
		if err != nil {
			return nil, nil, nil, err
		}

		if len(candidates) == 0 {
			if ref.RefType == "CALLS" {
				if _, ok := seenUnresolved[ref.ToName]; !ok {
					seenUnresolved[ref.ToName] = struct{}{}
					unresolved = append(unresolved, ref.ToName)
				}
			}
			continue
		}

		for _, candidate := range candidates {
			key := candidate.FQName + "|" + candidate.FilePath
			if ref.RefType == "CALLS" {
				if _, ok := seenCallee[key]; ok {
					continue
				}
				seenCallee[key] = struct{}{}
				callees = append(callees, candidate)
				continue
			}
			if _, ok := seenImport[key]; ok {
				continue
			}
			seenImport[key] = struct{}{}
			imports = append(imports, candidate)
		}
	}

	return callees, imports, unresolved, nil
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

		force := req.GetBool("force", false)

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
