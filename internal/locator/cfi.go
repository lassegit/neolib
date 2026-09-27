package locator

import "strings"

// cfiEscaper escapes the CFI grammar delimiters that may appear inside an ID
// or text assertion. Escaping is defined by EPUB CFI 1.1 §2.3: the circumflex
// escapes itself and the delimiter characters.
var cfiEscaper = strings.NewReplacer(
	"^", "^^",
	"[", "^[",
	"]", "^]",
	"(", "^(",
	")", "^)",
	",", "^,",
	";", "^;",
)

// escapeID escapes an element ID for use in a CFI ID assertion.
func escapeID(id string) string {
	return cfiEscaper.Replace(id)
}
