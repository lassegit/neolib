package reader

import (
	"encoding/json"
	"fmt"
)

// Settings selects how a book is rendered and displayed.
//
// The zero value is not meaningful: use DefaultSettings, DecodeSettings, or
// Normalize. New preferences are added as fields with a JSON tag; missing
// fields keep the default, so stored settings documents do not need
// migrations.
type Settings struct {
	ExternalLinks string `json:"external_links"`
	Images        string `json:"images"`

	// Display preferences. These are applied client-side as CSS custom
	// properties and can also be changed from the in-reader display sheet.
	Theme      string  `json:"theme"`
	FontFamily string  `json:"font_family"`
	FontSize   float64 `json:"font_size"`
	LineHeight float64 `json:"line_height"`
	Measure    int     `json:"measure"`
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

// Themes.
const (
	ThemeAuto  = "auto"
	ThemeLight = "light"
	ThemeSepia = "sepia"
	ThemeDark  = "dark"
)

// Font families.
const (
	FontSerif = "serif"
	FontSans  = "sans"
)

// Display defaults and bounds.
const (
	DefaultFontSize   = 1.125
	DefaultLineHeight = 1.65
	DefaultMeasure    = 65

	MinFontSize   = 0.875
	MaxFontSize   = 2.25
	MinLineHeight = 1.2
	MaxLineHeight = 2.4
	MinMeasure    = 36
	MaxMeasure    = 110
)

// DefaultSettings returns the settings used when the user has none stored.
func DefaultSettings() Settings {
	return Settings{
		ExternalLinks: ExternalLinksNewTab,
		Images:        ImagesLink,
		Theme:         ThemeAuto,
		FontFamily:    FontSerif,
		FontSize:      DefaultFontSize,
		LineHeight:    DefaultLineHeight,
		Measure:       DefaultMeasure,
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

// Normalize replaces empty or unknown enumeration values with their defaults
// and clamps numeric preferences, so a newer or older settings document never
// disables rendering.
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
	switch s.Theme {
	case ThemeLight, ThemeSepia, ThemeDark:
	case ThemeAuto:
	default:
		s.Theme = ThemeAuto
	}
	switch s.FontFamily {
	case FontSerif, FontSans:
	default:
		s.FontFamily = FontSerif
	}
	s.FontSize = clamp(s.FontSize, MinFontSize, MaxFontSize, DefaultFontSize)
	s.LineHeight = clamp(s.LineHeight, MinLineHeight, MaxLineHeight, DefaultLineHeight)
	if s.Measure < MinMeasure || s.Measure > MaxMeasure {
		s.Measure = DefaultMeasure
	}
	return s
}

// Overlay returns a copy of s with the non-zero display fields of other
// applied. It is used by the settings API so a display-only update never
// disturbs behavioral preferences and vice versa.
func (s Settings) Overlay(other Settings) Settings {
	if other.Theme != "" {
		s.Theme = other.Theme
	}
	if other.FontFamily != "" {
		s.FontFamily = other.FontFamily
	}
	if other.FontSize != 0 {
		s.FontSize = other.FontSize
	}
	if other.LineHeight != 0 {
		s.LineHeight = other.LineHeight
	}
	if other.Measure != 0 {
		s.Measure = other.Measure
	}
	return s.Normalize()
}

// MergeBehavior overlays the form-submitted behavioral fields onto s and
// keeps display preferences. Empty submitted values leave the stored field
// untouched, which makes form handling tolerant of older clients.
func (s Settings) MergeBehavior(other Settings) Settings {
	if other.ExternalLinks != "" {
		s.ExternalLinks = other.ExternalLinks
	}
	if other.Images != "" {
		s.Images = other.Images
	}
	return s.Normalize()
}

// ValidateBehavior reports whether the behavioral enumerations are empty or
// one of the accepted values. Empty means "not submitted".
func (s Settings) ValidateBehavior() bool {
	switch s.ExternalLinks {
	case "", ExternalLinksNewTab, ExternalLinksSameTab:
	default:
		return false
	}
	switch s.Images {
	case "", ImagesPlain, ImagesLink:
	default:
		return false
	}
	return true
}

// ValidateDisplay reports whether the display fields are empty or within
// bounds. Empty (zero) means "not submitted".
func (s Settings) ValidateDisplay() bool {
	switch s.Theme {
	case "", ThemeAuto, ThemeLight, ThemeSepia, ThemeDark:
	default:
		return false
	}
	switch s.FontFamily {
	case "", FontSerif, FontSans:
	default:
		return false
	}
	if s.FontSize != 0 && (s.FontSize < MinFontSize || s.FontSize > MaxFontSize) {
		return false
	}
	if s.LineHeight != 0 && (s.LineHeight < MinLineHeight || s.LineHeight > MaxLineHeight) {
		return false
	}
	if s.Measure != 0 && (s.Measure < MinMeasure || s.Measure > MaxMeasure) {
		return false
	}
	return true
}

func clamp(v, min, max, fallback float64) float64 {
	if v < min || v > max {
		if v == 0 {
			return fallback
		}
		if v < min {
			return min
		}
		return max
	}
	return v
}
