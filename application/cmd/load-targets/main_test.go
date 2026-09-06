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
		filepath.Join(directory, "private.jwk"), "http://127.0.0.1:18080", "create", "2", "load-offer", "2",
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
		strings.Contains(string(decoded), `"id"`) || !strings.Contains(string(decoded), `"bond_series":"DEMO2026"`) {
		t.Fatalf("first target = %#v, body = %s", first, decoded)
	}
	if len(first.Header["Authorization"]) != 1 || !strings.HasPrefix(first.Header["Authorization"][0], "Bearer ") ||
		first.Header["Idempotency-Key"][0] == second.Header["Idempotency-Key"][0] {
		t.Fatalf("targets are not independently authenticated: first=%#v second=%#v", first, second)
	}
	if first.Header["Authorization"][0] == second.Header["Authorization"][0] {
		t.Fatal("targets for distinct principals reused an assertion")
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
		input := "scenario"
		if scenario == "buy" || scenario == "contended-buy" {
			input = "019535d9-3df7-79fb-b466-fa907fa17f9e"
		}
		if err := run([]string{key, "http://localhost:8080", scenario, "1", input, "1"}, &output, now); err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		if !strings.Contains(output.String(), `"Authorization"`) {
			t.Fatalf("%s target lacks authorization: %s", scenario, output.String())
		}
	}
}

func TestGenerateRejectedTrafficTargets(t *testing.T) {
	directory := t.TempDir()
	if err := demoauth.Initialize(directory); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(directory, "private.jwk")
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)

	var denied bytes.Buffer
	if err := run([]string{key, "http://localhost:8080", "denied", "1", "denied", "1"}, &denied, now); err != nil {
		t.Fatal(err)
	}
	var deniedTarget target
	if err := json.Unmarshal([]byte(strings.TrimSpace(denied.String())), &deniedTarget); err != nil {
		t.Fatal(err)
	}
	if deniedTarget.Method != "GET" || deniedTarget.URL != "http://localhost:8080/active-bond-series" {
		t.Fatalf("denied target = %#v", deniedTarget)
	}
	if len(deniedTarget.Header["Authorization"]) != 1 || deniedTarget.Body != "" ||
		len(deniedTarget.Header["Idempotency-Key"]) != 0 {
		t.Fatalf("denied target is not an authenticated read: %#v", deniedTarget)
	}

	// The invalid-assertion target must request the same URL a correct listing
	// requests, so the server rejects it for its binding rather than for its
	// route. Its assertion is issued to an unseeded subject and bound to
	// DEMO2027, and is refused before principal resolution or admission.
	var invalid, valid bytes.Buffer
	if err := run([]string{key, "http://localhost:8080", "invalid-assertion", "1", "invalid", "1"}, &invalid, now); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{key, "http://localhost:8080", "list-offers", "1", "list-offers", "1"}, &valid, now); err != nil {
		t.Fatal(err)
	}
	var invalidTarget, validTarget target
	if err := json.Unmarshal([]byte(strings.TrimSpace(invalid.String())), &invalidTarget); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(valid.String())), &validTarget); err != nil {
		t.Fatal(err)
	}
	if invalidTarget.URL != validTarget.URL {
		t.Fatalf("invalid-assertion target requests %q, want %q", invalidTarget.URL, validTarget.URL)
	}
	if invalidTarget.Header["Authorization"][0] == validTarget.Header["Authorization"][0] {
		t.Fatal("invalid-assertion target reused the correctly bound assertion")
	}
}

func TestRejectInvalidArguments(t *testing.T) {
	for _, arguments := range [][]string{
		{},
		{"key", "https://localhost", "create", "1", "prefix"},
		{"key", "http://localhost/path", "create", "1", "prefix"},
		{"key", "http://localhost", "create", "0", "prefix"},
		{"key", "http://localhost", "unknown", "1", "prefix", "1"},
		{"key", "http://localhost", "create", "1", strings.Repeat("x", 4097), "1"},
		{"key", "http://localhost", "create", "1", "prefix", "0"},
		{"key", "http://localhost", "create", "1", "prefix", "1000001"},
	} {
		if err := run(arguments, &bytes.Buffer{}, time.Now()); err == nil {
			t.Fatalf("run(%q) unexpectedly succeeded", arguments)
		}
	}
}
