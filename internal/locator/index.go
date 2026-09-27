package locator

import (
	"fmt"

	"github.com/lassegit/neolib/internal/epub"
)

// Index is the derived locator data for one book: the per-chapter logical
// text and the deterministic position list.
type Index struct {
	Chapters  []Chapter
	Positions []Position
	// Warnings names spine documents that could not be indexed. The rest
	// of the index is still usable; the book stays readable.
	Warnings []string
}

// IndexPublication computes the projection and position list for every
// readable document in the publication's spine, in reading order. Documents
// that cannot be read or projected are reported in Warnings and skipped.
func IndexPublication(pub *epub.Publication) Index {
	spine := pub.Spine()
	idx := Index{
		Chapters:  make([]Chapter, 0, len(spine)),
		Positions: []Position{},
	}
	anchors := make([][]anchor, 0, len(spine))

	for i, ch := range spine {
		raw, err := pub.Read(ch.Href, epub.MaxChapterBytes)
		if err != nil {
			idx.Warnings = append(idx.Warnings, fmt.Sprintf("%s: %v", ch.Href, err))
			continue
		}
		chapter, a, err := project(i, ch.Href, pub.MediaType(ch.Href), raw)
		if err != nil {
			idx.Warnings = append(idx.Warnings, fmt.Sprintf("%s: %v", ch.Href, err))
			continue
		}
		idx.Chapters = append(idx.Chapters, chapter)
		anchors = append(anchors, a)
	}

	idx.Positions = buildPositions(idx.Chapters, anchors)
	if idx.Positions == nil {
		idx.Positions = []Position{}
	}
	return idx
}
