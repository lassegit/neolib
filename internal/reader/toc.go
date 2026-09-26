package reader

import (
	"bytes"
	"encoding/xml"
	"net/url"
	"path"
	"strconv"
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

// readNavigation returns the publication's preferred table of contents and
// the chapter titles from both navigation sources. The EPUB 3 navigation
// document takes precedence; the EPUB 2 NCX supplies titles for paths it
// does not cover and is used on its own when the navigation document is
// missing or empty.
func readNavigation(pub *epub.Publication) (items []navItem, baseDir string, titles map[string]string) {
	navItems, navBase := readNavItems(pub, pub.NavPath(), parseNavDocument)
	ncxItems, ncxBase := readNavItems(pub, pub.NCXPath(), parseNCXDocument)

	titles = make(map[string]string)
	mergeTitles(titles, navTitles(navItems, navBase, pub))
	mergeTitles(titles, navTitles(ncxItems, ncxBase, pub))

	if usableNavigation(navItems) {
		return navItems, navBase, titles
	}
	return ncxItems, ncxBase, titles
}

// readNavItems reads and parses one navigation document, returning no items
// when it is missing or unreadable.
func readNavItems(pub *epub.Publication, name string, parse func([]byte) []navItem) ([]navItem, string) {
	if name == "" {
		return nil, ""
	}
	data, err := pub.Read(name, maxNavBytes)
	if err != nil {
		return nil, ""
	}
	return parse(data), path.Dir(name)
}

// mergeTitles adds titles for targets that are not covered yet.
func mergeTitles(titles, fill map[string]string) {
	for target, title := range fill {
		if _, exists := titles[target]; !exists {
			titles[target] = title
		}
	}
}

// usableNavigation reports whether parsed entries contain anything that can
// become a table of contents entry. Items without a title or href are
// ignored, so markup that parses but carries no navigation does not mask a
// usable fallback document.
func usableNavigation(items []navItem) bool {
	for _, item := range items {
		if strings.TrimSpace(item.title) != "" || strings.TrimSpace(item.href) != "" {
			return true
		}
		if usableNavigation(item.children) {
			return true
		}
	}
	return false
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

// fillTOCGaps adds a chapter-level entry for every section the navigation
// does not reach. Navigation documents are not required to be complete, so
// chapters must stay reachable from the table of contents.
func fillTOCGaps(entries []TOCEntry, sections []section) []TOCEntry {
	if len(sections) == 0 {
		return entries
	}
	covered := make([]bool, len(sections))
	var mark func([]TOCEntry)
	mark = func(entries []TOCEntry) {
		for _, entry := range entries {
			if index, ok := anchorSection(entry.Href, len(sections)); ok {
				covered[index] = true
			}
			mark(entry.Children)
		}
	}
	mark(entries)

	for i, s := range sections {
		if covered[i] {
			continue
		}
		entries = insertTOCEntry(entries, TOCEntry{Title: s.title, Href: "#" + s.id}, i, len(sections))
	}
	return entries
}

// insertTOCEntry places an entry in reading order. It descends into the
// deepest entry whose subtree spans the section, so a chapter that its part
// only partially covers stays with that part instead of moving to the end.
func insertTOCEntry(entries []TOCEntry, entry TOCEntry, section, sectionCount int) []TOCEntry {
	for i := range entries {
		if containsSection(entries[i], section, sectionCount) {
			entries[i].Children = insertTOCEntry(entries[i].Children, entry, section, sectionCount)
			return entries
		}
	}

	inserted := false
	out := make([]TOCEntry, 0, len(entries)+1)
	for _, existing := range entries {
		if !inserted {
			if first := entrySection(existing, sectionCount); first > section {
				out = append(out, entry)
				inserted = true
			}
		}
		out = append(out, existing)
	}
	if !inserted {
		out = append(out, entry)
	}
	return out
}

// containsSection reports whether an entry or one of its descendants links to
// sections on both sides of the given one.
func containsSection(entry TOCEntry, section, sectionCount int) bool {
	first := entrySection(entry, sectionCount)
	if first < 0 || first > section {
		return false
	}
	return entryMaxSection(entry, sectionCount) >= section
}

// entryMaxSection returns the last section an entry or its descendants link
// to, or -1 when the entry contains no in-page link.
func entryMaxSection(entry TOCEntry, sectionCount int) int {
	last := -1
	if index, ok := anchorSection(entry.Href, sectionCount); ok {
		last = index
	}
	for _, child := range entry.Children {
		if index := entryMaxSection(child, sectionCount); index > last {
			last = index
		}
	}
	return last
}

// entrySection returns the first section an entry or its descendants link
// to, or -1 when the entry contains no in-page link.
func entrySection(entry TOCEntry, sectionCount int) int {
	first := -1
	if index, ok := anchorSection(entry.Href, sectionCount); ok {
		first = index
	}
	for _, child := range entry.Children {
		if index := entrySection(child, sectionCount); index >= 0 && (first < 0 || index < first) {
			first = index
		}
	}
	return first
}

// anchorSection parses the section index from an in-page anchor such as
// "#c3" or "#c3-heading".
func anchorSection(href string, sectionCount int) (int, bool) {
	if len(href) < 3 || href[0] != '#' || href[1] != 'c' {
		return 0, false
	}
	end := 2
	for end < len(href) && href[end] >= '0' && href[end] <= '9' {
		end++
	}
	if end == 2 || (end < len(href) && href[end] != '-') {
		return 0, false
	}
	index, err := strconv.Atoi(href[2:end])
	if err != nil || index < 0 || index >= sectionCount {
		return 0, false
	}
	return index, true
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
