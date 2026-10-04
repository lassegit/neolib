package locator_test

import (
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/locator"
)

func TestLocatorRoundTrip(t *testing.T) {
	original := locator.Locator{
		V:    1,
		Book: &locator.BookRef{Hash: "sha256:abc"},
		Href: "OEBPS/ch1.xhtml",
		Type: "application/xhtml+xml",
		Locations: locator.Locations{
			DomRange: &locator.DomRange{
				Start: locator.DOMAnchor{Selector: "#p1", TextNodeIndex: 0, CharOffset: 2},
				End:   locator.DOMAnchor{Selector: "#p1", TextNodeIndex: 0, CharOffset: 9},
			},
			CharRange:        &locator.Range{Start: 10, End: 17},
			Progression:      0.25,
			TotalProgression: 0.1,
		},
		Text: locator.TextQuote{Before: "a ", Highlight: "sentence", After: " b"},
	}

	data, err := original.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := locator.DecodeLocator(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Href != original.Href || decoded.Locations.CharRange.Start != 10 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded locator invalid: %v", err)
	}
	if decoded.IsPoint() {
		t.Errorf("range locator reported as a point")
	}
	if got := decoded.ProgressionValue(); got != 0.1 {
		t.Errorf("ProgressionValue = %v, want 0.1", got)
	}
}

func TestLocatorNormalize(t *testing.T) {
	loc := locator.Locator{Href: "  ch1.xhtml  "}.Normalize()
	if loc.V != 1 || loc.Href != "ch1.xhtml" {
		t.Fatalf("normalize = %+v", loc)
	}

	// A backwards charRange is ordered rather than rejected.
	loc = locator.Locator{
		V:         1,
		Href:      "ch1.xhtml",
		Locations: locator.Locations{CharRange: &locator.Range{Start: 30, End: 10}},
	}.Normalize()
	if loc.Locations.CharRange.Start != 10 || loc.Locations.CharRange.End != 30 {
		t.Fatalf("charRange = %+v", loc.Locations.CharRange)
	}
}

func TestLocatorValidate(t *testing.T) {
	valid := locator.Locator{V: 1, Href: "ch1.xhtml"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid locator rejected: %v", err)
	}

	cases := []struct {
		name string
		loc  locator.Locator
	}{
		{"missing href", locator.Locator{V: 1}},
		{"wrong version", locator.Locator{V: 2, Href: "ch1.xhtml"}},
		{"progression too high", locator.Locator{V: 1, Href: "ch1.xhtml", Locations: locator.Locations{Progression: 1.5}}},
		{"negative progression", locator.Locator{V: 1, Href: "ch1.xhtml", Locations: locator.Locations{TotalProgression: -0.1}}},
		{"backwards charRange", locator.Locator{V: 1, Href: "ch1.xhtml", Locations: locator.Locations{CharRange: &locator.Range{Start: 5, End: 1}}}},
		{"domRange without selector", locator.Locator{V: 1, Href: "ch1.xhtml", Locations: locator.Locations{DomRange: &locator.DomRange{}}}},
		{"oversized quote", locator.Locator{V: 1, Href: "ch1.xhtml", Text: locator.TextQuote{Highlight: strings.Repeat("x", 5000)}}},
	}
	for _, tc := range cases {
		if err := tc.loc.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}
