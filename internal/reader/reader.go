// Package reader turns an EPUB's spine into a single scrollable HTML
// document for the browser. Chapter HTML is sanitized and every reference
// (anchors, images, styles) is rewritten so that the book stays intact when
// its chapters are concatenated into one page.
package reader

import (
	"bytes"
	"encoding/xml"
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
}

// Chapter is one spine document with sanitized, reference-preserving HTML.
type Chapter struct {
	ID      string // section id, e.g. "c3"
	Title   string // navigation title, first heading, or "Chapter N"
	Href    string // archive path of the source document
	LabelID string // id of the element that labels the section
	HTML    string // sanitized HTML fragment
	Linear  bool   // false for spine items marked linear="no"
	Err     string // non-empty when the chapter could not be read
}

// Build reads the publication's spine and returns the whole book as one
// document. Per-chapter failures are reported on the chapter instead of
// failing the book, because the remaining chapters are still readable.
func Build(pub *epub.Publication, bookID string) (Document, error) {
	spine := pub.Spine()
	if len(spine) == 0 {
		return Document{}, nil
	}

	titles := navigationTitles(pub)
	bookTitle := pub.Title()
	byPath := make(map[string]int, len(spine))
	for i, ch := range spine {
		byPath[ch.Href] = i
	}

	doc := Document{Chapters: make([]Chapter, 0, len(spine))}
	for i, ch := range spine {
		out := Chapter{
			ID:     fmt.Sprintf("c%d", i),
			Href:   ch.Href,
			Title:  titles[ch.Href],
			Linear: ch.Linear,
		}

		raw, err := pub.Read(ch.Href, maxChapterBytes)
		if err != nil {
			out.Err = "This chapter could not be read."
		} else {
			frag, labelID, heading, docTitle := renderChapter(pub, bookID, raw, i, ch.Href, byPath)
			out.HTML = frag
			out.LabelID = labelID
			if out.Title == "" {
				out.Title = heading
			}
			if out.Title == "" && docTitle != "" && !strings.EqualFold(docTitle, bookTitle) {
				out.Title = docTitle
			}
			// Every section needs a label for assistive technology and a
			// visible heading when the chapter does not provide one of its own.
			if out.LabelID == "" && out.HTML != "" {
				if out.Title == "" {
					out.Title = fmt.Sprintf("Chapter %d", i+1)
				}
				out.HTML, out.LabelID = headingThenBody(out.HTML, out.ID, out.Title)
			}
		}
		if out.Title == "" {
			out.Title = fmt.Sprintf("Chapter %d", i+1)
		}
		doc.Chapters = append(doc.Chapters, out)
	}
	return doc, nil
}

// renderChapter parses one content document, sanitizes it, rewrites its
// references, and returns the body fragment, the id of its labelling element,
// its first heading text, and its document title.
func renderChapter(pub *epub.Publication, bookID string, raw []byte, index int, href string, spine map[string]int) (string, string, string, string) {
	source, err := charset.NewReader(bytes.NewReader(raw), "text/html")
	if err != nil {
		source = bytes.NewReader(raw)
	}
	doc, err := html.Parse(source)
	if err != nil {
		return "", "", "", ""
	}
	body := findBody(doc)
	if body == nil {
		return "", "", "", ""
	}
	docTitle := documentTitle(doc)

	t := &transformer{
		pub:       pub,
		bookID:    bookID,
		sectionID: fmt.Sprintf("c%d", index),
		href:      href,
		baseDir:   path.Dir(href),
		spine:     spine,
	}

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

	var out bytes.Buffer
	// A link may target the chapter's body element itself; keep that anchor
	// addressable even though the body wrapper is not emitted.
	if id := bodyID(body); id != "" {
		anchor := &html.Node{
			Type:     html.ElementNode,
			Data:     "span",
			DataAtom: atom.Span,
			Attr:     []html.Attribute{{Key: "id", Val: prefixID(t.sectionID, id)}},
		}
		html.Render(&out, anchor)
	}
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		html.Render(&out, child)
	}
	return out.String(), t.headingID, t.headingText, docTitle
}

// bodyID returns the id of a body element, checking both id spellings.
func bodyID(body *html.Node) string {
	if id := attrValue(body, "id"); id != "" {
		return id
	}
	return attrValue(body, "xml:id")
}

// headingThenBody prepends a generated chapter heading to a fragment and
// returns the heading's id as the section label.
func headingThenBody(body, sectionID, title string) (string, string) {
	heading := &html.Node{
		Type:     html.ElementNode,
		Data:     "h2",
		DataAtom: atom.H2,
		Attr:     []html.Attribute{{Key: "id", Val: sectionID + "-title"}},
	}
	heading.AppendChild(&html.Node{Type: html.TextNode, Data: title})

	var out bytes.Buffer
	html.Render(&out, heading)
	out.WriteString(body)
	return out.String(), sectionID + "-title"
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

// navigationTitles maps archive paths to the first title found for them in
// the EPUB 3 navigation document or the EPUB 2 NCX.
func navigationTitles(pub *epub.Publication) map[string]string {
	titles := make(map[string]string)

	if navPath := pub.NavPath(); navPath != "" {
		if data, err := pub.Read(navPath, maxNavBytes); err == nil {
			collectNavTitles(data, path.Dir(navPath), pub, titles)
		}
	}
	if ncxPath := pub.NCXPath(); ncxPath != "" {
		if data, err := pub.Read(ncxPath, maxNavBytes); err == nil {
			collectNCXTitles(data, path.Dir(ncxPath), pub, titles)
		}
	}
	return titles
}

// collectNavTitles reads an EPUB 3 navigation document and records the first
// title for every target document.
func collectNavTitles(data []byte, baseDir string, pub *epub.Publication, titles map[string]string) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return
	}
	nav := findTOCNav(doc)
	if nav == nil {
		return
	}

	var walkList func(*html.Node)
	walkList = func(list *html.Node) {
		for item := list.FirstChild; item != nil; item = item.NextSibling {
			if item.Type != html.ElementNode || item.DataAtom != atom.Li {
				continue
			}
			for child := item.FirstChild; child != nil; child = child.NextSibling {
				if child.Type != html.ElementNode {
					continue
				}
				if child.DataAtom == atom.A || child.DataAtom == atom.Span {
					if href := attrValue(child, "href"); href != "" {
						recordTitle(titles, pub, baseDir, href, textContent(child))
					}
					break
				}
				if child.DataAtom == atom.Ol || child.DataAtom == atom.Ul {
					break
				}
			}
			for child := item.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && (child.DataAtom == atom.Ol || child.DataAtom == atom.Ul) {
					walkList(child)
				}
			}
		}
	}
	for child := nav.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (child.DataAtom == atom.Ol || child.DataAtom == atom.Ul) {
			walkList(child)
			break
		}
	}
}

// findTOCNav returns the navigation element marked as the table of contents,
// falling back to the first nav element.
func findTOCNav(root *html.Node) *html.Node {
	var first, toc *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Nav {
			if first == nil {
				first = n
			}
			for _, attr := range n.Attr {
				key := strings.ToLower(attr.Key)
				if key != "epub:type" && key != "type" && key != "role" {
					continue
				}
				if strings.Contains(strings.ToLower(attr.Val), "toc") {
					toc = n
					return
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if toc != nil {
		return toc
	}
	return first
}

// ncxDocument is the subset of an NCX document needed for chapter titles.
type ncxDocument struct {
	NavMap struct {
		Points []ncxPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

type ncxPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []ncxPoint `xml:"navPoint"`
}

// collectNCXTitles reads an EPUB 2 NCX document and records the first title
// for every target document.
func collectNCXTitles(data []byte, baseDir string, pub *epub.Publication, titles map[string]string) {
	var doc ncxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return
	}
	var walk func([]ncxPoint)
	walk = func(points []ncxPoint) {
		for _, point := range points {
			recordTitle(titles, pub, baseDir, point.Content.Src, point.Label.Text)
			walk(point.Points)
		}
	}
	walk(doc.NavMap.Points)
}

// recordTitle stores the first non-empty title for a navigation target.
func recordTitle(titles map[string]string, pub *epub.Publication, baseDir, href, label string) {
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		return
	}
	href = stripFragment(href)
	if href == "" {
		return
	}
	target := pub.ResolvePath(path.Join(baseDir, href))
	if target == "" {
		return
	}
	if _, ok := titles[target]; !ok {
		titles[target] = label
	}
}
