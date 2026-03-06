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
)

func ParseFile(path string, content []byte) ([]Symbol, []Reference) {
	language := languageByExt(path)
	if language == "" {
		return nil, nil
	}

	lines := strings.Split(string(content), "\n")
	packageName := parsePackage(lines, language)
	baseName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	symbols := make([]Symbol, 0, 16)
	refs := make([]Reference, 0, 32)

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
		symbols = append(symbols, parseFunctionSymbol(line, language, packageName, path, lineNo)...)
	}

	return dedupeSymbols(symbols), refs
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

func parseFunctionSymbol(line, language, pkg, filePath string, lineNo int) []Symbol {
	if language == "java" {
		if match := javaMethodRE.FindStringSubmatch(line); len(match) > 1 {
			name := match[1]
			if name == "if" || name == "for" || name == "while" || name == "switch" || name == "catch" {
				return nil
			}
			return []Symbol{makeSymbol(name, pkg, "Method", language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	if language == "kotlin" {
		if match := ktFunRE.FindStringSubmatch(line); len(match) > 1 {
			name := match[1]
			return []Symbol{makeSymbol(name, pkg, "Function", language, filePath, lineNo, strings.TrimSpace(line))}
		}
	}
	return nil
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
