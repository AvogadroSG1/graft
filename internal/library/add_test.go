package library

import (
	"reflect"
	"testing"

	"github.com/poconnor/graft/internal/model"
)

func TestParseMCPJSONWrappedMultiServer(t *testing.T) {
	t.Parallel()
	data := []byte(`{"mcpServers":{"docs":{"command":"npx","args":["docs"]},"db":{"type":"http","url":"https://db.example/mcp"}}}`)
	defs, err := ParseMCPJSON(data, "")
	if err != nil {
		t.Fatalf("ParseMCPJSON() error = %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("ParseMCPJSON() len = %d, want 2", len(defs))
	}
	if defs[0].Name != "db" || defs[1].Name != "docs" {
		t.Fatalf("ParseMCPJSON() names = %q,%q, want db,docs (sorted)", defs[0].Name, defs[1].Name)
	}
	if defs[1].Version != "0.1.0" {
		t.Fatalf("ParseMCPJSON() version = %q, want 0.1.0", defs[1].Version)
	}
	if len(defs[1].Adapters) != 0 {
		t.Fatalf("ParseMCPJSON() set per-tool adapters = %+v, want none", defs[1].Adapters)
	}
}

func TestParseMCPJSONBareWithNameField(t *testing.T) {
	t.Parallel()
	data := []byte(`{"name":"foo","command":"foo-bin","args":["--stdio"]}`)
	defs, err := ParseMCPJSON(data, "ignored")
	if err != nil {
		t.Fatalf("ParseMCPJSON() error = %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "foo" || defs[0].Command != "foo-bin" {
		t.Fatalf("ParseMCPJSON() = %+v, want foo definition", defs)
	}
}

func TestParseMCPJSONBareUsesNameOverride(t *testing.T) {
	t.Parallel()
	data := []byte(`{"command":"foo-bin"}`)
	defs, err := ParseMCPJSON(data, "foo")
	if err != nil {
		t.Fatalf("ParseMCPJSON() error = %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "foo" {
		t.Fatalf("ParseMCPJSON() = %+v, want name from override", defs)
	}
}

func TestParseMCPJSONBareMissingNameErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseMCPJSON([]byte(`{"command":"foo-bin"}`), ""); err == nil {
		t.Fatal("ParseMCPJSON() missing name error = nil")
	}
}

func TestParseMCPJSONMalformedErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseMCPJSON([]byte(`{not json`), "foo"); err == nil {
		t.Fatal("ParseMCPJSON() malformed error = nil")
	}
}

func TestValidateDefinition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		def     model.Definition
		wantErr bool
	}{
		{"stdio ok", model.Definition{Name: "a", Command: "npx"}, false},
		{"stdio missing command", model.Definition{Name: "a"}, true},
		{"http ok", model.Definition{Name: "a", Type: "http", URL: "https://x"}, false},
		{"http missing url", model.Definition{Name: "a", Type: "http"}, true},
		{"sse missing url", model.Definition{Name: "a", Type: "sse"}, true},
		{"unknown type", model.Definition{Name: "a", Type: "grpc", Command: "x"}, true},
		{"bad name", model.Definition{Name: "../x", Command: "x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDefinition(tt.def)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateDefinition(%+v) err = %v, wantErr %v", tt.def, err, tt.wantErr)
			}
		})
	}
}

func TestRedactSecrets(t *testing.T) {
	t.Parallel()
	def := model.Definition{
		Name: "docs",
		Env: map[string]string{
			"API_TOKEN": "sk-real-secret",
			"LOG_LEVEL": "debug",
			"DB_TOKEN":  "${DB_TOKEN}",
		},
		Headers: map[string]string{
			"Authorization": "Bearer abc123",
		},
	}
	got := RedactSecrets(&def)
	want := []string{"API_TOKEN", "Authorization"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RedactSecrets() = %v, want %v", got, want)
	}
	if def.Env["API_TOKEN"] != "${API_TOKEN}" {
		t.Fatalf("API_TOKEN = %q, want ${API_TOKEN}", def.Env["API_TOKEN"])
	}
	if def.Env["LOG_LEVEL"] != "debug" {
		t.Fatalf("LOG_LEVEL = %q, want literal debug", def.Env["LOG_LEVEL"])
	}
	if def.Env["DB_TOKEN"] != "${DB_TOKEN}" {
		t.Fatalf("DB_TOKEN = %q, want existing reference kept", def.Env["DB_TOKEN"])
	}
	if def.Headers["Authorization"] != "${Authorization}" {
		t.Fatalf("Authorization = %q, want ${Authorization}", def.Headers["Authorization"])
	}
}

func TestIsSensitiveField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key, value string
		want       bool
	}{
		{"API_TOKEN", "x", true},
		{"MY_SECRET", "x", true},
		{"OPENAI_KEY", "x", true},
		{"DB_PASSWORD", "x", true},
		{"AWS_CREDENTIAL", "x", true},
		{"Authorization", "x", true},
		{"X", "Bearer y", true},
		{"LOG_LEVEL", "debug", false},
		{"PORT", "8080", false},
	}
	for _, tt := range tests {
		if got := IsSensitiveField(tt.key, tt.value); got != tt.want {
			t.Fatalf("IsSensitiveField(%q,%q) = %v, want %v", tt.key, tt.value, got, tt.want)
		}
	}
}
