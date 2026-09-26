package reader

import (
	"encoding/json"
	"fmt"
)

// Settings selects how chapters are decorated when a book is rendered.
//
// The zero value is not meaningful: use DefaultSettings, DecodeSettings, or
// Normalize. New preferences are added as fields with a JSON tag; missing
// fields keep the default, so stored settings documents do not need
// migrations.
type Settings struct {
	ExternalLinks string `json:"external_links"`
	Images        string `json:"images"`
	TOC           string `json:"toc"`
}

// External link policies.
const (
	ExternalLinksNewTab  = "new_tab"
	ExternalLinksSameTab = "same_tab"
)

// Image policies.
const (
	ImagesPlain = "plain"
	ImagesLink  = "link"
)

// Table of contents layouts.
const (
	TOCInline = "inline"
	TOCHidden = "hidden"
	TOCLeft   = "left"
	TOCRight  = "right"
)

// SideTOC reports whether the table of contents is rendered as a sticky
// sidebar rather than in the flow of the book.
func (s Settings) SideTOC() bool {
	return s.TOC == TOCLeft || s.TOC == TOCRight
}

// DefaultSettings returns the settings used when the user has none stored.
func DefaultSettings() Settings {
	return Settings{
		ExternalLinks: ExternalLinksNewTab,
		Images:        ImagesLink,
		TOC:           TOCInline,
	}
}

// DecodeSettings overlays a stored JSON document on the defaults. A missing
// document yields the defaults; unreadable JSON returns an error and no
// settings, so callers surface the problem instead of silently resetting the
// user's preferences.
func DecodeSettings(data []byte) (Settings, error) {
	settings := DefaultSettings()
	if len(data) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("decode reader settings: %w", err)
	}
	return settings.Normalize(), nil
}

// Encode serializes the settings for storage.
func (s Settings) Encode() ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode reader settings: %w", err)
	}
	return data, nil
}

// Normalize replaces empty or unknown enumeration values with their defaults,
// so a newer or older settings document never disables rendering.
func (s Settings) Normalize() Settings {
	switch s.ExternalLinks {
	case ExternalLinksNewTab, ExternalLinksSameTab:
	default:
		s.ExternalLinks = ExternalLinksNewTab
	}
	switch s.Images {
	case ImagesPlain, ImagesLink:
	default:
		s.Images = ImagesLink
	}
	switch s.TOC {
	case TOCInline, TOCHidden, TOCLeft, TOCRight:
	default:
		s.TOC = TOCInline
	}
	return s
}
