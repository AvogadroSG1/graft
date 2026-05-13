package model

import "testing"

func TestDefinitionAdapterMergesTransportOverrides(t *testing.T) {
	t.Parallel()
	def := Definition{
		Name:    "docs",
		Type:    "sse",
		URL:     "https://base.example/mcp",
		Headers: map[string]string{"Authorization": "${AUTH}"},
		Adapters: map[string]AdapterConfig{
			"claude": {
				Type:    "http",
				URL:     "https://claude.example/mcp",
				Headers: map[string]string{"X-Token": "${TOKEN}"},
			},
		},
	}

	got := def.Adapter("claude")

	if got.Type != "http" || got.URL != "https://claude.example/mcp" {
		t.Fatalf("Adapter() transport = (%q, %q), want claude override", got.Type, got.URL)
	}
	if got.Headers["X-Token"] != "${TOKEN}" || got.Headers["Authorization"] != "${AUTH}" {
		t.Fatalf("Adapter() headers = %+v, want merged headers", got.Headers)
	}
}
