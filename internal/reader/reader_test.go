package reader_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/lassegit/neolib/internal/epub"
	"github.com/lassegit/neolib/internal/reader"
)

const containerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

const packageXML = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Test Book</dc:title></metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="chap1.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="chap2.xhtml" media-type="application/xhtml+xml"/>
<item id="c3" href="chap3.xhtml" media-type="application/xhtml+xml"/>
<item id="pic" href="images/pic.png" media-type="image/png"/>
</manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/></spine>
</package>`

const navXML = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#top">First Chapter</a></li>
<li><a href="chap2.xhtml">Second Chapter</a></li>
</ol></nav></body></html>`

const chap1 = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>One</title></head>
<body>
<h1 id="top">Chapter One</h1>
<p id="para">Alpha <a href="chap2.xhtml#note">to two</a> <a href="chap2.xhtml">plain</a> <a href="#para">self</a>.</p>
<p><a href="https://example.com/">web</a></p>
<p><a href="//example.com/page">protocol</a></p>
<p><a href="chap2.xhtml"><span><img src="images/linked.png" alt="linked"></span></a></p>
<img src="images/pic.png" alt="pic">
<script>alert(1)</script>
<a href="javascript:alert(2)">bad</a>
<p style="color:red">styled</p>
</body></html>`

const chap2 = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h2><a id="note"></a>Notes</h2>
<p>Beta</p>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
<image xlink:href="images/pic.png" width="10" height="10"></image>
<use href="#note"></use>
</svg>
</body></html>`

const chap3 = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Just some text.</p></body></html>`

type zipEntry struct{ name, data string }

func writeEPUB(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "test.epub"))
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("create %s: %v", entry.name, err)
		}
		if _, err := w.Write([]byte(entry.data)); err != nil {
			t.Fatalf("write %s: %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return file.Name()
}

func openPublication(t *testing.T, path string) *epub.Publication {
	t.Helper()
	pub, err := epub.Open(path)
	if err != nil {
		t.Fatalf("open publication: %v", err)
	}
	t.Cleanup(func() { pub.Close() })
	return pub
}

func buildDocument(t *testing.T, path string) reader.Document {
	t.Helper()
	return buildDocumentWith(t, path, reader.DefaultSettings())
}

func buildDocumentWith(t *testing.T, path string, settings reader.Settings) reader.Document {
	t.Helper()
	return reader.Build(openPublication(t, path), "testbook", settings)
}

func TestBuild(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXML},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
		zipEntry{"OEBPS/images/pic.png", "png"},
	)

	doc := buildDocument(t, path)
	if len(doc.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(doc.Chapters))
	}

	first := doc.Chapters[0]
	if first.ID != "c0" || first.Title != "First Chapter" {
		t.Errorf("first chapter = %q %q", first.ID, first.Title)
	}
	if first.LabelID != "c0-top" {
		t.Errorf("first LabelID = %q, want c0-top", first.LabelID)
	}
	for _, want := range []string{
		`id="c0-top"`,
		`id="c0-para"`,
		`href="#c1-note"`,
		`href="#c1"`,
		`href="#c0-para"`,
		`src="/books/testbook/resource/OEBPS/images/pic.png"`,
		`loading="lazy"`,
		// Default settings: external links open in a new tab and images are
		// wrapped in a link to the full-size resource.
		`href="https://example.com/" target="_blank" rel="noopener noreferrer"`,
		`href="//example.com/page" target="_blank" rel="noopener noreferrer"`,
		`<a href="/books/testbook/resource/OEBPS/images/pic.png" target="_blank" rel="noopener noreferrer"><img`,
	} {
		if !strings.Contains(first.HTML, want) {
			t.Errorf("first chapter HTML missing %q:\n%s", want, first.HTML)
		}
	}
	for _, unwanted := range []string{"script", "javascript:", "style=", "alert"} {
		if strings.Contains(first.HTML, unwanted) {
			t.Errorf("first chapter HTML still contains %q:\n%s", unwanted, first.HTML)
		}
	}

	// An image nested deeper inside a link must not be wrapped again.
	if nested := countNestedAnchors(t, first.HTML); nested != 0 {
		t.Errorf("first chapter has %d nested anchors:\n%s", nested, first.HTML)
	}
	if strings.Contains(first.HTML, `<a href="/books/testbook/resource/OEBPS/images/linked.png"`) {
		t.Errorf("linked image was wrapped in a second anchor:\n%s", first.HTML)
	}
	if !strings.Contains(first.HTML, `src="/books/testbook/resource/OEBPS/images/linked.png"`) {
		t.Errorf("linked image source not rewritten:\n%s", first.HTML)
	}

	second := doc.Chapters[1]
	if second.Title != "Second Chapter" {
		t.Errorf("second title = %q", second.Title)
	}
	if second.LabelID != "c1-title" {
		t.Errorf("second LabelID = %q, want c1-title", second.LabelID)
	}
	for _, want := range []string{
		`xlink:href="/books/testbook/resource/OEBPS/images/pic.png"`,
		`href="#c1-note"`,
	} {
		if !strings.Contains(second.HTML, want) {
			t.Errorf("second chapter HTML missing %q:\n%s", want, second.HTML)
		}
	}

	// A chapter without a heading gets one built from the fallback title.
	third := doc.Chapters[2]
	if third.Title != "Chapter 3" {
		t.Errorf("third title = %q, want Chapter 3", third.Title)
	}
	if !strings.Contains(third.HTML, `<h2 id="c2-title">Chapter 3</h2>`) {
		t.Errorf("third chapter is missing a generated heading:\n%s", third.HTML)
	}

	// Navigation entries link to the anchor they name, not just the section.
	if len(doc.TOC) != 2 {
		t.Fatalf("toc entries = %d, want 2: %+v", len(doc.TOC), doc.TOC)
	}
	if doc.TOC[0].Title != "First Chapter" || doc.TOC[0].Href != "#c0-top" {
		t.Errorf("toc[0] = %+v, want First Chapter -> #c0-top", doc.TOC[0])
	}
	if doc.TOC[1].Title != "Second Chapter" || doc.TOC[1].Href != "#c1" {
		t.Errorf("toc[1] = %+v, want Second Chapter -> #c1", doc.TOC[1])
	}
}

// A generated chapter heading id must not collide with an author id that
// prefixes to the same name: "title" becomes "c0-title", the id the
// generated heading used to take.
func TestBuildGeneratedIDDoesNotCollide(t *testing.T) {
	const chapCollision = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h1>Heading Without Id</h1>
<p id="title">An author element that already owns the prefixed id.</p>
</body></html>`

	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXML},
		zipEntry{"OEBPS/chap1.xhtml", chapCollision},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
		zipEntry{"OEBPS/images/pic.png", "png"},
	)

	doc := buildDocument(t, path)
	first := doc.Chapters[0]
	if first.LabelID == "c0-title" {
		t.Errorf("generated heading reused the author's id %q", first.LabelID)
	}
	if duplicates := duplicateIDs(t, first.HTML); len(duplicates) != 0 {
		t.Errorf("duplicate ids %v in:\n%s", duplicates, first.HTML)
	}
}

// duplicateIDs returns the ids assigned to more than one element in a
// fragment.
func duplicateIDs(t *testing.T, fragment string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	seen := map[string]int{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				if strings.EqualFold(attr.Key, "id") {
					seen[attr.Val]++
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	var duplicates []string
	for id, count := range seen {
		if count > 1 {
			duplicates = append(duplicates, id)
		}
	}
	return duplicates
}

// countNestedAnchors returns the number of anchors nested inside another
// anchor, which is invalid HTML and breaks the outer link.
func countNestedAnchors(t *testing.T, fragment string) int {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	nested := 0
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inAnchor bool) {
		if n.Type == html.ElementNode && n.DataAtom == atom.A {
			if inAnchor {
				nested++
			}
			inAnchor = true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inAnchor)
		}
	}
	walk(doc, false)
	return nested
}

// The reader settings switch off the default link and image decorations.
func TestBuildSettings(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXML},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
		zipEntry{"OEBPS/images/pic.png", "png"},
	)

	doc := buildDocumentWith(t, path, reader.Settings{
		ExternalLinks: reader.ExternalLinksSameTab,
		Images:        reader.ImagesPlain,
	})

	first := doc.Chapters[0].HTML
	if strings.Contains(first, `target="_blank"`) {
		t.Errorf("external link still opens in a new tab:\n%s", first)
	}
	if strings.Contains(first, `<a href="/books/testbook/resource/OEBPS/images/pic.png">`) {
		t.Errorf("image is still wrapped in a link:\n%s", first)
	}
	if !strings.Contains(first, `src="/books/testbook/resource/OEBPS/images/pic.png"`) {
		t.Errorf("image source not rewritten:\n%s", first)
	}
}

// A prefixed EPUB that cannot be repackaged (for example because it also
// contains a stray top-level entry) must still resolve its content.
func TestBuildPrefixed(t *testing.T) {
	const prefix = "Book.epub/"
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"Book.epub", ""}, // stray entry blocks repackaging
		zipEntry{prefix + "META-INF/container.xml", containerXML},
		zipEntry{prefix + "OEBPS/content.opf", packageXML},
		zipEntry{prefix + "OEBPS/nav.xhtml", navXML},
		zipEntry{prefix + "OEBPS/chap1.xhtml", chap1},
		zipEntry{prefix + "OEBPS/chap2.xhtml", chap2},
		zipEntry{prefix + "OEBPS/chap3.xhtml", chap3},
		zipEntry{prefix + "OEBPS/images/pic.png", "png"},
	)

	doc := buildDocument(t, path)
	if len(doc.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(doc.Chapters))
	}
	if doc.Chapters[0].Title != "First Chapter" {
		t.Errorf("first title = %q, want First Chapter", doc.Chapters[0].Title)
	}
	if !strings.Contains(doc.Chapters[0].HTML, `href="#c1-note"`) {
		t.Errorf("cross-chapter link not rewritten:\n%s", doc.Chapters[0].HTML)
	}
	if !strings.Contains(doc.Chapters[0].HTML, `src="/books/testbook/resource/Book.epub/OEBPS/images/pic.png"`) {
		t.Errorf("resource URL does not carry the archive path:\n%s", doc.Chapters[0].HTML)
	}
}

const packageXMLNCX = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Old Book</dc:title></metadata>
<manifest>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="c1" href="chap1.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="chap2.xhtml" media-type="application/xhtml+xml"/>
<item id="c3" href="chap3.xhtml" media-type="application/xhtml+xml"/>
<item id="pic" href="images/pic.png" media-type="image/png"/>
</manifest>
<spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/></spine>
</package>`

const ncxXML = `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<navMap>
<navPoint id="n1"><navLabel><text>Chapter One</text></navLabel><content src="chap1.xhtml"/></navPoint>
<navPoint id="n2"><navLabel><text>Chapter Two</text></navLabel><content src="chap2.xhtml"/></navPoint>
</navMap></ncx>`

func TestBuildNCX(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLNCX},
		zipEntry{"OEBPS/toc.ncx", ncxXML},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
	)

	doc := buildDocument(t, path)
	if doc.Chapters[0].Title != "Chapter One" || doc.Chapters[1].Title != "Chapter Two" {
		t.Errorf("NCX titles not used: %q, %q", doc.Chapters[0].Title, doc.Chapters[1].Title)
	}
	if len(doc.TOC) != 2 {
		t.Fatalf("toc entries = %d, want 2: %+v", len(doc.TOC), doc.TOC)
	}
	if doc.TOC[0].Href != "#c0" || doc.TOC[1].Href != "#c1" {
		t.Errorf("toc hrefs = %q, %q, want #c0, #c1", doc.TOC[0].Href, doc.TOC[1].Href)
	}
}

const packageXMLNCXAnchors = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Anchored Book</dc:title></metadata>
<manifest>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="c1" href="chap%201.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="chap2.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`

const ncxXMLAnchors = `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<navMap>
<navPoint id="n1"><navLabel><text>Part I</text></navLabel><content src="chap%201.xhtml#part1"/></navPoint>
<navPoint id="n2"><navLabel><text>Chapter A</text></navLabel><content src="chap%201.xhtml#sec1"/></navPoint>
<navPoint id="n3"><navLabel><text>Chapter B</text></navLabel><content src="chap2.xhtml#old"/></navPoint>
</navMap></ncx>`

const chapEncoded1 = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h1 id="part1">Part One</h1>
<h2 id="sec1">First Section</h2>
</body></html>`

const chapEncoded2 = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<a name="old"></a>
<h1 id="sec2">Second Heading</h1>
</body></html>`

// Navigation hrefs are URL-encoded, and one document can hold several
// navigation anchors. The NCX labels must title the chapters, and every
// anchor must become its own TOC entry in navigation order.
func TestTOCFromEncodedNCX(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXMLNCXAnchors},
		zipEntry{"OEBPS/toc.ncx", ncxXMLAnchors},
		zipEntry{"OEBPS/chap 1.xhtml", chapEncoded1},
		zipEntry{"OEBPS/chap2.xhtml", chapEncoded2},
	)
	doc := buildDocument(t, path)

	if doc.Chapters[0].Title != "Part I" || doc.Chapters[1].Title != "Chapter B" {
		t.Errorf("chapter titles = %q, %q, want NCX labels", doc.Chapters[0].Title, doc.Chapters[1].Title)
	}

	want := []reader.TOCEntry{
		{Title: "Part I", Href: "#c0-part1"},
		{Title: "Chapter A", Href: "#c0-sec1"},
		{Title: "Chapter B", Href: "#c1-old"},
	}
	if len(doc.TOC) != len(want) {
		t.Fatalf("toc entries = %d, want %d: %+v", len(doc.TOC), len(want), doc.TOC)
	}
	for i, expected := range want {
		if doc.TOC[i].Title != expected.Title || doc.TOC[i].Href != expected.Href {
			t.Errorf("toc[%d] = %+v, want %+v", i, doc.TOC[i], expected)
		}
	}
}

const navXMLHierarchy = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#top">First Chapter</a>
<ol><li><a href="chap1.xhtml#para">Section One</a></li></ol></li>
<li><span>Appendix</span>
<ol><li><a href="chap2.xhtml">Second Chapter</a></li></ol></li>
</ol></nav></body></html>`

// The EPUB 3 navigation document keeps its hierarchy, including group labels
// without a target of their own.
func TestTOCHierarchy(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXMLHierarchy},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 2 {
		t.Fatalf("toc entries = %d, want 2: %+v", len(doc.TOC), doc.TOC)
	}

	first := doc.TOC[0]
	if first.Title != "First Chapter" || first.Href != "#c0-top" {
		t.Errorf("toc[0] = %+v, want First Chapter -> #c0-top", first)
	}
	if len(first.Children) != 1 || first.Children[0].Title != "Section One" || first.Children[0].Href != "#c0-para" {
		t.Errorf("first chapter children = %+v", first.Children)
	}

	appendix := doc.TOC[1]
	if appendix.Title != "Appendix" || appendix.Href != "" {
		t.Errorf("toc[1] = %+v, want a group label without href", appendix)
	}
	if len(appendix.Children) != 1 || appendix.Children[0].Title != "Second Chapter" || appendix.Children[0].Href != "#c1" {
		t.Errorf("appendix children = %+v", appendix.Children)
	}
}

const navXMLMissingFragments = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chap1.xhtml#nope">First Chapter</a></li>
<li><a href="chap2.xhtml#gone">Second Chapter</a></li>
</ol></nav></body></html>`

// A navigation fragment that does not exist must fall back to the top of the
// section instead of leaving a dead link.
func TestTOCMissingFragmentFallsBack(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXMLMissingFragments},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 2 {
		t.Fatalf("toc entries = %d, want 2: %+v", len(doc.TOC), doc.TOC)
	}
	if doc.TOC[0].Href != "#c0" || doc.TOC[1].Href != "#c1" {
		t.Errorf("toc hrefs = %q, %q, want #c0, #c1", doc.TOC[0].Href, doc.TOC[1].Href)
	}
}

const navXMLUnreachable = `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="ghost.xhtml#x">Missing</a>
<ol><li><a href="chap1.xhtml#top">First Chapter</a></li></ol></li>
<li><a href="https://example.com/">External</a></li>
</ol></nav></body></html>`

// Navigation entries for documents outside the spine and for external URLs
// are dropped; their children take their place.
func TestTOCSkipsUnreachableTargets(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/nav.xhtml", navXMLUnreachable},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != 1 {
		t.Fatalf("toc entries = %d, want 1: %+v", len(doc.TOC), doc.TOC)
	}
	if doc.TOC[0].Title != "First Chapter" || doc.TOC[0].Href != "#c0-top" {
		t.Errorf("toc[0] = %+v, want the hoisted First Chapter", doc.TOC[0])
	}
}

// Without a navigation document the table of contents falls back to one
// entry per chapter.
func TestTOCFallbackWithoutNavigation(t *testing.T) {
	path := writeEPUB(t,
		zipEntry{"mimetype", "application/epub+zip"},
		zipEntry{"META-INF/container.xml", containerXML},
		zipEntry{"OEBPS/content.opf", packageXML},
		zipEntry{"OEBPS/chap1.xhtml", chap1},
		zipEntry{"OEBPS/chap2.xhtml", chap2},
		zipEntry{"OEBPS/chap3.xhtml", chap3},
	)
	doc := buildDocument(t, path)
	if len(doc.TOC) != len(doc.Chapters) {
		t.Fatalf("toc entries = %d, want %d", len(doc.TOC), len(doc.Chapters))
	}
	for i, entry := range doc.TOC {
		if entry.Href != "#"+doc.Chapters[i].ID || entry.Title != doc.Chapters[i].Title {
			t.Errorf("toc[%d] = %+v, want %q -> #%s", i, entry, doc.Chapters[i].Title, doc.Chapters[i].ID)
		}
	}
}
