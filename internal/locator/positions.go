package locator

import (
	"strconv"
	"unicode/utf16"
)

// Position is one deterministic synthetic page boundary. Positions are
// derived only from the projection, so every device numbers them the same
// regardless of viewport, font, or pagination.
type Position struct {
	Position         int // 1-based, globally ordered across the spine
	Href             string
	MediaType        string
	Fragment         string  // partial CFI of the boundary
	Progression      float64 // within the resource
	TotalProgression float64 // within the publication
}

// boundary is one candidate position before global numbering.
type boundary struct {
	chapter int
	offset  int // UTF-16 offset in the chapter projection
	path    string
	char    int // CFI character offset within the anchored chunk
}

// buildPositions places one synthetic position per PositionChunk UTF-16 code
// units in every chapter. anchors must be parallel to chapters.
func buildPositions(chapters []Chapter, anchors [][]anchor) []Position {
	var bounds []boundary
	for i, ch := range chapters {
		if ch.UTF16Length == 0 {
			continue
		}
		units := utf16.Encode([]rune(ch.Text))
		for off := 0; off < ch.UTF16Length; off += PositionChunk {
			off = snapForwardToCodePoint(units, off)
			a, char, ok := anchorAt(anchors[i], off)
			if !ok {
				continue
			}
			bounds = append(bounds, boundary{chapter: i, offset: off, path: a.path, char: char})
		}
	}

	positions := make([]Position, 0, len(bounds))
	for i, b := range bounds {
		ch := chapters[b.chapter]
		progress := 0.0
		if ch.UTF16Length > 0 {
			progress = float64(b.offset) / float64(ch.UTF16Length)
		}
		total := 0.0
		if len(bounds) > 0 {
			total = float64(i+1) / float64(len(bounds))
		}
		positions = append(positions, Position{
			Position:         i + 1,
			Href:             ch.Href,
			MediaType:        ch.MediaType,
			Fragment:         b.path + ":" + strconv.Itoa(b.char),
			Progression:      progress,
			TotalProgression: total,
		})
	}
	return positions
}

// anchorAt translates a projection offset to a partial CFI path and a CFI
// character offset. When the offset falls on content that has no anchor
// (a synthetic br space, or a gap), it snaps forward to the next text node.
func anchorAt(anchors []anchor, off int) (anchor, int, bool) {
	for _, a := range anchors {
		if off >= a.start && off <= a.start+a.length {
			return a, a.chunkBase + (off - a.start), true
		}
	}
	for _, a := range anchors {
		if a.start >= off {
			return a, a.chunkBase, true
		}
	}
	return anchor{}, 0, false
}

// snapForwardToCodePoint moves off past a split surrogate pair so a position
// never starts halfway through a non-BMP character.
func snapForwardToCodePoint(units []uint16, off int) int {
	if off <= 0 || off >= len(units) {
		return off
	}
	if isHighSurrogate(units[off-1]) && isLowSurrogate(units[off]) {
		return off + 1
	}
	return off
}

func isHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }
func isLowSurrogate(u uint16) bool  { return u >= 0xDC00 && u <= 0xDFFF }
