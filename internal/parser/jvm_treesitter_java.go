package parser

import (
	"path/filepath"
	"regexp"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

var importQualifiedRE = regexp.MustCompile(`import\s+(?:static\s+)?([a-zA-Z0-9_.*]+)\s*;`)

type javaWalkContext struct {
	source          []byte
	filePath        string
	baseName        string
	packageName     string
	currentCallable string
	classStack      []string
	symbols         []Symbol
	refs            []Reference
}

func parseJavaTreeSitter(path string, content []byte) ([]Symbol, []Reference, bool) {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	language := tree_sitter.NewLanguage(tree_sitter_java.Language())
	if err := parser.SetLanguage(language); err != nil {
		return nil, nil, false
	}

	tree := parser.Parse(content, nil)
	if tree == nil {
		return nil, nil, false
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, nil, false
	}

	ctx := &javaWalkContext{
		source:   content,
		filePath: path,
		baseName: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		symbols:  make([]Symbol, 0, 24),
		refs:     make([]Reference, 0, 64),
	}
	walkJavaNode(root, ctx)

	if len(ctx.symbols) == 0 && len(ctx.refs) == 0 {
		return nil, nil, false
	}
	return ctx.symbols, ctx.refs, true
}

func walkJavaNode(node *tree_sitter.Node, ctx *javaWalkContext) {
	if node == nil {
		return
	}

	kind := node.Kind()
	switch kind {
	case "package_declaration":
		if pkg := extractPackageFromJavaNode(node, ctx.source); pkg != "" {
			ctx.packageName = pkg
		}
	case "import_declaration":
		qualified := extractImportFromJavaNode(node, ctx.source)
		if qualified != "" {
			ctx.refs = append(ctx.refs, Reference{
				FromName:   ownerName(ctx),
				FromFile:   ctx.filePath,
				ToName:     shortName(qualified),
				ToFQName:   qualified,
				RefType:    "IMPORTS",
				Confidence: 0.98,
				Evidence:   strings.TrimSpace(node.Utf8Text(ctx.source)),
			})
		}
	case "class_declaration", "interface_declaration", "enum_declaration", "record_declaration":
		handleJavaTypeNode(node, kind, ctx)
		return
	case "method_declaration", "constructor_declaration":
		handleJavaCallableNode(node, kind, ctx)
		return
	case "method_invocation":
		handleJavaMethodInvocation(node, ctx)
	}

	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		walkJavaNode(child, ctx)
	}
}

func handleJavaTypeNode(node *tree_sitter.Node, kind string, ctx *javaWalkContext) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child != nil && child.Kind() == "identifier" {
				nameNode = child
				break
			}
		}
	}
	if nameNode == nil {
		return
	}

	name := strings.TrimSpace(nameNode.Utf8Text(ctx.source))
	if name == "" {
		return
	}

	ctx.classStack = append(ctx.classStack, name)
	fq := qualifyName(ctx.packageName, append([]string{}, ctx.classStack...))
	ctx.symbols = append(ctx.symbols, Symbol{
		Name:      name,
		FQName:    fq,
		Kind:      normalizeTypeKind(strings.TrimSuffix(kind, "_declaration")),
		Language:  "java",
		FilePath:  ctx.filePath,
		StartLine: int(nameNode.StartPosition().Row) + 1,
		EndLine:   int(nameNode.EndPosition().Row) + 1,
		Signature: strings.TrimSpace(node.Utf8Text(ctx.source)),
	})

	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		walkJavaNode(child, ctx)
	}

	ctx.classStack = ctx.classStack[:len(ctx.classStack)-1]
}

func handleJavaCallableNode(node *tree_sitter.Node, kind string, ctx *javaWalkContext) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil && kind == "constructor_declaration" {
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child != nil && child.Kind() == "identifier" {
				nameNode = child
				break
			}
		}
	}
	if nameNode == nil {
		return
	}

	name := strings.TrimSpace(nameNode.Utf8Text(ctx.source))
	if name == "" {
		return
	}

	pathParts := append([]string{}, ctx.classStack...)
	pathParts = append(pathParts, name)
	fq := qualifyName(ctx.packageName, pathParts)
	kindName := "Method"
	if kind == "constructor_declaration" {
		kindName = "Constructor"
	}
	ctx.symbols = append(ctx.symbols, Symbol{
		Name:      name,
		FQName:    fq,
		Kind:      kindName,
		Language:  "java",
		FilePath:  ctx.filePath,
		StartLine: int(nameNode.StartPosition().Row) + 1,
		EndLine:   int(nameNode.EndPosition().Row) + 1,
		Signature: strings.TrimSpace(node.Utf8Text(ctx.source)),
	})

	prevOwner := ctx.currentCallable
	ctx.currentCallable = name
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		walkJavaNode(child, ctx)
	}
	ctx.currentCallable = prevOwner
}

func handleJavaMethodInvocation(node *tree_sitter.Node, ctx *javaWalkContext) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child != nil && child.Kind() == "identifier" {
				nameNode = child
				break
			}
		}
	}
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Utf8Text(ctx.source))
	if name == "" || isCallKeyword(name) {
		return
	}
	ctx.refs = append(ctx.refs, Reference{
		FromName:   ownerName(ctx),
		FromFile:   ctx.filePath,
		ToName:     name,
		RefType:    "CALLS",
		Confidence: 0.85,
		Evidence:   strings.TrimSpace(node.Utf8Text(ctx.source)),
	})
}

func ownerName(ctx *javaWalkContext) string {
	if ctx.currentCallable != "" {
		return ctx.currentCallable
	}
	if len(ctx.classStack) > 0 {
		return ctx.classStack[len(ctx.classStack)-1]
	}
	return ctx.baseName
}

func extractPackageFromJavaNode(node *tree_sitter.Node, source []byte) string {
	text := strings.TrimSpace(node.Utf8Text(source))
	text = strings.TrimPrefix(text, "package")
	text = strings.TrimSpace(text)
	text = strings.TrimSuffix(text, ";")
	return strings.TrimSpace(text)
}

func extractImportFromJavaNode(node *tree_sitter.Node, source []byte) string {
	text := strings.TrimSpace(node.Utf8Text(source))
	match := importQualifiedRE.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func qualifyName(pkg string, parts []string) string {
	parts = filterNonEmpty(parts)
	if len(parts) == 0 {
		return pkg
	}
	if pkg == "" {
		return strings.Join(parts, ".")
	}
	return pkg + "." + strings.Join(parts, ".")
}

func filterNonEmpty(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
