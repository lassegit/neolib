package reader_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	doc, err := reader.Build(openPublication(t, path), "testbook")
	if err != nil {
		t.Fatalf("build document: %v", err)
	}
	return doc
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
}
