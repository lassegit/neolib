package reader_test

import (
	"testing"

	"github.com/lassegit/neolib/internal/reader"
)

// A stored settings document from before the table of contents preference
// existed keeps the inline default.
func TestDecodeSettingsTOCDefault(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("{}"),
		[]byte(`{"external_links":"same_tab","images":"plain"}`),
	} {
		settings, err := reader.DecodeSettings(data)
		if err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		if settings.TOC != reader.TOCInline {
			t.Errorf("TOC for %q = %q, want %q", data, settings.TOC, reader.TOCInline)
		}
		if settings.SideTOC() {
			t.Errorf("TOC for %q is a sidebar, want inline", data)
		}
	}
}

func TestDecodeSettingsTOC(t *testing.T) {
	settings, err := reader.DecodeSettings([]byte(`{"toc":"right"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings.TOC != reader.TOCRight || !settings.SideTOC() {
		t.Errorf("TOC = %q SideTOC = %v, want %q true", settings.TOC, settings.SideTOC(), reader.TOCRight)
	}
}

func TestNormalizeTOC(t *testing.T) {
	if got := (reader.Settings{TOC: "sideways"}).Normalize().TOC; got != reader.TOCInline {
		t.Errorf("invalid TOC normalized to %q, want %q", got, reader.TOCInline)
	}
}
