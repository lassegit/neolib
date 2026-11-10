package reader

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/lassegit/neolib/internal/epub"
)

var (
	dotLeaders       = regexp.MustCompile(`(?i)(?:\.\s*){4,}(?:[0-9ivxlcdm]+)?\s*$`)
	ellipsisLeaders  = regexp.MustCompile(`(?i)(?:…\s*){2,}(?:[0-9ivxlcdm]+)?\s*$`)
	titlePunctuation = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`, "–", "-", "—", "-")
)

// Converted print TOCs sometimes include dot leaders and page numbers in
// the navigation label itself. They are not part of the section title.
func navigationTitle(s string) string {
	s = normalizeTitle(s)
	if cleaned := strings.TrimSpace(dotLeaders.ReplaceAllString(s, "")); cleaned != "" {
		s = cleaned
	}
	if cleaned := strings.TrimSpace(ellipsisLeaders.ReplaceAllString(s, "")); cleaned != "" {
		s = cleaned
	}
	return s
}

func titleKey(s string) string {
	return strings.ToLower(titlePunctuation.Replace(normalizeTitle(s)))
}

// PDF extraction sometimes attaches a footnote number to a heading. Only
// use this alias for systematically broken navigation and an exact match.
func withoutAttachedFootnote(s string) string {
	runes := []rune(s)
	end := len(runes)
	for end > 0 && runes[end-1] >= '0' && runes[end-1] <= '9' {
		end--
	}
	if end == len(runes) || len(runes)-end > 2 || end == 0 || !unicode.IsLetter(runes[end-1]) {
		return ""
	}
	return string(runes[:end])
}

// Some PDF conversions append a hierarchical document outline to the spine,
// while generating a truncated and broken EPUB navigation document. Treat
// that outline as a navigation candidate, not as an authoritative TOC: it is
// used only when its links can be recovered better than the supplied nav.
func parseDocumentOutline(raw []byte) []navItem {
	source, err := epub.DecodeXHTML(raw)
	if err != nil {
		return nil
	}
	doc, err := html.Parse(source)
	if err != nil {
		return nil
	}
	var list *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if list != nil {
			return
		}
		if isHeading(n) && strings.EqualFold(normalizeTitle(textContent(n)), "Document Outline") {
			for next := n.NextSibling; next != nil; next = next.NextSibling {
				if next.Type != html.ElementNode {
					continue
				}
				if next.DataAtom == atom.Ol || next.DataAtom == atom.Ul {
					list = next
				}
				break
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if list == nil {
		return nil
	}
	return parseNavList(list, 0)
}

func hasAuthorFragment(s section, fragment string) bool {
	id := prefixID(s.id, fragment)
	return id != "" && s.authorIDs[id] && s.ids[id]
}

// validNavTitles ignores labels whose fragments do not actually exist. In
// particular, a long list of broken links into one file must not name that
// entire file after its first, unrelated navigation item.
func validNavTitles(source navigationSource, pub *epub.Publication, spine map[string]int, sections []section) map[string]string {
	titles := make(map[string]string)
	var walk func([]navItem)
	walk = func(items []navItem) {
		for _, item := range items {
			target, fragment, ok := resolveNavHref(source.base, item.href)
			if ok {
				target = pub.ResolvePath(target)
				index, exists := spine[target]
				if exists && index < len(sections) && (fragment == "" || hasAuthorFragment(sections[index], fragment)) {
					if title := navigationTitle(item.title); title != "" && titles[target] == "" {
						titles[target] = title
					}
				}
			}
			walk(item.children)
		}
	}
	walk(source.items)
	return titles
}

type tocResolver struct {
	pub        *epub.Publication
	spine      map[string]int
	sections   []section
	base       string
	targets    map[string][]contentTarget
	footnotes  map[string][]contentTarget
	repairs    map[string][]linkRepair
	boundaries []contentTarget
	broken     bool
	cursor     contentTarget
}

func newTOCResolver(source navigationSource, pub *epub.Publication, spine map[string]int, sections []section, targets []contentTarget) *tocResolver {
	r := &tocResolver{
		pub: pub, spine: spine, sections: sections, base: source.base,
		targets:   make(map[string][]contentTarget),
		footnotes: make(map[string][]contentTarget),
		repairs:   make(map[string][]linkRepair),
		cursor:    contentTarget{section: -1, order: -1},
	}
	// Candidate order must follow the book, not the order in which a
	// composite two-paragraph heading was discovered.
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].section != targets[j].section {
			return targets[i].section < targets[j].section
		}
		return targets[i].order < targets[j].order
	})
	for _, target := range targets {
		key := titleKey(target.title)
		r.targets[key] = append(r.targets[key], target)
		if alias := withoutAttachedFootnote(key); alias != "" {
			r.footnotes[alias] = append(r.footnotes[alias], target)
		}
	}

	var total, missing int
	chapterTitles := make(map[string]bool)
	var count func([]navItem)
	count = func(items []navItem) {
		for _, item := range items {
			key := titleKey(navigationTitle(item.title))
			if strings.HasPrefix(key, "chapter ") || strings.HasPrefix(key, "appendix ") {
				chapterTitles[key] = true
			}
			if target, fragment, ok := resolveNavHref(source.base, item.href); ok {
				total++
				index, exists := spine[pub.ResolvePath(target)]
				if !exists || index >= len(sections) || (fragment != "" && !hasAuthorFragment(sections[index], fragment)) {
					missing++
				}
			}
			count(item.children)
		}
	}
	count(source.items)
	// Only trust reading-order disambiguation when there is clear evidence
	// that the navigation was systematically damaged by a conversion.
	r.broken = missing >= 3 && missing*2 >= total
	if r.broken {
		for title := range chapterTitles {
			if matches := r.targets[title]; len(matches) > 0 {
				r.boundaries = append(r.boundaries, matches[0])
			}
		}
		sort.Slice(r.boundaries, func(i, j int) bool { return targetBefore(r.boundaries[i], r.boundaries[j]) })
	}
	return r
}

func (r *tocResolver) build(items []navItem) []TOCEntry {
	var entries []TOCEntry
	for _, item := range items {
		// Resolve the parent first: damaged flat TOCs are repaired in the
		// navigation's reading order, including nested entries.
		entry, ok := r.resolve(item)
		children := r.build(item.children)
		if !ok {
			entries = append(entries, children...)
			continue
		}
		entry.Children = children
		entries = append(entries, entry)
	}
	return entries
}

func (r *tocResolver) resolve(item navItem) (TOCEntry, bool) {
	title := navigationTitle(item.title)
	if strings.TrimSpace(item.href) == "" {
		return TOCEntry{Title: title}, title != ""
	}
	target, fragment, ok := resolveNavHref(r.base, item.href)
	if !ok {
		return TOCEntry{}, false
	}
	index, inSpine := r.spine[r.pub.ResolvePath(target)]
	if inSpine && index < len(r.sections) {
		s := r.sections[index]
		if s.continuation {
			// Running headings on split files are not separate chapters.
			// Drop their duplicate entries (children are hoisted by build),
			// including links to the hidden running-heading itself.
			if fragment == "" || (s.leadingID != "" && prefixID(s.id, fragment) == s.leadingID) {
				return TOCEntry{}, false
			}
		}
		if fragment == "" || hasAuthorFragment(s, fragment) {
			href := "#" + s.id
			order := 0
			if fragment != "" {
				id := prefixID(s.id, fragment)
				href = "#" + id
				order = s.idOrder[id]
			}
			if title == "" {
				title = s.title
			}
			r.advance(contentTarget{section: index, order: order})
			return TOCEntry{Title: title, Href: href}, true
		}
	}

	// Do not silently send a missing fragment to the beginning of a file.
	// Recover only an exact, heading-like match; otherwise omit the bad link.
	if title != "" {
		if match, ok := r.match(title, index, inSpine); ok {
			r.advance(match)
			href := "#" + match.id
			if inSpine && index < len(r.sections) && fragment != "" {
				old := "#" + prefixID(r.sections[index].id, fragment)
				r.repairs[old] = append(r.repairs[old], linkRepair{title: titleKey(title), href: href})
			}
			return TOCEntry{Title: title, Href: href}, true
		}
	}
	return TOCEntry{}, false
}

func targetBefore(a, b contentTarget) bool {
	return a.section < b.section || (a.section == b.section && a.order < b.order)
}

func (r *tocResolver) advance(target contentTarget) {
	if targetBefore(r.cursor, target) {
		r.cursor = target
	}
}

func (r *tocResolver) match(title string, intended int, inSpine bool) (contentTarget, bool) {
	key := titleKey(title)
	matches := r.targets[key]
	if len(matches) == 0 && r.broken {
		matches = r.footnotes[key]
	}
	if r.broken {
		// A missing generic label such as "Conclusion" must not jump to a
		// similarly named heading in the next chapter. Bound the search by
		// the next chapter heading that the navigation itself identifies.
		var boundary *contentTarget
		for i := range r.boundaries {
			if targetBefore(r.cursor, r.boundaries[i]) {
				boundary = &r.boundaries[i]
				break
			}
		}
		for _, target := range matches {
			if targetBefore(target, r.cursor) {
				continue
			}
			if boundary != nil && targetBefore(*boundary, target) {
				break
			}
			return target, true
		}
		return contentTarget{}, false
	}

	// For isolated errors, do not guess among repeated labels. Prefer a
	// unique match in the declared file, then a unique match in the book.
	var local []contentTarget
	if inSpine {
		for _, target := range matches {
			if target.section == intended {
				local = append(local, target)
			}
		}
	}
	if len(local) == 1 {
		return local[0], true
	}
	if len(local) == 0 && len(matches) == 1 {
		return matches[0], true
	}
	return contentTarget{}, false
}

func tocLinkCount(entries []TOCEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Href != "" {
			count++
		}
		count += tocLinkCount(entry.Children)
	}
	return count
}

// fillTOCGaps adds real, distinct leading headings that a partial TOC omits.
// A spine resource is not necessarily a chapter: unlabelled front matter and
// continuation files must not become invented "Chapter N" entries.
func fillTOCGaps(entries []TOCEntry, sections []section, targets []contentTarget, includeUnlabelled bool) []TOCEntry {
	covered := make([]bool, len(sections))
	titles := make(map[string][]int)
	var mark func([]TOCEntry)
	mark = func(items []TOCEntry) {
		for _, item := range items {
			if index, ok := anchorSection(item.Href, len(sections)); ok {
				covered[index] = true
				key := titleKey(item.Title)
				titles[key] = append(titles[key], index)
			}
			mark(item.Children)
		}
	}
	mark(entries)

	for i, s := range sections {
		if s.continuation || s.navigationOnly || covered[i] {
			continue
		}
		if s.navTitle {
			// A secondary NCX can supply a useful title for a resource the
			// preferred navigation does not cover, even without a heading.
			entries = insertTOCEntry(entries, TOCEntry{Title: s.title, Href: "#" + s.id}, i, len(sections))
			continue
		}
		if !s.leadingHeading || s.heading == "" {
			if includeUnlabelled {
				entries = insertTOCEntry(entries, TOCEntry{Title: s.title, Href: "#" + s.id}, i, len(sections))
			}
			continue
		}
		key := titleKey(s.heading)
		alreadyListed := false
		for _, index := range titles[key] {
			if index == i || index == i-1 {
				alreadyListed = true
				break
			}
		}
		if alreadyListed {
			continue
		}

		href := "#" + s.id
		index := i
		// A running heading may start in the next split file; prefer the
		// actual numbered title immediately before it, when it is available.
		for _, target := range targets {
			if target.chapterPair && titleKey(target.title) == key && target.section >= i-1 && target.section <= i {
				href = "#" + target.id
				index = target.section
			}
		}
		entries = insertTOCEntry(entries, TOCEntry{Title: s.heading, Href: href}, index, len(sections))
		titles[key] = append(titles[key], index)
	}
	return entries
}

// markContinuations detects runs of repeated leading headings across split
// files. Three repetitions are required before hiding any of them; ordinary
// books with coincidentally similar chapter headings are left alone.
func markContinuations(chapters []Chapter, sections []section, targets []contentTarget) {
	for start := 0; start < len(sections); {
		first := sections[start]
		if !first.leadingHeading || first.heading == "" {
			start++
			continue
		}
		key := titleKey(first.heading)
		last, count, gap := start, 1, 0
		next := start + 1
		for ; next < len(sections); next++ {
			s := sections[next]
			if !s.leadingHeading || s.heading == "" {
				gap++
				if gap <= 1 {
					continue
				}
				break
			}
			if titleKey(s.heading) != key {
				break
			}
			last, count, gap = next, count+1, 0
		}
		if count >= 3 {
			firstDuplicate := start + 1
			for _, target := range targets {
				if target.chapterPair && target.section == start-1 && titleKey(target.title) == key {
					// The actual chapter title is in the preceding file; even
					// the first repeated running header interrupts its text.
					firstDuplicate = start
					break
				}
			}
			for i := firstDuplicate; i <= last; i++ {
				sections[i].continuation = true
				chapters[i].Continuation = true
				if sections[i].leadingHeading {
					chapters[i].LabelID = ""
				} else if strings.HasPrefix(chapters[i].Title, "Section ") {
					chapters[i].Title = first.heading
					sections[i].title = first.heading
				}
			}
		}
		// Revisit the first non-matching heading; a trailing unheaded file
		// may start another run and must not be skipped either.
		start = last + 1
	}
}
