package parser

import (
	"path/filepath"
	"regexp"
	"strings"
)

type Symbol struct {
	Name      string
	FQName    string
	Kind      string
	Language  string
	FilePath  string
	StartLine int
	EndLine   int
	Signature string
}

type Reference struct {
	FromName   string
	FromFile   string
	ToName     string
	ToFQName   string
	RefType    string
	Confidence float64
	Evidence   string
}

var (
	javaPackageRE = regexp.MustCompile(`^\s*package\s+([a-zA-Z0-9_.]+)\s*;`)
	ktPackageRE   = regexp.MustCompile(`^\s*package\s+([a-zA-Z0-9_.]+)\s*$`)

	javaImportRE = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([a-zA-Z0-9_.*]+)\s*;`)
	ktImportRE   = regexp.MustCompile(`^\s*import\s+([a-zA-Z0-9_.*]+)\s*$`)

	javaTypeRE   = regexp.MustCompile(`\b(class|interface|enum|record)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	javaMethodRE = regexp.MustCompile(`\b(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?[A-Za-z0-9_<>,\[\].?]+\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

	ktTypeRE = regexp.MustCompile(`\b(class|interface|object|enum\s+class|data\s+class|sealed\s+class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	ktFunRE  = regexp.MustCompile(`\bfun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

	callExprRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func ParseFile(path string, content []byte) ([]Symbol, []Reference) {
	language := languageByExt(path)
	if language == "" {
		return nil, nil
	}
	if language == "java" {
		if symbols, refs, ok := parseJavaTreeSitter(path, content); ok {
			return dedupeSymbols(symbols), dedupeReferences(refs)
		}
	}
	return parseHeuristic(path, content, language)
}

func parseHeuristic(path string, content []byte, language string) ([]Symbol, []Reference) {

	lines := strings.Split(string(content), "\n")
	packageName := parsePackage(lines, language)
	baseName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ownerPrefix := baseName
	if packageName != "" {
		ownerPrefix = packageName + "." + baseName
	}

	symbols := make([]Symbol, 0, 16)
	refs := make([]Reference, 0, 32)
	callRefs := make([]Reference, 0, 64)
	lastCallableByLine := map[int]string{}

	for i, line := range lines {
		lineNo := i + 1

		if imported, ok := parseImport(line, language); ok {
			refs = append(refs, Reference{
				FromName:   baseName,
				FromFile:   path,
				ToName:     shortName(imported),
				ToFQName:   imported,
				RefType:    "IMPORTS",
				Confidence: 0.95,
				Evidence:   strings.TrimSpace(line),
			})
		}

		symbols = append(symbols, parseTypeSymbol(line, language, packageName, path, lineNo)...)
		fnSymbols := parseFunctionSymbol(line, language, packageName, ownerPrefix, path, lineNo)
		symbols = append(symbols, fnSymbols...)
		if len(fnSymbols) > 0 {
			lastCallableByLine[lineNo] = fnSymbols[0].Name
		}
	}

	currentOwner := baseName
	for i, line := range lines {
		lineNo := i + 1
		if owner, ok := lastCallableByLine[lineNo]; ok {
			currentOwner = owner
		}
		callRefs = append(callRefs, parseCallReferences(path, line, currentOwner)...)
	}
	refs = append(refs, callRefs...)

	return dedupeSymbols(symbols), dedupeReferences(refs)
}

func parsePackage(lines []string, language string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if language == "java" {
			if match := javaPackageRE.FindStringSubmatch(line); len(match) > 1 {
				return match[1]
			}
		}
		if language == "kotlin" {
			if match := ktPackageRE.FindStringSubmatch(line); len(match) > 1 {
				return match[1]
			}
		}
	}
	return ""
}

func parseImport(line string, language string) (string, bool) {
	if language == "java" {
		if match := javaImportRE.FindStringSubmatch(line); len(match) > 1 {
			return strings.TrimSpace(match[1]), true
		}
	}
	if language == "kotlin" {
		if match := ktImportRE.FindStringSubmatch(line); len(match) > 1 {
			return strings.TrimSpace(match[1]), true
		}
	}
	return "", false
}

func parseTypeSymbol(line, language, pkg, filePath string, lineNo int) []Symbol {
	if language == "java" {
		if match := javaTypeRE.FindStringSubmatch(line); len(match) > 2 {
			name := match[2]
			return []Symbol{makeSymbol(name, pkg, normalizeTypeKind(match[1]), language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	if language == "kotlin" {
		if match := ktTypeRE.FindStringSubmatch(line); len(match) > 2 {
			name := match[2]
			return []Symbol{makeSymbol(name, pkg, "Type", language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	return nil
}

func parseFunctionSymbol(line, language, pkg, ownerPrefix, filePath string, lineNo int) []Symbol {
	if language == "java" {
		if match := javaMethodRE.FindStringSubmatch(line); len(match) > 1 {
			name := match[1]
			if name == "if" || name == "for" || name == "while" || name == "switch" || name == "catch" {
				return nil
			}
			return []Symbol{makeCallableSymbol(name, pkg, ownerPrefix, "Method", language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	if language == "kotlin" {
		if match := ktFunRE.FindStringSubmatch(line); len(match) > 1 {
			name := match[1]
			return []Symbol{makeCallableSymbol(name, pkg, ownerPrefix, "Function", language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	return nil
}

func makeCallableSymbol(name, pkg, ownerPrefix, kind, language, filePath string, lineNo int, signature string) Symbol {
	fq := name
	if ownerPrefix != "" {
		fq = ownerPrefix + "." + name
	} else if pkg != "" {
		fq = pkg + "." + name
	}
	return Symbol{
		Name:      name,
		FQName:    fq,
		Kind:      kind,
		Language:  language,
		FilePath:  filePath,
		StartLine: lineNo,
		EndLine:   lineNo,
		Signature: signature,
	}
}

func parseCallReferences(filePath, line, owner string) []Reference {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "import ") {
		return nil
	}

	matches := callExprRE.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}

	refs := make([]Reference, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		target := strings.TrimSpace(match[1])
		if isCallKeyword(target) {
			continue
		}
		if looksLikeDeclaration(line, target) {
			continue
		}
		refs = append(refs, Reference{
			FromName:   owner,
			FromFile:   filePath,
			ToName:     target,
			RefType:    "CALLS",
			Confidence: 0.7,
			Evidence:   trimmed,
		})
	}
	return refs
}

func makeSymbol(name, pkg, kind, language, filePath string, lineNo int, signature string) Symbol {
	fq := name
	if pkg != "" {
		fq = pkg + "." + name
	}
	return Symbol{
		Name:      name,
		FQName:    fq,
		Kind:      kind,
		Language:  language,
		FilePath:  filePath,
		StartLine: lineNo,
		EndLine:   lineNo,
		Signature: signature,
	}
}

func normalizeTypeKind(kind string) string {
	if kind == "interface" {
		return "Interface"
	}
	return "Type"
}

func languageByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	default:
		return ""
	}
}

func shortName(imported string) string {
	parts := strings.Split(imported, ".")
	if len(parts) == 0 {
		return imported
	}
	return parts[len(parts)-1]
}

func dedupeSymbols(symbols []Symbol) []Symbol {
	seen := map[string]struct{}{}
	out := make([]Symbol, 0, len(symbols))
	for _, s := range symbols {
		key := strings.Join([]string{s.FilePath, s.Name, s.Kind, s.Signature}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func dedupeReferences(refs []Reference) []Reference {
	seen := map[string]struct{}{}
	out := make([]Reference, 0, len(refs))
	for _, ref := range refs {
		key := strings.Join([]string{ref.FromFile, ref.FromName, ref.ToName, ref.ToFQName, ref.RefType, ref.Evidence}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func isCallKeyword(name string) bool {
	switch name {
	case "if", "for", "while", "switch", "catch", "when", "try", "return", "throw", "new", "super", "this", "class", "fun":
		return true
	default:
		return false
	}
}

func looksLikeDeclaration(line, candidate string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "class "+candidate) || strings.Contains(trimmed, "interface "+candidate) || strings.Contains(trimmed, "record "+candidate) || strings.Contains(trimmed, "enum "+candidate) {
		return true
	}
	if strings.Contains(trimmed, "fun "+candidate+"(") {
		return true
	}
	if strings.Contains(trimmed, " "+candidate+"(") && (strings.Contains(trimmed, "public ") || strings.Contains(trimmed, "private ") || strings.Contains(trimmed, "protected ") || strings.Contains(trimmed, "static ")) {
		return true
	}
	return false
}
