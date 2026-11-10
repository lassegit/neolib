package epub

import (
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// PlaceholderTitle reports metadata that is clearly not a useful book title.
// In particular, some PDF-to-EPUB conversions put a UUID in dc:title.
func PlaceholderTitle(title string) bool {
	s := strings.ToLower(strings.TrimSpace(title))
	if s == "" || s == "unknown" || s == "untitled" {
		return true
	}
	s = strings.TrimPrefix(s, "urn:uuid:")
	s = strings.TrimPrefix(s, "uuid:")
	if len(s) == 36 {
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return false
		}
		s = strings.ReplaceAll(s, "-", "")
	}
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// inferredTitle makes a deliberately conservative attempt to recover a
// missing/placeholder title from the beginning of the book. A semantic EPUB
// title or an initial h1 is unambiguous enough; for conversions that use
// only paragraphs, require the first text to be emphasized and repeated in
// the front matter. Never replace valid package metadata with this guess.
func (p *Publication) inferredTitle() string {
	const maxTitleDocuments = 4
	const maxTitleBytes = 2 << 20
	const maxBlocks = 128

	var first string
	blocks := 0
	titlePages := p.titlePagePaths()
	for i, ch := range p.Spine() {
		if i >= maxTitleDocuments || blocks >= maxBlocks {
			break
		}
		raw, err := p.Read(ch.Href, maxTitleBytes)
		if err != nil {
			continue
		}
		source, err := DecodeXHTML(raw)
		if err != nil {
			continue
		}
		doc, err := html.Parse(source)
		if err != nil {
			continue
		}
		body := titleBody(doc)
		if body == nil {
			continue
		}
		titlePage := titlePages[ch.Href]

		var found string
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if found != "" || blocks >= maxBlocks {
				return
			}
			if n.Type == html.ElementNode {
				switch n.DataAtom {
				case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Nav:
					return
				}
				if isTitleBlock(n) {
					text := titleText(n)
					if text == "" {
						return
					}
					blocks++
					if !plausibleTitle(text) {
						if first == "" {
							first = "-" // do not treat later prose as the first text
						}
						return
					}
					if semanticTitle(n) || (titlePage && n.DataAtom == atom.H1) {
						found = text
						return
					}
					if first == "" {
						if emphasizedTitle(n) {
							first = text
						} else {
							first = "-"
						}
					} else if first != "-" && strings.EqualFold(first, text) && emphasizedTitle(n) {
						found = first
					}
					return
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(body)
		if found != "" {
			return found
		}
	}
	return ""
}

// titlePagePaths identifies documents the package explicitly marks as the
// title page. H1 recovery is only safe there; a first chapter heading must
// not become the book's title merely because the metadata is missing.
func (p *Publication) titlePagePaths() map[string]bool {
	paths := make(map[string]bool)
	for _, ref := range p.pkg.Guide.References {
		if ref.Href != "" && strings.Contains(strings.ToLower(ref.Type), "title") {
			paths[p.resolveManifestHref(ref.Href)] = true
		}
	}
	for _, item := range p.pkg.Manifest.Items {
		for _, prop := range strings.Fields(item.Properties) {
			if strings.Contains(strings.ToLower(prop), "title-page") {
				paths[p.resolveManifestHref(item.Href)] = true
			}
		}
	}
	return paths
}

// titleBody is kept local because internal/reader imports internal/epub;
// importing the reader's DOM helpers back into this package would create a
// dependency cycle.
func titleBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if body := titleBody(child); body != nil {
			return body
		}
	}
	return nil
}

func isTitleBlock(n *html.Node) bool {
	return n.DataAtom == atom.P || n.DataAtom == atom.H1 || n.DataAtom == atom.H2 || semanticTitle(n)
}

func semanticTitle(n *html.Node) bool {
	for _, attr := range n.Attr {
		if attr.Key != "epub:type" && attr.Key != "type" {
			continue
		}
		for _, value := range strings.Fields(attr.Val) {
			if value == "title" || value == "fulltitle" {
				return true
			}
		}
	}
	return false
}

func titleText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func plausibleTitle(s string) bool {
	if PlaceholderTitle(s) || utf8.RuneCountInString(s) < 2 || utf8.RuneCountInString(s) > 120 {
		return false
	}
	lower := strings.ToLower(s)
	for _, prefix := range []string{"chapter ", "contents", "table of contents", "cover", "foreword", "preface", "introduction"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

// emphasizedTitle requires the entire text of the block to be in a bold
// element, rather than merely containing a bold word in a paragraph. It is
// intentionally parallel to reader.whollyBold: reader imports this package,
// so sharing that helper here would introduce an import cycle.
func emphasizedTitle(n *html.Node) bool {
	for n != nil {
		if n.DataAtom == atom.B || n.DataAtom == atom.Strong {
			return true
		}
		var only *html.Node
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode && strings.TrimSpace(child.Data) == "" {
				continue
			}
			if child.Type != html.ElementNode || only != nil {
				return false
			}
			only = child
		}
		n = only
	}
	return false
}
