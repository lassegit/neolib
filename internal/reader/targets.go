package reader

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// contentTarget is a heading-like element in reading order. These targets
// let us recover an obviously broken navigation link without moving or
// changing any of the book's text.
type contentTarget struct {
	title       string
	id          string
	section     int
	order       int
	chapterPair bool // a numbered title split across two adjacent paragraphs
}

// recordTarget records actual headings and short, wholly bold paragraphs.
// The latter are common in PDF conversions that lost both heading tags and
// navigation anchors. Assigning an id changes neither text nor DOM order.
func (t *transformer) recordTarget(n *html.Node) {
	if !isHeading(n) && (n.DataAtom != atom.P || !whollyBold(n)) {
		return
	}
	text := normalizeTitle(textContent(n))
	if text == "" || utf8.RuneCountInString(text) > 160 {
		return
	}
	// A printed contents page can consist of wholly bold links. Dot leaders
	// distinguish those from lost headings; treating them as navigation
	// targets would send an early TOC entry to another TOC entry.
	if dotLeaders.MatchString(text) || ellipsisLeaders.MatchString(text) {
		return
	}
	id := t.targetID(n)
	t.targets = append(t.targets, contentTarget{title: text, id: id, order: t.order})

	if n.DataAtom != atom.P {
		return
	}
	prev := n.PrevSibling
	for prev != nil && prev.Type != html.ElementNode {
		if prev.Type == html.TextNode && strings.TrimSpace(prev.Data) != "" {
			return
		}
		prev = prev.PrevSibling
	}
	if prev == nil || prev.DataAtom != atom.P || !whollyBold(prev) {
		return
	}
	previous := normalizeTitle(textContent(prev))
	if previous == "" {
		return
	}
	value, err := strconv.Atoi(previous)
	if len(previous) <= 3 && err == nil && value >= 1 && strconv.Itoa(value) == previous {
		// A title page in a converted PDF often reads <p>2</p><p>Names</p>,
		// while its TOC calls the same location "Chapter 2: Names".
		t.targets = append(t.targets, contentTarget{
			title:       fmt.Sprintf("Chapter %s: %s", previous, text),
			id:          t.targetID(prev),
			order:       t.nodeOrder[prev],
			chapterPair: true,
		})
		return
	}
	// A PDF line break can split a section title into two bold paragraphs.
	// Record their combination as another exact-match candidate.
	if err != nil && utf8.RuneCountInString(previous+text) <= 160 {
		t.targets = append(t.targets, contentTarget{
			title: previous + " " + text, id: t.targetID(prev), order: t.nodeOrder[prev],
		})
	}
}

func (t *transformer) targetID(n *html.Node) string {
	if id := attrValue(n, "id"); id != "" {
		return id
	}
	id := t.uniqueID(t.sectionID + "-nav")
	setAttr(n, "id", id)
	return id
}

// whollyBold avoids treating a bold word inside an ordinary paragraph as a
// heading. Links and other wrappers around the bold element are tolerated.
// The equivalent predicate in internal/epub exists separately to avoid an
// import cycle (reader imports epub).
func whollyBold(n *html.Node) bool {
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

// Some converted books use empty NBSP-only divs as short spacers. Without
// their original stylesheets each one otherwise occupies a full text line.
// Keep the original nodes and text for stable locators.
func isSpacer(n *html.Node) bool {
	if n.DataAtom != atom.Div {
		return false
	}
	text := false
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.TextNode || strings.TrimSpace(child.Data) != "" {
			return false
		}
		text = text || child.Data != ""
	}
	return text
}
