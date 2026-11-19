package toolschema

import "strings"

// Escape a literal opening bracket inside an ECMAScript character class.
// Codex rejects the unescaped spelling even though ECMAScript allows it.
func normalizeCodexPatternClassBrackets(pattern string) string {
	var out strings.Builder
	inClass := false
	last := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			if inClass {
				out.WriteString(pattern[last:i])
				out.WriteString(`\[`)
				last = i + 1
			} else {
				inClass = true
			}
		case ']':
			inClass = false
		}
	}
	if last == 0 {
		return pattern
	}
	out.WriteString(pattern[last:])
	return out.String()
}
