package reader

import (
	"bytes"
	"encoding/xml"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"

	"github.com/lassegit/neolib/internal/epub"
)

// TOCEntry is one item of the generated table of contents. Href is an
// in-page anchor ("#c3" or "#c3-heading"); it is empty for a group label
// that has no target of its own.
type TOCEntry struct {
	Title    string
	Href     string
	Children []TOCEntry
}

// navItem is one entry parsed from the publication's navigation document or
// NCX, before it is resolved against the rendered spine.
type navItem struct {
	title    string
	href     string
	children []navItem
}

// maxTOCDepth caps navigation nesting. Real publications rarely exceed three
// or four levels, and deeply nested markup must not exhaust the stack while
// the table of contents is parsed and rendered.
const maxTOCDepth = 10

// readNavigation returns the table of contents stored in the publication:
// the EPUB 3 navigation document when it yields entries, otherwise the
// EPUB 2 NCX. baseDir is the archive directory of the document, used to
// resolve relative hrefs.
func readNavigation(pub *epub.Publication) (items []navItem, baseDir string) {
	if navPath := pub.NavPath(); navPath != "" {
		if data, err := pub.Read(navPath, maxNavBytes); err == nil {
			if parsed := parseNavDocument(data); len(parsed) > 0 {
				return parsed, path.Dir(navPath)
			}
		}
	}
	if ncxPath := pub.NCXPath(); ncxPath != "" {
		if data, err := pub.Read(ncxPath, maxNavBytes); err == nil {
			if parsed := parseNCXDocument(data); len(parsed) > 0 {
				return parsed, path.Dir(ncxPath)
			}
		}
	}
	return nil, ""
}

// parseNavDocument reads an EPUB 3 navigation document. Only the list of a
// nav element marked as the table of contents is used, falling back to the
// first nav element.
func parseNavDocument(data []byte) []navItem {
	source, err := charset.NewReader(bytes.NewReader(data), "text/html")
	if err != nil {
		source = bytes.NewReader(data)
	}
	doc, err := html.Parse(source)
	if err != nil {
		return nil
	}
	nav := findTOCNav(doc)
	if nav == nil {
		return nil
	}
	for child := nav.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (child.DataAtom == atom.Ol || child.DataAtom == atom.Ul) {
			return parseNavList(child, 0)
		}
	}
	return nil
}

// parseNavList reads one ol/ul of an EPUB 3 navigation document. Labels are
// the first link or span of an item; nested lists become children. Wrapper
// elements between the item and its label (or list) are tolerated.
func parseNavList(list *html.Node, depth int) []navItem {
	var items []navItem
	for item := list.FirstChild; item != nil; item = item.NextSibling {
		if item.Type != html.ElementNode || item.DataAtom != atom.Li {
			continue
		}
		var (
			entry navItem
			label *html.Node
			lists []*html.Node
		)
		var scan func(*html.Node)
		scan = func(n *html.Node) {
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				if child.Type != html.ElementNode {
					continue
				}
				switch child.DataAtom {
				case atom.Ol, atom.Ul:
					lists = append(lists, child)
				case atom.A, atom.Span:
					if label == nil {
						label = child
					}
				default:
					scan(child)
				}
			}
		}
		scan(item)

		if label != nil {
			entry.title = textContent(label)
			entry.href = attrValue(label, "href")
		}
		if depth < maxTOCDepth {
			for _, nested := range lists {
				entry.children = append(entry.children, parseNavList(nested, depth+1)...)
			}
		}
		items = append(items, entry)
	}
	return items
}

// findTOCNav returns the navigation element marked as the table of contents,
// falling back to the first nav element.
func findTOCNav(root *html.Node) *html.Node {
	var first, toc *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if toc != nil {
			return
		}
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

// ncxDocument is the subset of an NCX document needed for the contents.
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

// parseNCXDocument reads an EPUB 2 NCX document.
func parseNCXDocument(data []byte) []navItem {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = charset.NewReaderLabel

	var doc ncxDocument
	if err := decoder.Decode(&doc); err != nil {
		return nil
	}

	var convert func([]ncxPoint, int) []navItem
	convert = func(points []ncxPoint, depth int) []navItem {
		items := make([]navItem, 0, len(points))
		for _, point := range points {
			item := navItem{
				title: point.Label.Text,
				href:  point.Content.Src,
			}
			if depth < maxTOCDepth {
				item.children = convert(point.Points, depth+1)
			}
			items = append(items, item)
		}
		return items
	}
	return convert(doc.NavMap.Points, 0)
}

// resolveNavHref splits a navigation href into the archive path it targets
// and its fragment. It reports false for missing and external hrefs.
func resolveNavHref(baseDir, raw string) (target, fragment string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	if u.Scheme != "" || u.Host != "" {
		return "", "", false
	}
	if u.Path == "" {
		return "", "", false
	}
	if strings.HasPrefix(u.Path, "/") {
		target = strings.TrimLeft(u.Path, "/")
	} else {
		target = path.Join(baseDir, u.Path)
	}
	return target, u.Fragment, true
}

// navTitles maps archive paths to the first title the navigation assigns to
// them. It titles chapters and the headings generated for them.
func navTitles(items []navItem, baseDir string, pub *epub.Publication) map[string]string {
	titles := make(map[string]string)
	var walk func([]navItem)
	walk = func(items []navItem) {
		for _, item := range items {
			title := normalizeTitle(item.title)
			if title != "" {
				if target, _, ok := resolveNavHref(baseDir, item.href); ok {
					if resolved := pub.ResolvePath(target); resolved != "" {
						if _, exists := titles[resolved]; !exists {
							titles[resolved] = title
						}
					}
				}
			}
			walk(item.children)
		}
	}
	walk(items)
	return titles
}

// section is the rendered state of one spine document, needed to map
// navigation entries onto in-page anchors.
type section struct {
	id    string          // rendered section id, e.g. "c3"
	ids   map[string]bool // ids present in the rendered fragment
	title string          // title used when navigation has no label
}

// buildTOC resolves navigation items against the rendered chapters. An item
// whose target is not part of the reading order is skipped, but its children
// are kept so no headline is lost.
func buildTOC(items []navItem, baseDir string, pub *epub.Publication, spine map[string]int, sections []section) []TOCEntry {
	var entries []TOCEntry
	for _, item := range items {
		children := buildTOC(item.children, baseDir, pub, spine, sections)
		entry, ok := resolveNavItem(item, children, baseDir, pub, spine, sections)
		if !ok {
			entries = append(entries, children...)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func resolveNavItem(item navItem, children []TOCEntry, baseDir string, pub *epub.Publication, spine map[string]int, sections []section) (TOCEntry, bool) {
	title := normalizeTitle(item.title)

	if strings.TrimSpace(item.href) == "" {
		// A label without a target is a grouping heading; keep it when it has
		// a title so the hierarchy stays visible.
		if title == "" {
			return TOCEntry{}, false
		}
		return TOCEntry{Title: title, Children: children}, true
	}
	target, fragment, ok := resolveNavHref(baseDir, item.href)
	if !ok {
		return TOCEntry{}, false
	}
	index, ok := spine[pub.ResolvePath(target)]
	if !ok || index >= len(sections) {
		return TOCEntry{}, false
	}
	if title == "" {
		title = sections[index].title
	}
	return TOCEntry{
		Title:    title,
		Href:     sectionHref(sections[index], fragment),
		Children: children,
	}, true
}

// sectionHref links a navigation fragment to the rendered anchor, falling
// back to the top of the section when the target is missing.
func sectionHref(s section, fragment string) string {
	if fragment != "" {
		if id := prefixID(s.id, fragment); id != "" && s.ids[id] {
			return "#" + id
		}
	}
	return "#" + s.id
}

// fallbackTOC lists every chapter when the publication has no usable
// navigation document.
func fallbackTOC(chapters []Chapter) []TOCEntry {
	entries := make([]TOCEntry, 0, len(chapters))
	for _, chapter := range chapters {
		entries = append(entries, TOCEntry{Title: chapter.Title, Href: "#" + chapter.ID})
	}
	return entries
}

// collectIDs records every id in a rendered fragment, so navigation
// fragments can be validated before they are turned into links. Legacy
// named anchors are included because fragment navigation reaches them too.
func collectIDs(root *html.Node) map[string]bool {
	ids := make(map[string]bool)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if id := attrValue(child, "id"); id != "" {
				ids[id] = true
			}
			if child.DataAtom == atom.A || child.DataAtom == atom.Map {
				if name := attrValue(child, "name"); name != "" {
					ids[name] = true
				}
			}
			walk(child)
		}
	}
	walk(root)
	return ids
}

func normalizeTitle(title string) string {
	return strings.Join(strings.Fields(title), " ")
}
