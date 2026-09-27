// Package locator computes the derived reference data for books: the
// logical-text projection of each spine document and the deterministic
// position list built on top of it. The projection is the coordinate system
// that locator offsets index into; see docs/LOCATORS.md §4.
//
// The implementation deliberately uses only the standard library and
// golang.org/x/net/html (already a dependency of the reader). Algorithms are
// implemented in-house; no external JavaScript locator libraries are used.
package locator

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/lassegit/neolib/internal/epub"
)

const (
	// ProjectionVersion identifies the logical-text algorithm. Chapter
	// locators record the version their offsets were computed with.
	ProjectionVersion = "neolib/logical-text/1"

	// PositionChunk is the number of UTF-16 code units per synthetic
	// position (one "synthetic page").
	PositionChunk = 1024
)

// Chapter is the logical-text projection of one spine document.
type Chapter struct {
	SpineIndex  int
	Href        string
	MediaType   string
	Projection  string
	UTF16Length int
	Text        string
}

// skippedElements are excluded together with their subtrees.
var skippedElements = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Template: true,
}

// anchor marks one text node in the projection so that any UTF-16 offset can
// be translated to a partial CFI.
type anchor struct {
	start     int    // UTF-16 offset in the projection where the node begins
	length    int    // UTF-16 length of the node
	chunkBase int    // UTF-16 offset of the node within its CFI character-data chunk
	path      string // partial CFI path to the containing character-data chunk
}

// projector accumulates one chapter's projection.
type projector struct {
	text    strings.Builder
	length  int
	anchors []anchor
}

// project computes the logical-text projection of one content document. A
// document that cannot be decoded, parsed, or located in is reported as an
// error; callers decide whether to skip the chapter.
func project(spineIndex int, href, mediaType string, raw []byte) (Chapter, []anchor, error) {
	chapter := Chapter{
		SpineIndex: spineIndex,
		Href:       href,
		MediaType:  mediaType,
		Projection: ProjectionVersion,
	}

	source, err := epub.DecodeXHTML(raw)
	if err != nil {
		return chapter, nil, err
	}
	doc, err := html.Parse(source)
	if err != nil {
		return chapter, nil, fmt.Errorf("parse xhtml: %w", err)
	}

	body := findElement(doc, atom.Body)
	if body == nil {
		return chapter, nil, errors.New("no body element")
	}

	// CFI paths are relative to the content document root, so a body nested
	// under <html> must contribute its own step (normally /4).
	prefix := ""
	if root := findElement(doc, atom.Html); root != nil && root != body {
		prefix = childPath(root, body)
	}

	p := &projector{}
	p.walkChildren(body, prefix)
	chapter.Text = p.text.String()
	chapter.UTF16Length = p.length
	return chapter, p.anchors, nil
}

// walkChildren appends the character data of parent's subtree to the
// projection. parentPath is the partial CFI path to parent. Numbering follows
// EPUB CFI 1.1 §3.1.1: child elements get even indices starting at 2, and
// contiguous runs of character data get odd indices starting at 1.
func (p *projector) walkChildren(parent *html.Node, parentPath string) {
	elements := 0  // child elements seen so far in this parent
	chunkStep := 0 // odd step of the current character-data chunk, 0 between chunks
	chunkBase := 0 // projection offset where the current chunk starts

	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if chunkStep == 0 {
				chunkStep = 2*elements + 1
				chunkBase = p.length
			}
			node := anchor{
				start:     p.length,
				length:    utf16Length(child.Data),
				chunkBase: chunkBase,
				path:      parentPath + "/" + strconv.Itoa(chunkStep),
			}
			p.anchors = append(p.anchors, node)
			p.write(child.Data)

		case html.ElementNode:
			elements++
			if skippedElements[child.DataAtom] {
				// The element still occupies its step in the DOM, but its
				// content is not part of the projection and it separates
				// the surrounding character-data chunks.
				chunkStep = 0
				continue
			}
			if child.DataAtom == atom.Br {
				// A br renders as one space. It is not real character data,
				// so it gets no anchor: offsets that fall on the synthetic
				// space snap forward to the next text node.
				p.write(" ")
				chunkStep = 0
				continue
			}

			segment := "/" + strconv.Itoa(2*elements)
			if id := nodeAttr(child, "id"); id != "" {
				segment += "[" + escapeID(id) + "]"
			}
			p.walkChildren(child, parentPath+segment)
			chunkStep = 0
		}
	}
}

func (p *projector) write(s string) {
	p.text.WriteString(s)
	p.length += utf16Length(s)
}

// utf16Length returns the number of UTF-16 code units in s, which is the
// offset unit used by DOM Range and EPUB CFI.
func utf16Length(s string) int {
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

// findElement returns the first element with the given atom, depth first.
func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, a); found != nil {
			return found
		}
	}
	return nil
}

// childPath returns the CFI element step from root to child, or "" when
// child is not an element child of root.
func childPath(root, child *html.Node) string {
	elements := 0
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		elements++
		if c == child {
			return "/" + strconv.Itoa(2*elements)
		}
	}
	return ""
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
