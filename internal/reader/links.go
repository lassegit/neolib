package reader

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type linkRepair struct {
	title string
	href  string
}

// mergeLinkRepairs unions repairs from every navigation source that was
// attempted. Falling back to the NCX or document outline must not discard
// fixes already found for links in the book's printed contents.
func mergeLinkRepairs(dst, src map[string][]linkRepair) {
	for href, choices := range src {
		for _, choice := range choices {
			found := false
			for _, existing := range dst[href] {
				if existing.title == choice.title && existing.href == choice.href {
					found = true
					break
				}
			}
			if !found {
				dst[href] = append(dst[href], choice)
			}
		}
	}
}

// repairContentLinks applies the same recovered destinations to links in
// the book's own printed TOC. If several labels share a broken page anchor,
// require the label to match; never guess which destination was intended.
// Only attributes are changed, so the content DOM and locator offsets stay
// in their original order.
func repairContentLinks(fragment string, repairs map[string][]linkRepair) string {
	if len(repairs) == 0 || !strings.Contains(fragment, `href="#c`) {
		return fragment
	}
	context := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return fragment
	}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	changed := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.A {
			if choices := repairs[attrValue(n, "href")]; len(choices) > 0 {
				label := titleKey(navigationTitle(textContent(n)))
				destination := ""
				matched, ambiguous := false, false
				for _, choice := range choices {
					if choice.title != label {
						continue
					}
					if matched && destination != choice.href {
						ambiguous = true
					}
					matched = true
					destination = choice.href
				}
				if ambiguous {
					destination = ""
				} else if !matched {
					destination = choices[0].href
					for _, choice := range choices[1:] {
						if choice.href != destination {
							destination = ""
							break
						}
					}
				}
				if destination != "" {
					setAttr(n, "href", destination)
					changed = true
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if !changed {
		return fragment
	}
	var out bytes.Buffer
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&out, child); err != nil {
			return fragment
		}
	}
	return out.String()
}
