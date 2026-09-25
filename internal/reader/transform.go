package reader

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/lassegit/neolib/internal/epub"
)

// transformer rewrites one chapter while walking its DOM.
type transformer struct {
	pub       *epub.Publication
	bookID    string
	sectionID string
	href      string
	baseDir   string
	spine     map[string]int
	settings  Settings

	headingID   string
	headingText string
}

// droppedElements are removed together with their content. Script-like,
// document-level, and interactive elements have no place inside the reading
// page; the rest of a chapter's markup is preserved.
var droppedElements = map[string]bool{
	"script":   true,
	"style":    true,
	"iframe":   true,
	"frame":    true,
	"frameset": true,
	"object":   true,
	"embed":    true,
	"applet":   true,
	"param":    true,
	"base":     true,
	"meta":     true,
	"link":     true,
	"title":    true,
	"template": true,
	"noscript": true,
	"form":     true,
	"button":   true,
	"input":    true,
	"select":   true,
	"textarea": true,
	"option":   true,
	"optgroup": true,
	"fieldset": true,
	"legend":   true,
	"label":    true,
	// SVG animation can rewrite attributes such as href at runtime.
	"animate":          true,
	"animatecolor":     true,
	"animatemotion":    true,
	"animatetransform": true,
	"set":              true,
}

// urlAttrs hold URLs that must be resolved and rewritten.
var urlAttrs = map[string]bool{
	"href":        true,
	"src":         true,
	"srcset":      true,
	"imagesrcset": true,
	"poster":      true,
	"background":  true,
	"data":        true,
	"longdesc":    true,
	"cite":        true,
	"xlink:href":  true,
}

// mediaURLAttrs may carry data: image URIs.
var mediaURLAttrs = map[string]bool{
	"src":        true,
	"poster":     true,
	"background": true,
	"data":       true,
	"xlink:href": true,
}

// idrefAttrs hold references to ids elsewhere in the document.
var idrefAttrs = map[string]bool{
	"for":                   true,
	"form":                  true,
	"headers":               true,
	"list":                  true,
	"aria-activedescendant": true,
	"aria-controls":         true,
	"aria-describedby":      true,
	"aria-details":          true,
	"aria-errormessage":     true,
	"aria-flowto":           true,
	"aria-labelledby":       true,
	"aria-owns":             true,
}

// element sanitizes and rewrites one element and its subtree.
func (t *transformer) element(n *html.Node) {
	if droppedElements[strings.ToLower(n.Data)] {
		removeNode(n)
		return
	}

	t.rewriteAttrs(n)

	// Chapter images that appear far down the combined page should not all
	// load at once.
	if n.DataAtom == atom.Img && attrValue(n, "loading") == "" {
		n.Attr = append(n.Attr, html.Attribute{Key: "loading", Val: "lazy"})
	}

	// External links are decorated after sanitizing, so an author-supplied
	// target can never survive.
	if t.settings.ExternalLinks == ExternalLinksNewTab && n.DataAtom == atom.A && isExternalURL(attrValue(n, "href")) {
		setAttr(n, "target", "_blank")
		addRel(n, "noopener", "noreferrer")
	}

	if t.headingID == "" && isHeading(n) {
		if text := strings.Join(strings.Fields(textContent(n)), " "); text != "" {
			t.headingText = text
			t.headingID = t.ensureID(n)
		}
	}

	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		switch child.Type {
		case html.ElementNode:
			t.element(child)
		case html.CommentNode, html.DoctypeNode:
			n.RemoveChild(child)
		}
		child = next
	}
}

// ensureID returns the element's (already prefixed) id, assigning one when
// the element has none.
func (t *transformer) ensureID(n *html.Node) string {
	if id := attrValue(n, "id"); id != "" {
		return id
	}
	id := t.sectionID + "-title"
	n.Attr = append(n.Attr, html.Attribute{Key: "id", Val: id})
	return id
}

// rewriteAttrs drops dangerous attributes and rewrites ids, id references,
// and URLs in place.
func (t *transformer) rewriteAttrs(n *html.Node) {
	data := strings.ToLower(n.Data)
	attrs := n.Attr[:0]
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		if strings.HasPrefix(key, "on") {
			continue
		}
		switch key {
		case "style", "srcdoc", "ping", "target", "http-equiv", "action", "formaction",
			"method", "enctype", "autofocus", "autoplay", "contenteditable":
			continue
		}

		switch {
		case key == "id" || key == "xml:id":
			attr.Val = t.prefixID(attr.Val)
			if attr.Val == "" {
				continue
			}
		case key == "name" && (data == "a" || data == "map"):
			attr.Val = t.prefixID(attr.Val)
			if attr.Val == "" {
				continue
			}
		case key == "usemap":
			attr.Val = t.rewriteUseMap(attr.Val)
		case idrefAttrs[key]:
			attr.Val = t.prefixIDList(attr.Val)
			if attr.Val == "" {
				continue
			}
		case urlAttrs[key]:
			attr.Val = t.rewriteURL(key, attr.Val)
			if attr.Val == "" {
				continue
			}
		}
		attrs = append(attrs, attr)
	}
	n.Attr = attrs
}

// rewriteURL validates a URL attribute and rewrites document-relative
// references so they keep working inside the concatenated page.
func (t *transformer) rewriteURL(key, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if key == "srcset" || key == "imagesrcset" {
		return t.rewriteSrcset(raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "javascript", "vbscript", "file", "blob":
		return ""
	case "data":
		if mediaURLAttrs[key] && strings.HasPrefix(strings.ToLower(raw), "data:image/") {
			return raw
		}
		return ""
	case "http", "https", "mailto", "tel":
		return raw
	case "":
		if u.Host != "" {
			return raw // protocol-relative external URL
		}
		return t.reference(u)
	default:
		return ""
	}
}

// reference rewrites a relative or fragment-only URL. Links to spine
// documents become in-page anchors; everything else is served as a resource.
func (t *transformer) reference(u *url.URL) string {
	if u.Path == "" {
		if u.Fragment == "" {
			return rawReference(u)
		}
		return "#" + prefixID(t.sectionID, u.Fragment)
	}

	target := t.resolveArchivePath(u.Path)
	if index, ok := t.spine[target]; ok {
		if u.Fragment != "" {
			return "#" + prefixID(fmt.Sprintf("c%d", index), u.Fragment)
		}
		return fmt.Sprintf("#c%d", index)
	}
	return t.resourceURL(target, u.Fragment)
}

// rawReference renders a URL that has no path, preserving query and fragment.
func rawReference(u *url.URL) string {
	var b strings.Builder
	if u.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(u.RawQuery)
	}
	if u.Fragment != "" {
		b.WriteByte('#')
		b.WriteString(url.PathEscape(u.Fragment))
	}
	if b.Len() == 0 {
		return "#"
	}
	return b.String()
}

// resolveArchivePath maps a document-relative URL path to an archive member.
func (t *transformer) resolveArchivePath(p string) string {
	if p == "" {
		return t.href
	}
	if strings.HasPrefix(p, "/") {
		return t.pub.ResolvePath(strings.TrimLeft(p, "/"))
	}
	return t.pub.ResolvePath(path.Join(t.baseDir, p))
}

// resourceURL builds the URL that serves an archive member.
func (t *transformer) resourceURL(target, fragment string) string {
	var b strings.Builder
	b.WriteString("/books/")
	b.WriteString(url.PathEscape(t.bookID))
	b.WriteString("/resource/")
	b.WriteString(escapePath(target))
	if fragment != "" {
		b.WriteByte('#')
		b.WriteString(url.PathEscape(fragment))
	}
	return b.String()
}

// rewriteSrcset rewrites every candidate URL in a srcset attribute.
func (t *transformer) rewriteSrcset(raw string) string {
	var out []string
	for _, candidate := range strings.Split(raw, ",") {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) == 0 {
			continue
		}
		rewritten := t.rewriteURL("src", fields[0])
		if rewritten == "" {
			continue
		}
		if len(fields) > 1 {
			rewritten += " " + strings.Join(fields[1:], " ")
		}
		out = append(out, rewritten)
	}
	return strings.Join(out, ", ")
}

// rewriteUseMap prefixes the id reference in a usemap attribute.
func (t *transformer) rewriteUseMap(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "#" + t.prefixID(strings.TrimPrefix(value, "#"))
}

// prefixID namespaces an id so ids from different chapters cannot collide.
func (t *transformer) prefixID(id string) string {
	return prefixID(t.sectionID, id)
}

func prefixID(section, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	id = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, id)
	return section + "-" + id
}

// prefixIDList namespaces a space-separated list of id references.
func (t *transformer) prefixIDList(value string) string {
	fields := strings.Fields(value)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if prefixed := t.prefixID(strings.TrimPrefix(field, "#")); prefixed != "" {
			out = append(out, prefixed)
		}
	}
	return strings.Join(out, " ")
}

// removeNode detaches a node from its parent.
func removeNode(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// wrapImages turns every standalone image into a link to its full-size
// resource. Images that are already links, part of an image map, or inside a
// <picture> are left untouched.
func wrapImages(root *html.Node) {
	var walk func(*html.Node)
	walk = func(parent *html.Node) {
		for child := parent.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode && child.DataAtom == atom.Img && canWrapImage(child) {
				src := attrValue(child, "src")
				link := &html.Node{
					Type:     html.ElementNode,
					Data:     "a",
					DataAtom: atom.A,
					// Opening the full-size resource in a new tab keeps the
					// reader's scroll position in the book.
					Attr: []html.Attribute{
						{Key: "href", Val: src},
						{Key: "target", Val: "_blank"},
						{Key: "rel", Val: "noopener noreferrer"},
					},
				}
				parent.InsertBefore(link, child)
				parent.RemoveChild(child)
				link.AppendChild(child)
			} else {
				walk(child)
			}
			child = next
		}
	}
	walk(root)
}

// canWrapImage reports whether an image is a standalone, linkable resource.
func canWrapImage(img *html.Node) bool {
	for ancestor := img.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor.DataAtom == atom.A || ancestor.DataAtom == atom.Picture {
			return false
		}
	}
	if attrValue(img, "usemap") != "" {
		return false
	}
	src := attrValue(img, "src")
	return src != "" && !strings.HasPrefix(strings.ToLower(src), "data:")
}

// isExternalURL reports whether href points outside the application.
func isExternalURL(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	case "":
		return u.Host != "" // protocol-relative URL
	}
	return false
}

// setAttr sets or replaces an attribute.
func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

// addRel merges tokens into an element's rel attribute.
func addRel(n *html.Node, values ...string) {
	rel := strings.Fields(attrValue(n, "rel"))
	for _, value := range values {
		found := false
		for _, existing := range rel {
			if strings.EqualFold(existing, value) {
				found = true
				break
			}
		}
		if !found {
			rel = append(rel, value)
		}
	}
	setAttr(n, "rel", strings.Join(rel, " "))
}

// isHeading reports whether n is an h1 through h6 element.
func isHeading(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	name := strings.ToLower(n.Data)
	if len(name) != 2 || name[0] != 'h' {
		return false
	}
	return name[1] >= '1' && name[1] <= '6'
}

// textContent concatenates the text nodes below n, ignoring script and
// style content.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(m *html.Node) {
		switch {
		case m.Type == html.TextNode:
			b.WriteString(m.Data)
			return
		case m.Type == html.ElementNode && (m.DataAtom == atom.Script || m.DataAtom == atom.Style):
			return
		}
		for child := m.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

// attrValue returns the value of an attribute, compared case-insensitively.
func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

// stripFragment removes a fragment and query string from an href.
func stripFragment(href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if i := strings.IndexByte(href, '?'); i >= 0 {
		href = href[:i]
	}
	return strings.TrimSpace(href)
}

// escapePath percent-encodes each segment of an archive path.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
