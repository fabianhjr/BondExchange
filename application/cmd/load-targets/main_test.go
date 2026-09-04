package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fabianhjr/BondExchange/application/cmd/internal/demoauth"
)

func TestGenerateCreateTargets(t *testing.T) {
	directory := t.TempDir()
	if err := demoauth.Initialize(directory); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{
		filepath.Join(directory, "private.jwk"), "http://127.0.0.1:18080", "create", "2", "load-offer",
	}, &output, time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("target lines = %d, want 2", len(lines))
	}
	var first, second target
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	if first.Method != "POST" || first.URL != "http://127.0.0.1:18080/sale-offers" ||
		!strings.Contains(string(decoded), `"id":"load-offer-000001"`) {
		t.Fatalf("first target = %#v, body = %s", first, decoded)
	}
	if len(first.Header["Authorization"]) != 1 || !strings.HasPrefix(first.Header["Authorization"][0], "Bearer ") ||
		first.Header["Idempotency-Key"][0] == second.Header["Idempotency-Key"][0] || first.Body == second.Body {
		t.Fatalf("targets are not independently authenticated: first=%#v second=%#v", first, second)
	}
}

func TestGenerateReadAndContendedTargets(t *testing.T) {
	directory := t.TempDir()
	if err := demoauth.Initialize(directory); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(directory, "private.jwk")
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"list-offers", "list-series", "contended-buy", "buy"} {
		var output bytes.Buffer
		if err := run([]string{key, "http://localhost:8080", scenario, "1", "scenario"}, &output, now); err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		if !strings.Contains(output.String(), `"Authorization"`) {
			t.Fatalf("%s target lacks authorization: %s", scenario, output.String())
		}
	}
}

func TestRejectInvalidArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{},
		{"key", "https://localhost", "create", "1", "prefix"},
		{"key", "http://localhost/path", "create", "1", "prefix"},
		{"key", "http://localhost", "create", "0", "prefix"},
		{"key", "http://localhost", "unknown", "1", "prefix"},
		{"key", "http://localhost", "create", "1", strings.Repeat("x", 97)},
	} {
		if err := run(arguments, &bytes.Buffer{}, time.Now()); err == nil {
			t.Fatalf("run(%q) unexpectedly succeeded", arguments)
		}
	}
}
