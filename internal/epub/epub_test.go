package epub_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/lassegit/neolib/internal/epub"
)

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const packageXML = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Test Book</dc:title>
    <dc:creator>Ada Lovelace</dc:creator>
    <dc:identifier id="pub-id">urn:isbn:9780000000001</dc:identifier>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="images/cover.jpg" media-type="image/jpeg"/>
  </manifest>
  <spine>
    <itemref idref="chapter1"/>
  </spine>
</package>`

var fakeJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}

func writeTestEPUB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.epub")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", containerXML)
	add("OEBPS/content.opf", packageXML)
	add("OEBPS/chapter1.xhtml", "<html><body><p>Hello</p></body></html>")

	cover, err := zw.Create("OEBPS/images/cover.jpg")
	if err != nil {
		t.Fatalf("create cover: %v", err)
	}
	if _, err := cover.Write(fakeJPEG); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close epub: %v", err)
	}
	return path
}

func TestRead(t *testing.T) {
	meta, err := epub.Read(writeTestEPUB(t))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if meta.Title != "Test Book" {
		t.Errorf("Title = %q, want %q", meta.Title, "Test Book")
	}
	if meta.Author != "Ada Lovelace" {
		t.Errorf("Author = %q, want %q", meta.Author, "Ada Lovelace")
	}
	if meta.Identifier != "urn:isbn:9780000000001" {
		t.Errorf("Identifier = %q", meta.Identifier)
	}
	if meta.CoverMediaType != "image/jpeg" {
		t.Errorf("CoverMediaType = %q, want image/jpeg", meta.CoverMediaType)
	}
	if string(meta.Cover) != string(fakeJPEG) {
		t.Errorf("Cover = %v, want %v", meta.Cover, fakeJPEG)
	}
}

func TestReadRejectsNonEPUB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-epub.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := epub.Read(path); err == nil {
		t.Fatal("Read accepted a non-EPUB file")
	}
}
