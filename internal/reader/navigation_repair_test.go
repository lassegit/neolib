package reader_test

import (
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/reader"
)

const packageXMLRepair = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Repair Book</dc:title></metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="chap1.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="chap2.xhtml" media-type="application/xhtml+xml"/>
<item id="c3" href="chap3.xhtml" media-type="application/xhtml+xml"/>
<item id="c4" href="chap4.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/><itemref idref="c4"/></spine>
</package>`

func assertTOCAnchorsExist(t *testing.T, doc reader.Document) {
	t.Helper()
	byID := make(map[string]reader.Chapter, len(doc.Chapters))
	for _, chapter := range doc.Chapters {
		byID[chapter.ID] = chapter
	}
	var walk func([]reader.TOCEntry)
	walk = func(entries []reader.TOCEntry) {
		for _, entry := range entries {
			if entry.Href != "" {
				id := strings.TrimPrefix(entry.Href, "#")
				section := id
				if dash := strings.IndexByte(id, '-'); dash >= 0 {
					section = id[:dash]
				}
				chapter, ok := byID[section]
				if !ok {
					t.Errorf("toc entry %q points at unknown section %q", entry.Title, entry.Href)
				} else if section != id && !strings.Contains(chapter.HTML, `id="`+id+`"`) {
					t.Errorf("toc entry %q points at missing anchor %q", entry.Title, entry.Href)
				}
			}
			walk(entry.Children)
		}
	}
	walk(doc.TOC)
}

// A PDF conversion often leaves a navigation document whose fragments all
// point at page anchors that its own HTML rewriting destroyed. The TOC must
// recover real headings in reading order rather than linking every entry to
// the start of the same file or inventing "Chapter N" spine entries.
func TestBuildRepairsSystematicallyBrokenNavigation(t *testing.T) {
	const nav = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#gone">Foreword</a></li>
<li><a href="chap1.xhtml#gone">Chapter 1: Start</a></li>
<li><a href="chap1.xhtml#gone">First Topic</a></li>
<li><a href="chap1.xhtml#gone">Chapter 2: Next</a></li>
<li><a href="chap1.xhtml#gone">Second Topic</a></li>
</ol></nav></body></html>`

	const chap1 = `<html><body>
<p><a href="#gone"><b>Foreword....................1</b></a></p>
<p><b>Foreword</b></p><p>Opening text.</p>
<p><b>Chapter 1: Start</b></p><p>Start text.</p>
<p><b>First Topic</b></p><p>Topic text.</p>
</body></html>`

	const chap2 = `<html><body>
<h2>Chapter 2: Next</h2><p>Next text.</p>
<p><b>Second Topic</b></p><p>Second topic text.</p>
</body></html>`

	const chap3 = `<html><body><h2>Chapter 3: Later</h2><p>Later text.</p></body></html>`
	const chap4 = `<html><body><h2>Chapter 4: Last</h2><p>Last text.</p></body></html>`

	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLRepair},
		zipEntry{"OEBPS/nav.xhtml", nav},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
		zipEntry{"OEBPS/chap4.xhtml", chap4},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 7 {
		t.Fatalf("toc entries = %d, want 7: %+v", len(doc.TOC), doc.TOC)
	}
	for i, want := range []string{"Foreword", "Chapter 1: Start", "First Topic", "Chapter 2: Next", "Second Topic", "Chapter 3: Later", "Chapter 4: Last"} {
		if doc.TOC[i].Title != want {
			t.Errorf("toc[%d].Title = %q, want %q", i, doc.TOC[i].Title, want)
		}
	}
	assertTOCAnchorsExist(t, doc)

	// The printed contents page is rewritten with the same recovered
	// destinations; it must not keep a dead fragment anchor.
	if strings.Contains(doc.Chapters[0].HTML, "#c0-gone") {
		t.Errorf("printed TOC still contains the broken anchor:\n%s", doc.Chapters[0].HTML)
	}
	if !strings.Contains(doc.Chapters[0].HTML, `href="#c0-nav"`) {
		t.Errorf("printed TOC entry was not repaired:\n%s", doc.Chapters[0].HTML)
	}

}

// Split files that repeat the same leading running heading are continuations
// of one chapter. Only the first copy belongs in the table of contents.
func TestBuildMarksSplitContinuations(t *testing.T) {
	const nav = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml">Chapter One</a></li>
<li><a href="chap2.xhtml#run">Chapter One</a></li>
<li><a href="chap3.xhtml">Chapter One</a></li>
<li><a href="chap4.xhtml">Chapter Two</a></li>
</ol></nav></body></html>`
	chapter := func(text string) string {
		return `<html><body><h2 id="run">Chapter One</h2><p>` + text + `</p></body></html>`
	}
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLRepair},
		zipEntry{"OEBPS/nav.xhtml", nav},
		zipEntry{"OEBPS/chap1.xhtml", chapter("first")},
		zipEntry{"OEBPS/chap2.xhtml", chapter("second")},
		zipEntry{"OEBPS/chap3.xhtml", chapter("third")},
		zipEntry{"OEBPS/chap4.xhtml", `<html><body><h2>Chapter Two</h2><p>next</p></body></html>`},
	)
	doc := buildDocument(t, path)
	for i, want := range []bool{false, true, true, false} {
		if doc.Chapters[i].Continuation != want {
			t.Errorf("chapter %d continuation = %v, want %v", i, doc.Chapters[i].Continuation, want)
		}
	}
	for _, index := range []int{1, 2} {
		if doc.Chapters[index].LabelID != "" {
			t.Errorf("continuation %d still has label %q", index, doc.Chapters[index].LabelID)
		}
		if !strings.Contains(doc.Chapters[index].HTML, "data-reader-leading-heading") {
			t.Errorf("continuation %d has no running-heading marker", index)
		}
	}
	if len(doc.TOC) != 2 || doc.TOC[0].Title != "Chapter One" || doc.TOC[1].Title != "Chapter Two" {
		t.Errorf("toc = %+v, want one entry per chapter", doc.TOC)
	}
	assertTOCAnchorsExist(t, doc)
}

// PDF conversions sometimes retain a complete document outline as the last
// spine document while their EPUB nav is truncated and broken. Prefer the
// recoverable outline, but do not expose the outline document itself as a
// chapter in the table of contents.
func TestBuildUsesDocumentOutlineWhenNavigationIsBroken(t *testing.T) {
	const nav = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#gone">Broken One</a></li>
<li><a href="chap1.xhtml#gone">Broken Two</a></li>
<li><a href="chap1.xhtml#gone">Broken Three</a></li>
</ol></nav></body></html>`
	const chap1 = `<html><body><h2 id="real1">Chapter 1: Start</h2><p>Text.</p>
<h3 id="real2">First Topic</h3><p>More.</p></body></html>`
	const chap2 = `<html><body><h2 id="real3">Chapter 2: Next</h2><p>Text.</p>
<h3 id="real4">Second Topic</h3><p>More.</p></body></html>`
	const outline = `<html><body><h1>Document Outline</h1><ul>
<li><a href="chap1.xhtml#real1">Chapter 1: Start</a><ul>
<li><a href="chap1.xhtml#real2">First Topic</a></li></ul></li>
<li><a href="chap2.xhtml#real3">Chapter 2: Next</a><ul>
<li><a href="chap2.xhtml#real4">Second Topic</a></li></ul></li>
</ul></body></html>`

	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLRepair},
		zipEntry{"OEBPS/nav.xhtml", nav},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", `<html><body><p>Untitled insert.</p></body></html>`},
		zipEntry{"OEBPS/chap4.xhtml", outline},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 2 {
		t.Fatalf("toc entries = %d, want 2 chapters: %+v", len(doc.TOC), doc.TOC)
	}
	if doc.TOC[0].Title != "Chapter 1: Start" || doc.TOC[0].Href != "#c0-real1" {
		t.Errorf("toc[0] = %+v", doc.TOC[0])
	}
	if len(doc.TOC[0].Children) != 1 || doc.TOC[0].Children[0].Title != "First Topic" {
		t.Errorf("toc[0] children = %+v", doc.TOC[0].Children)
	}
	if doc.TOC[1].Title != "Chapter 2: Next" || doc.TOC[1].Href != "#c1-real3" {
		t.Errorf("toc[1] = %+v", doc.TOC[1])
	}
	for _, entry := range doc.TOC {
		if entry.Title == "Document Outline" {
			t.Errorf("outline document leaked into toc: %+v", doc.TOC)
		}
	}
	assertTOCAnchorsExist(t, doc)
}

// EPUB 2 documents may use xml:id instead of id. The reader emits HTML, so
// the anchor must be materialized as a real id before navigation is resolved.
func TestBuildMaterializesXMLIDAnchors(t *testing.T) {
	const nav = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#xml-note">XML Heading</a></li>
</ol></nav></body></html>`
	const chapter = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h2 xml:id="xml-note">XML Heading</h2><p>Text.</p>
</body></html>`
	const other = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Other.</p></body></html>`

	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", nav},
		zipEntry{"OEBPS/chap1.xhtml", chapter},
		zipEntry{"OEBPS/chap2.xhtml", other},
		zipEntry{"OEBPS/chap3.xhtml", other},
	)
	doc := buildDocument(t, path)
	if !strings.Contains(doc.Chapters[0].HTML, `id="c0-xml-note"`) {
		t.Errorf("xml:id was not materialized as a real id:\n%s", doc.Chapters[0].HTML)
	}
	if len(doc.TOC) != 1 || doc.TOC[0].Href != "#c0-xml-note" {
		t.Errorf("toc = %+v, want the materialized xml:id anchor", doc.TOC)
	}
	assertTOCAnchorsExist(t, doc)
}

// Falling back to the embedded document outline must not discard anchor
// repairs already discovered in the primary navigation; otherwise the
// book's printed contents keeps dead fragment links.
func TestBuildKeepsNavigationRepairsWhenOutlineReplacesTOC(t *testing.T) {
	const nav = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#gone">A1</a></li>
<li><a href="chap1.xhtml#gone">A2</a></li>
<li><a href="chap1.xhtml#gone">A3</a></li>
<li><a href="chap1.xhtml#gone">A4</a></li>
</ol></nav></body></html>`
	const chap1 = `<html><body>
<p><a href="#gone"><b>A1............</b></a></p>
<h2 id="a1">A1</h2><p>Text.</p>
<h3 id="a2">A2</h3><p>Text.</p>
<h3 id="a3">A3</h3><p>Text.</p>
<h3 id="a4">A4</h3><p>Text.</p>
</body></html>`
	const chap2 = `<html><body>
<h2 id="b1">B1</h2><p>Text.</p>
<h3 id="b2">B2</h3><p>Text.</p>
<h3 id="b3">B3</h3><p>Text.</p>
<h3 id="b4">B4</h3><p>Text.</p>
</body></html>`
	const outline = `<html><body><h1>Document Outline</h1><ul>
<li><a href="chap1.xhtml#a1">A1</a></li>
<li><a href="chap1.xhtml#a2">A2</a></li>
<li><a href="chap1.xhtml#a3">A3</a></li>
<li><a href="chap1.xhtml#a4">A4</a></li>
<li><a href="chap2.xhtml#b1">B1</a></li>
<li><a href="chap2.xhtml#b2">B2</a></li>
<li><a href="chap2.xhtml#b3">B3</a></li>
<li><a href="chap2.xhtml#b4">B4</a></li>
</ul></body></html>`

	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLRepair},
		zipEntry{"OEBPS/nav.xhtml", nav},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", `<html><body><p>Untitled insert.</p></body></html>`},
		zipEntry{"OEBPS/chap4.xhtml", outline},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 8 {
		t.Fatalf("toc entries = %d, want the 8 outline entries: %+v", len(doc.TOC), doc.TOC)
	}
	if strings.Contains(doc.Chapters[0].HTML, "#c0-gone") {
		t.Errorf("printed TOC kept the dead anchor after outline fallback:\n%s", doc.Chapters[0].HTML)
	}
	if !strings.Contains(doc.Chapters[0].HTML, `href="#c0-a1"`) {
		t.Errorf("printed TOC repair was lost after outline fallback:\n%s", doc.Chapters[0].HTML)
	}
	assertTOCAnchorsExist(t, doc)
}
