package locator

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Locator is the canonical reference to a point or range inside a book. The
// envelope follows docs/LOCATORS.md §3: it is stored as JSON by the
// annotations and progress tables and is the only anchoring currency exposed
// to the client and, later, to plugins.
//
// The zero value is invalid; construct locators from the client and validate
// them with Validate before storing.
type Locator struct {
	V          int       `json:"v"`
	Book       *BookRef  `json:"book,omitempty"`
	Href       string    `json:"href"`
	Type       string    `json:"type,omitempty"`
	Title      string    `json:"title,omitempty"`
	Projection string    `json:"projection,omitempty"`
	Locations  Locations `json:"locations"`
	Text       TextQuote `json:"text,omitempty"`
}

// BookRef identifies the book a locator belongs to. Hash is the SHA-256 of
// the canonical EPUB, which is exact for as long as the catalog keeps
// content-addressed files.
type BookRef struct {
	Hash string `json:"hash,omitempty"`
	UUID string `json:"uuid,omitempty"`
	ISBN string `json:"isbn,omitempty"`
	Work string `json:"workId,omitempty"`
}

// Locations carries the composite anchors. All offsets are UTF-16 code units.
type Locations struct {
	PartialCFI       string    `json:"partialCfi,omitempty"`
	DomRange         *DomRange `json:"domRange,omitempty"`
	CharRange        *Range    `json:"charRange,omitempty"`
	Progression      float64   `json:"progression,omitempty"`
	TotalProgression float64   `json:"totalProgression,omitempty"`
	Position         int       `json:"position,omitempty"`
	Fragment         string    `json:"fragment,omitempty"`
}

// Range is a half-open [start,end) offset pair in UTF-16 code units. Points
// have start == end.
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// DomRange anchors a range to the rendered DOM with a CSS selector, the
// index of the text node among the selected element's direct text node
// children, and a UTF-16 character offset. See LOCATORS.md §3.
type DomRange struct {
	Start DOMAnchor `json:"start"`
	End   DOMAnchor `json:"end"`
}

// DOMAnchor is one endpoint of a DomRange.
type DOMAnchor struct {
	Selector      string `json:"cssSelector"`
	TextNodeIndex int    `json:"textNodeIndex"`
	CharOffset    int    `json:"charOffset"`
}

// TextQuote is the durable re-anchoring anchor. Slices are raw projection
// text; normalization happens only when comparing.
type TextQuote struct {
	Before    string `json:"before,omitempty"`
	Highlight string `json:"highlight,omitempty"`
	After     string `json:"after,omitempty"`
}

// Limits keep client-supplied locators bounded. The client stores full
// quotes locally; only shared/exported locators are capped (LOCATORS.md §8.3),
// so these are deliberately generous.
const (
	maxHrefBytes  = 1024
	maxTitleRunes = 512
	maxTextSlice  = 4096
	maxLocator    = 64 << 10
)

// DecodeLocator parses a stored or submitted locator and normalizes it.
func DecodeLocator(data []byte) (Locator, error) {
	var loc Locator
	if err := json.Unmarshal(data, &loc); err != nil {
		return Locator{}, fmt.Errorf("decode locator: %w", err)
	}
	return loc.Normalize(), nil
}

// Encode serializes the locator for storage.
func (l Locator) Encode() ([]byte, error) {
	data, err := json.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("encode locator: %w", err)
	}
	return data, nil
}

// Normalize fills in defaults and orders range endpoints. Progress values
// are validated rather than clamped so out-of-range client input is
// rejected instead of silently changed.
func (l Locator) Normalize() Locator {
	if l.V == 0 {
		l.V = 1
	}
	l.Href = strings.TrimSpace(l.Href)
	if l.Locations.CharRange != nil && l.Locations.CharRange.End < l.Locations.CharRange.Start {
		l.Locations.CharRange.Start, l.Locations.CharRange.End = l.Locations.CharRange.End, l.Locations.CharRange.Start
	}
	return l
}

// Validate reports whether the locator is structurally usable. Text caps are
// enforced here because the data crosses a trust boundary; oversized slices
// are rejected rather than silently truncated so clients keep honest state.
func (l Locator) Validate() error {
	switch {
	case l.V != 1:
		return fmt.Errorf("unsupported locator version %d", l.V)
	case strings.TrimSpace(l.Href) == "":
		return errors.New("locator: href is required")
	case len(l.Href) > maxHrefBytes:
		return errors.New("locator: href is too long")
	case len([]rune(l.Title)) > maxTitleRunes:
		return errors.New("locator: title is too long")
	case utf16Len(l.Text.Before) > maxTextSlice:
		return errors.New("locator: text.before is too long")
	case utf16Len(l.Text.Highlight) > maxTextSlice:
		return errors.New("locator: text.highlight is too long")
	case utf16Len(l.Text.After) > maxTextSlice:
		return errors.New("locator: text.after is too long")
	case l.Locations.Progression < 0 || l.Locations.Progression > 1:
		return errors.New("locator: progression is out of range")
	case l.Locations.TotalProgression < 0 || l.Locations.TotalProgression > 1:
		return errors.New("locator: totalProgression is out of range")
	}
	if loc := l.Locations.CharRange; loc != nil && (loc.Start < 0 || loc.End < loc.Start) {
		return errors.New("locator: charRange is out of order")
	}
	if r := l.Locations.DomRange; r != nil {
		if strings.TrimSpace(r.Start.Selector) == "" || strings.TrimSpace(r.End.Selector) == "" {
			return errors.New("locator: domRange selectors are required")
		}
		if r.Start.CharOffset < 0 || r.End.CharOffset < 0 || r.Start.TextNodeIndex < 0 || r.End.TextNodeIndex < 0 {
			return errors.New("locator: domRange offsets are negative")
		}
	}
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("locator: %w", err)
	}
	if len(data) > maxLocator {
		return errors.New("locator: document is too large")
	}
	return nil
}

// IsPoint reports whether the locator describes a collapsed range.
func (l Locator) IsPoint() bool {
	if l.Locations.CharRange != nil {
		return l.Locations.CharRange.Start == l.Locations.CharRange.End
	}
	if l.Locations.DomRange != nil {
		return l.Locations.DomRange.Start == l.Locations.DomRange.End
	}
	return true
}

// ProgressionValue returns the best available total progression for ordering
// and UI, preferring totalProgression and falling back to progress.
func (l Locator) ProgressionValue() float64 {
	if l.Locations.TotalProgression > 0 {
		return clamp01(l.Locations.TotalProgression)
	}
	return clamp01(l.Locations.Progression)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
