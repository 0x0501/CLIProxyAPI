package toolschema

import (
	"testing"

	"github.com/MoeForge/moego/ojson"
)

func TestNormalizeCodexPatternClassBrackets(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"path", `^(?:\.|[^/\\*?[\]{}\u0000-\u001f]+(?:\/[^/\\*?[\]{}\u0000-\u001f]+)*)$`, `^(?:\.|[^/\\*?\[\]{}\u0000-\u001f]+(?:\/[^/\\*?\[\]{}\u0000-\u001f]+)*)$`},
		{"already escaped", `[^/\\*?\[\]{}]+`, `[^/\\*?\[\]{}]+`},
		{"literal bracket", `[[]`, `[\[]`},
		{"escaped outside class", `^a\[b$`, `^a\[b$`},
		{"ordinary class", `^[a-z0-9]+$`, `^[a-z0-9]+$`},
		{"escaped backslash", `[\\[]`, `[\\\[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCodexPatternClassBrackets(tt.input)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if twice := normalizeCodexPatternClassBrackets(got); twice != got {
				t.Fatalf("normalization is not idempotent: %q", twice)
			}
		})
	}
}

func TestNormalizeCodexToolSchemasNestedClassBrackets(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"start","parameters":{"type":"object","properties":{"paths":{"type":"array","items":{"type":"string","pattern":"[[]","default":"[[]","enum":["[[]"]}},"simple":{"type":"string","pattern":"^[a-z]+$"}}}}]}`)
	normalized := NormalizeCodexToolSchemas(body)
	path := "tools.0.parameters.properties.paths.items"
	if got := ojson.GetBytesNoCopy(normalized, path+".pattern").String(); got != `[\[]` {
		t.Fatalf("nested pattern = %q", got)
	}
	for _, field := range []string{"default", "enum.0"} {
		if got := ojson.GetBytesNoCopy(normalized, path+"."+field).String(); got != "[[]" {
			t.Fatalf("%s changed: %q", field, got)
		}
	}
	if got := ojson.GetBytesNoCopy(normalized, "tools.0.parameters.properties.simple.pattern").String(); got != "^[a-z]+$" {
		t.Fatalf("ordinary pattern changed: %q", got)
	}
}
