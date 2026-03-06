package parser

import "testing"

func TestParseFile_KotlinExtractsSymbolsAndImports(t *testing.T) {
	content := []byte(`
package com.example.demo

import org.springframework.web.reactive.function.client.WebClient

class BotService {
    fun sendMessage(text: String) {}
}
`)

	symbols, refs := ParseFile("src/main/kotlin/com/example/demo/BotService.kt", content)
	if len(symbols) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d", len(symbols))
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 import reference, got %d", len(refs))
	}
	if refs[0].ToFQName != "org.springframework.web.reactive.function.client.WebClient" {
		t.Fatalf("unexpected import fq name: %s", refs[0].ToFQName)
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
	if refs[0].ToFQName != "java.util.List" {
		t.Fatalf("unexpected import fq name: %s", refs[0].ToFQName)
	}
}
