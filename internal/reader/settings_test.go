package reader_test

import (
	"testing"

	"github.com/lassegit/neolib/internal/reader"
)

// A stored table-of-contents preference from when the sidebar could start
// open is ignored without breaking the rest of the document.
func TestDecodeSettingsIgnoresLegacyTOC(t *testing.T) {
	settings, err := reader.DecodeSettings([]byte(`{"toc":"left","external_links":"same_tab"}`))
	if err != nil {
		t.Fatalf("decode legacy toc: %v", err)
	}
	if settings.ExternalLinks != reader.ExternalLinksSameTab {
		t.Errorf("ExternalLinks = %q, want %q", settings.ExternalLinks, reader.ExternalLinksSameTab)
	}
}

func TestNormalizeDisplay(t *testing.T) {
	settings := reader.Settings{
		Theme:      "neon",
		FontFamily: "comic",
		FontSize:   99,
		LineHeight: 0.1,
		Measure:    5,
	}.Normalize()
	if settings.Theme != reader.ThemeAuto {
		t.Errorf("Theme = %q, want %q", settings.Theme, reader.ThemeAuto)
	}
	if settings.FontFamily != reader.FontSerif {
		t.Errorf("FontFamily = %q, want %q", settings.FontFamily, reader.FontSerif)
	}
	if settings.FontSize != reader.MaxFontSize {
		t.Errorf("FontSize = %v, want %v", settings.FontSize, reader.MaxFontSize)
	}
	if settings.LineHeight != reader.MinLineHeight {
		t.Errorf("LineHeight = %v, want %v", settings.LineHeight, reader.MinLineHeight)
	}
	if settings.Measure != reader.DefaultMeasure {
		t.Errorf("Measure = %v, want %v", settings.Measure, reader.DefaultMeasure)
	}
}
