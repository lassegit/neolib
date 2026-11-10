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

	"github.com/lassegit/neolib/internal/epub"
)

const maxNavBytes = 4 << 20

// Document is a publication laid out as one page.
type Document struct {
	Chapters []Chapter
	// TOC is the table of contents built from the publication's navigation
	// documents, with recoverable headings added for sections they omit.
	TOC []TOCEntry
}

// Chapter is one spine document with sanitized, reference-preserving HTML.
type Chapter struct {
	ID           string // section id, e.g. "c3"
	Href         string // archive path of the spine document
	Title        string // accessible section label
	LabelID      string // id of the element that labels the section
	HTML         string // sanitized HTML fragment
	Err          string // non-empty when the chapter could not be read
	Continuation bool   // a split resource with a repeated running heading
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

	primary, secondary, legacyTitles := readNavigation(pub)
	bookTitle := pub.Title()
	byPath := make(map[string]int, len(spine))
	for i, ch := range spine {
		byPath[ch.Href] = i
	}

	doc := Document{Chapters: make([]Chapter, 0, len(spine))}
	sections := make([]section, 0, len(spine))
	renderedChapters := make([]renderedChapter, 0, len(spine))
	var targets []contentTarget
	var outline navigationSource
	outlineIndex := -1
	for i, ch := range spine {
		out := Chapter{ID: fmt.Sprintf("c%d", i), Href: ch.Href, Title: legacyTitles[ch.Href]}
		var rendered renderedChapter
		raw, err := pub.Read(ch.Href, epub.MaxChapterBytes)
		if err != nil {
			out.Err = "This chapter could not be read."
		} else {
			if bytes.Contains(raw, []byte("Document Outline")) {
				if items := parseDocumentOutline(raw); len(items) > len(outline.items) {
					outline = navigationSource{items: items, base: path.Dir(ch.Href)}
					outlineIndex = i
				}
			}
			rendered = renderChapter(pub, bookID, raw, i, ch.Href, byPath, settings)
			out.HTML = rendered.html
			out.LabelID = rendered.labelID
			if out.Title == "" {
				out.Title = rendered.heading
			}
			if out.Title == "" && rendered.docTitle != "" && !strings.EqualFold(rendered.docTitle, bookTitle) {
				out.Title = rendered.docTitle
			}
			if out.LabelID == "" && out.HTML != "" {
				if out.Title == "" {
					out.Title = fmt.Sprintf("Chapter %d", i+1)
				}
				// Retain the old generated node for saved DOM locators, but do
				// not display a fabricated chapter heading in the book.
				out.HTML, _ = headingThenBody(out.HTML, rendered.fallbackID, out.Title)
			}
		}
		for _, target := range rendered.targets {
			target.section = i
			targets = append(targets, target)
		}
		doc.Chapters = append(doc.Chapters, out)
		renderedChapters = append(renderedChapters, rendered)
		sections = append(sections, section{
			id: out.ID, ids: rendered.ids, authorIDs: rendered.authorIDs,
			idOrder: rendered.idOrder, heading: rendered.heading,
			leadingID: rendered.labelID, leadingHeading: rendered.leadingHeading,
		})
	}

	// Only a navigation target that actually exists can label an entire
	// spine resource. Otherwise use a real leading heading, not a random
	// label from a broken TOC or a fabricated chapter number.
	titles := validNavTitles(primary, pub, byPath, sections)
	mergeTitles(titles, validNavTitles(secondary, pub, byPath, sections))
	for i := range doc.Chapters {
		chapter := &doc.Chapters[i]
		rendered := renderedChapters[i]
		chapter.Title = titles[chapter.Href]
		sections[i].navTitle = chapter.Title != ""
		if chapter.Title == "" && rendered.leadingHeading {
			chapter.Title = rendered.heading
		}
		if chapter.Title == "" && usefulDocumentTitle(rendered.docTitle, bookTitle) {
			chapter.Title = rendered.docTitle
		}
		if chapter.Title == "" {
			chapter.Title = fmt.Sprintf("Section %d", i+1)
		}
		sections[i].title = chapter.Title
	}
	markContinuations(doc.Chapters, sections, targets)

	resolver := newTOCResolver(primary, pub, byPath, sections, targets)
	doc.TOC = resolver.build(primary.items)
	repairs := make(map[string][]linkRepair)
	mergeLinkRepairs(repairs, resolver.repairs)
	if tocLinkCount(doc.TOC) == 0 && len(secondary.items) > 0 {
		resolver = newTOCResolver(secondary, pub, byPath, sections, targets)
		doc.TOC = resolver.build(secondary.items)
		mergeLinkRepairs(repairs, resolver.repairs)
	}
	if (resolver.broken || tocLinkCount(doc.TOC) == 0) && len(outline.items) > 0 {
		candidate := newTOCResolver(outline, pub, byPath, sections, targets)
		outlineTOC := candidate.build(outline.items)
		mergeLinkRepairs(repairs, candidate.repairs)
		if tocLinkCount(outlineTOC) > tocLinkCount(doc.TOC)+3 {
			resolver = candidate
			doc.TOC = outlineTOC
			sections[outlineIndex].navigationOnly = true
		}
	}
	hasNavigation := tocLinkCount(doc.TOC) > 0
	if !hasNavigation {
		doc.TOC = nil
	}
	doc.TOC = fillTOCGaps(doc.TOC, sections, targets, !hasNavigation)
	if len(doc.TOC) == 0 {
		doc.TOC = fallbackTOC(doc.Chapters)
	}
	for i := range doc.Chapters {
		doc.Chapters[i].HTML = repairContentLinks(doc.Chapters[i].HTML, repairs)
	}
	return doc
}

func usefulDocumentTitle(title, bookTitle string) bool {
	title = strings.TrimSpace(title)
	if epub.PlaceholderTitle(title) || strings.EqualFold(title, bookTitle) {
		return false
	}
	switch strings.ToLower(title) {
	case "index", "cover", "title page", "navigation":
		return false
	}
	return true
}

// renderedChapter is the sanitized result for one spine document.
type renderedChapter struct {
	html           string
	labelID        string
	heading        string
	leadingHeading bool
	docTitle       string
	fallbackID     string
	ids            map[string]bool
	authorIDs      map[string]bool
	idOrder        map[string]int
	targets        []contentTarget
}

// renderChapter parses one content document, sanitizes it, rewrites its
// references, applies the reader settings, and returns the body fragment,
// the id of its labelling element, its first heading text, its document
// title, a unique id for a generated heading when the document has none,
// and the ids present in the fragment.
func renderChapter(pub *epub.Publication, bookID string, raw []byte, index int, href string, spine map[string]int, settings Settings) renderedChapter {
	source, err := epub.DecodeXHTML(raw)
	if err != nil {
		// One unreadable chapter must not fail the book; the reader treats
		// a chapter it cannot decode like one it cannot parse.
		return renderedChapter{}
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
		case html.TextNode:
			if strings.TrimSpace(child.Data) != "" {
				t.seenText = true
			}
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
		html:           out.String(),
		labelID:        t.headingID,
		heading:        t.headingText,
		leadingHeading: t.leadingHeading,
		docTitle:       docTitle,
		fallbackID:     fallbackID,
		ids:            ids,
		authorIDs:      t.authorIDs,
		idOrder:        t.idOrder,
		targets:        t.targets,
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
		Attr: []html.Attribute{
			{Key: "id", Val: labelID},
			{Key: "class", Val: "reader-generated-heading"},
			{Key: "aria-hidden", Val: "true"},
		},
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
