package epub_test

import (
	"io"
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/epub"
)

func decode(t *testing.T, raw []byte) string {
	t.Helper()
	decoded, err := epub.DecodeXHTML(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("read decoded: %v", err)
	}
	return string(out)
}

// Encoding must come from the XML prolog, not from sniffing the first
// kilobyte: UTF-8 text that starts after a long ASCII prefix must survive.
func TestDecodeXHTMLLongASCIIPrefix(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="utf-8"?><html><body><p>` +
		strings.Repeat("a", 2000) + "🦊" + `</p></body></html>`)

	if got := decode(t, raw); !strings.Contains(got, "🦊") {
		t.Fatalf("decoded text lost the non-BMP character")
	}
}

func TestDecodeXHTMLDeclaredEncoding(t *testing.T) {
	raw := []byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?>" +
		"<html><body><p>caf\xe9</p></body></html>")

	if got := decode(t, raw); !strings.Contains(got, "café") {
		t.Fatalf("decoded = %q, want café", got)
	}
}

// Without a declaration, EPUB 3 requires UTF-8, regardless of what the
// first kilobyte looks like.
func TestDecodeXHTMLDefaultsToUTF8(t *testing.T) {
	raw := []byte("<html><body><p>héllo 🦊</p></body></html>")

	if got := decode(t, raw); !strings.Contains(got, "héllo 🦊") {
		t.Fatalf("decoded = %q, want UTF-8 text", got)
	}
}
