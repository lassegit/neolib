// Package reader turns an EPUB's spine into a single scrollable HTML
// document for the browser. Chapter HTML is sanitized and every reference
// (anchors, images, styles) is rewritten so that the book stays intact when
// its chapters are concatenated into one page.
package reader

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"

	"github.com/lassegit/neolib/internal/epub"
)

const (
	maxChapterBytes = 16 << 20
	maxNavBytes     = 4 << 20
)

// Document is a publication laid out as one page.
type Document struct {
	Chapters []Chapter
	// TOC is the table of contents built from the publication's navigation
	// documents, with one entry per chapter for sections they do not cover.
	TOC []TOCEntry
}

// Chapter is one spine document with sanitized, reference-preserving HTML.
type Chapter struct {
	ID      string // section id, e.g. "c3"
	Title   string // navigation title, first heading, or "Chapter N"
	LabelID string // id of the element that labels the section
	HTML    string // sanitized HTML fragment
	Err     string // non-empty when the chapter could not be read
}

// Build reads the publication's spine and returns the whole book as one
// document. Per-chapter failures are reported on the chapter instead of
// failing the book, because the remaining chapters are still readable.
func Build(pub *epub.Publication, bookID string, settings Settings) Document {
	settings = settings.Normalize()
	spine := pub.Spine()
	if len(spine) == 0 {
		return Document{}
	}

	navItems, navBase, titles := readNavigation(pub)
	bookTitle := pub.Title()
	byPath := make(map[string]int, len(spine))
	for i, ch := range spine {
		byPath[ch.Href] = i
	}

	doc := Document{Chapters: make([]Chapter, 0, len(spine))}
	sections := make([]section, 0, len(spine))
	for i, ch := range spine {
		out := Chapter{
			ID:    fmt.Sprintf("c%d", i),
			Title: titles[ch.Href],
		}

		var ids map[string]bool
		raw, err := pub.Read(ch.Href, maxChapterBytes)
		if err != nil {
			out.Err = "This chapter could not be read."
		} else {
			rendered := renderChapter(pub, bookID, raw, i, ch.Href, byPath, settings)
			out.HTML = rendered.html
			out.LabelID = rendered.labelID
			ids = rendered.ids
			if out.Title == "" {
				out.Title = rendered.heading
			}
			if out.Title == "" && rendered.docTitle != "" && !strings.EqualFold(rendered.docTitle, bookTitle) {
				out.Title = rendered.docTitle
			}
			// Every section needs a label for assistive technology and a
			// visible heading when the chapter does not provide one of its own.
			if out.LabelID == "" && out.HTML != "" {
				if out.Title == "" {
					out.Title = fmt.Sprintf("Chapter %d", i+1)
				}
				out.HTML, out.LabelID = headingThenBody(out.HTML, rendered.fallbackID, out.Title)
			}
		}
		if out.Title == "" {
			out.Title = fmt.Sprintf("Chapter %d", i+1)
		}
		doc.Chapters = append(doc.Chapters, out)
		sections = append(sections, section{id: out.ID, ids: ids, title: out.Title})
	}

	if len(navItems) > 0 {
		doc.TOC = buildTOC(navItems, navBase, pub, byPath, sections)
		doc.TOC = fillTOCGaps(doc.TOC, sections)
	}
	if len(doc.TOC) == 0 {
		doc.TOC = fallbackTOC(doc.Chapters)
	}
	return doc
}

// renderedChapter is the sanitized result for one spine document.
type renderedChapter struct {
	html       string
	labelID    string
	heading    string
	docTitle   string
	fallbackID string
	ids        map[string]bool
}

// renderChapter parses one content document, sanitizes it, rewrites its
// references, applies the reader settings, and returns the body fragment,
// the id of its labelling element, its first heading text, its document
// title, a unique id for a generated heading when the document has none,
// and the ids present in the fragment.
func renderChapter(pub *epub.Publication, bookID string, raw []byte, index int, href string, spine map[string]int, settings Settings) renderedChapter {
	source, err := charset.NewReader(bytes.NewReader(raw), "text/html")
	if err != nil {
		source = bytes.NewReader(raw)
	}
	doc, err := html.Parse(source)
	if err != nil {
		return renderedChapter{}
	}
	body := findBody(doc)
	if body == nil {
		return renderedChapter{}
	}
	docTitle := documentTitle(doc)

	t := &transformer{
		pub:       pub,
		bookID:    bookID,
		sectionID: fmt.Sprintf("c%d", index),
		href:      href,
		baseDir:   path.Dir(href),
		spine:     spine,
		settings:  settings,
	}
	t.markExistingIDs(body)

	for child := body.FirstChild; child != nil; {
		next := child.NextSibling
		switch child.Type {
		case html.ElementNode:
			t.element(child)
		case html.CommentNode, html.DoctypeNode:
			body.RemoveChild(child)
		}
		child = next
	}

	if settings.Images == ImagesLink {
		wrapImages(body)
	}

	// A link may target the chapter's body element itself; keep that anchor
	// addressable even though the body wrapper is not emitted.
	bodyAnchor := prefixID(t.sectionID, bodyID(body))

	var out bytes.Buffer
	if bodyAnchor != "" {
		t.markID(bodyAnchor)
		anchor := &html.Node{
			Type:     html.ElementNode,
			Data:     "span",
			DataAtom: atom.Span,
			Attr:     []html.Attribute{{Key: "id", Val: bodyAnchor}},
		}
		html.Render(&out, anchor)
	}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		html.Render(&out, child)
	}
	fallbackID := t.uniqueID(t.sectionID + "-title")
	ids := collectIDs(body)
	if bodyAnchor != "" {
		ids[bodyAnchor] = true
	}
	return renderedChapter{
		html:       out.String(),
		labelID:    t.headingID,
		heading:    t.headingText,
		docTitle:   docTitle,
		fallbackID: fallbackID,
		ids:        ids,
	}
}

// bodyID returns the id of a body element, checking both id spellings.
func bodyID(body *html.Node) string {
	if id := attrValue(body, "id"); id != "" {
		return id
	}
	return attrValue(body, "xml:id")
}

// headingThenBody prepends a generated chapter heading to a fragment and
// returns the heading's id as the section label. The id comes from the
// transformer so it cannot collide with an element already in the chapter.
func headingThenBody(body, labelID, title string) (string, string) {
	heading := &html.Node{
		Type:     html.ElementNode,
		Data:     "h2",
		DataAtom: atom.H2,
		Attr:     []html.Attribute{{Key: "id", Val: labelID}},
	}
	heading.AppendChild(&html.Node{Type: html.TextNode, Data: title})

	var out bytes.Buffer
	html.Render(&out, heading)
	out.WriteString(body)
	return out.String(), labelID
}

// documentTitle returns the contents of the document's title element.
func documentTitle(root *html.Node) string {
	var title *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if title != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Title {
			title = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if title == nil {
		return ""
	}
	return strings.Join(strings.Fields(textContent(title)), " ")
}

// findBody returns the document's body element.
func findBody(root *html.Node) *html.Node {
	var body *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if body != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			body = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return body
}
