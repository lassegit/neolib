package epub_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/epub"
)

func TestPlaceholderTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"", true}, {"Untitled", true}, {"Unknown", true},
		{"0d66f70d-fa40-48b7-b3c6-a83f6d3a4b02", true},
		{"urn:uuid:0d66f70dfa4048b7b3c6a83f6d3a4b02", true},
		{"0d66f70d-fa40-48b7-b3c6-a83f6d3a4b0z", false},
		{"The Unknown", false}, {"A Real Book", false},
	} {
		if got := epub.PlaceholderTitle(tc.title); got != tc.want {
			t.Errorf("PlaceholderTitle(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}

func TestRecoverPlaceholderTitleFromFrontMatter(t *testing.T) {
	const uuid = "0d66f70d-fa40-48b7-b3c6-a83f6d3a4b02"
	const repeated = `<html><body><p><img src="cover.jpg"/></p><p><b>Clean Code</b></p>
<p>A series description.</p><p><b>Clean Code</b></p></body></html>`
	for _, tc := range []struct {
		name, metadata, content, want string
	}{
		{"uuid and repeated bold title", `<dc:title>` + uuid + `</dc:title>`, repeated, "Clean Code"},
		{"missing title", ``, repeated, "Clean Code"},
		{"valid metadata wins", `<dc:title>Official Title</dc:title>`, repeated, "Official Title"},
		{"first chapter h1 is not a title", `<dc:title>` + uuid + `</dc:title>`, `<html><body><h1>A Real Title</h1></body></html>`, uuid},
		{"semantic title", `<dc:title>` + uuid + `</dc:title>`, `<html><body><p>Other text</p><p epub:type="fulltitle">A Real Title</p></body></html>`, "A Real Title"},
		{"single bold paragraph is not enough", `<dc:title>` + uuid + `</dc:title>`, `<html><body><p><b>Dedication</b></p></body></html>`, uuid},
		{"prose before bold is not a title", `<dc:title>` + uuid + `</dc:title>`, `<html><body><p>First text</p><p><b>Dedication</b></p><p><b>Dedication</b></p></body></html>`, uuid},
		{"chapter heading is not a title", `<dc:title>` + uuid + `</dc:title>`, `<html><body><h1>Chapter 1</h1></body></html>`, uuid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := epubEntries("")
			for i := range entries {
				switch entries[i].name {
				case "OEBPS/content.opf":
					entries[i].data = []byte(packageXML(tc.metadata))
				case "OEBPS/chapter1.xhtml":
					entries[i].data = []byte(tc.content)
				}
			}
			file := writeZip(t, filepath.Join(t.TempDir(), "book.epub"), entries...)
			meta, err := epub.Read(file)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Title != tc.want {
				t.Errorf("title = %q, want %q", meta.Title, tc.want)
			}
		})
	}
}

func TestRecoverTitleFromMarkedTitlePage(t *testing.T) {
	const uuid = "0d66f70d-fa40-48b7-b3c6-a83f6d3a4b02"
	entries := epubEntries("")
	for i := range entries {
		switch entries[i].name {
		case "OEBPS/content.opf":
			pkg := packageXML(`<dc:title>` + uuid + `</dc:title>`)
			pkg = strings.Replace(pkg,
				`<item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>`,
				`<item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml" properties="calibre:title-page"/>`, 1)
			entries[i].data = []byte(pkg)
		case "OEBPS/chapter1.xhtml":
			entries[i].data = []byte(`<html><body><h1>A Real Title</h1><p>by A. Writer</p></body></html>`)
		}
	}
	file := writeZip(t, filepath.Join(t.TempDir(), "book.epub"), entries...)
	meta, err := epub.Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "A Real Title" {
		t.Errorf("title = %q, want A Real Title", meta.Title)
	}
}
