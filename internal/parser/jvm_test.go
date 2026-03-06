package parser

import "testing"

func TestParseFile_KotlinExtractsSymbolsAndImports(t *testing.T) {
	content := []byte(`
package com.example.demo

import org.springframework.web.reactive.function.client.WebClient

class BotService {
    fun sendMessage(text: String) { println(text) }
}
`)

	symbols, refs := ParseFile("src/main/kotlin/com/example/demo/BotService.kt", content)
	if len(symbols) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d", len(symbols))
	}
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 references (import + call), got %d", len(refs))
	}
	foundImport := false
	for _, ref := range refs {
		if ref.RefType == "IMPORTS" && ref.ToFQName == "org.springframework.web.reactive.function.client.WebClient" {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Fatalf("expected import reference to WebClient, refs=%#v", refs)
	}
	foundCall := false
	for _, ref := range refs {
		if ref.RefType == "CALLS" && ref.ToName == "println" {
			foundCall = true
			break
		}
	}
	if !foundCall {
		t.Fatalf("expected CALLS reference to println, refs=%#v", refs)
	}
}

func TestParseFile_JavaExtractsSymbolsAndImports(t *testing.T) {
	content := []byte(`
package com.example.demo;

import java.util.List;

public class Greeter {
  public String greet(String name) { return name; }
}
`)

	symbols, refs := ParseFile("src/main/java/com/example/demo/Greeter.java", content)
	if len(symbols) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d", len(symbols))
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 import reference, got %d", len(refs))
	}
	foundImport := false
	for _, ref := range refs {
		if ref.RefType == "IMPORTS" && ref.ToFQName == "java.util.List" {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Fatalf("expected import reference to java.util.List, refs=%#v", refs)
	}
}
