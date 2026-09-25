package epub_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

type zipEntry struct {
	name string
	data []byte
}

// epubEntries returns the members of a minimal EPUB, optionally nested
// under prefix (the layout produced by zipping an extracted folder).
func epubEntries(prefix string) []zipEntry {
	return []zipEntry{
		{prefix + "mimetype", []byte("application/epub+zip")},
		{prefix + "META-INF/container.xml", []byte(containerXML)},
		{prefix + "OEBPS/content.opf", []byte(packageXML)},
		{prefix + "OEBPS/chapter1.xhtml", []byte("<html><body><p>Hello</p></body></html>")},
		{prefix + "OEBPS/images/cover.jpg", fakeJPEG},
	}
}

func writeZip(t *testing.T, path string, entries ...zipEntry) string {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("create %s: %v", entry.name, err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatalf("write %s: %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}

func writeTestEPUB(t *testing.T) string {
	t.Helper()
	return writeZip(t, filepath.Join(t.TempDir(), "test.epub"), epubEntries("")...)
}

func assertTestBook(t *testing.T, path string) {
	t.Helper()
	meta, err := epub.Read(path)
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

func TestRead(t *testing.T) {
	assertTestBook(t, writeTestEPUB(t))
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

// macOS "Compress" on an already-extracted EPUB produces an archive
// whose members are nested under "Book.epub/".
func TestReadWithDirectoryPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Composing Software.epub.zip")
	writeZip(t, path, epubEntries("Composing Software.epub/")...)
	assertTestBook(t, path)
}

// Download sites often wrap the EPUB in an outer ZIP, which gives the
// upload the ".epub.zip" name.
func TestReadWrappedEPUB(t *testing.T) {
	inner, err := os.ReadFile(writeTestEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Composing Software.epub.zip")
	writeZip(t, path, zipEntry{name: "Composing Software.epub", data: inner})
	assertTestBook(t, path)
}

func TestCanonicalizeUnwrapsWrapper(t *testing.T) {
	inner, err := os.ReadFile(writeTestEPUB(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "book.epub.zip")
	writeZip(t, path, zipEntry{name: "book.epub", data: inner})

	canonical, err := epub.Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if canonical == path {
		t.Fatal("Canonicalize returned the wrapper unchanged")
	}
	defer os.Remove(canonical)

	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(inner) {
		t.Fatal("canonical file does not match the wrapped EPUB")
	}
}

// Some generators omit META-INF/container.xml; the package document is
// still discoverable by scanning for a .opf member.
func TestReadWithoutContainer(t *testing.T) {
	entries := epubEntries("")
	var filtered []zipEntry
	for _, entry := range entries {
		if !strings.HasSuffix(entry.name, "container.xml") {
			filtered = append(filtered, entry)
		}
	}
	path := filepath.Join(t.TempDir(), "no-container.epub")
	writeZip(t, path, filtered...)
	assertTestBook(t, path)
}

// Some Windows tools write backslash separators into the ZIP.
func TestReadBackslashSeparators(t *testing.T) {
	entries := epubEntries("")
	for i := range entries {
		entries[i].name = strings.ReplaceAll(entries[i].name, "/", `\`)
	}
	path := filepath.Join(t.TempDir(), "backslash.epub")
	writeZip(t, path, entries...)
	assertTestBook(t, path)
}

func TestReadCaseInsensitiveContainer(t *testing.T) {
	entries := epubEntries("")
	for i := range entries {
		if strings.Contains(entries[i].name, "META-INF") {
			entries[i].name = "meta-inf/Container.xml"
		}
	}
	path := filepath.Join(t.TempDir(), "case.epub")
	writeZip(t, path, entries...)
	assertTestBook(t, path)
}

// Repackaging must be deterministic so the same upload keeps mapping
// to the same content hash.
func TestCanonicalizePrefixedIsDeterministic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub.zip")
	writeZip(t, path, epubEntries("book.epub/")...)

	first, err := epub.Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	defer os.Remove(first)
	second, err := epub.Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	defer os.Remove(second)

	one, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("repackaged output differs between runs")
	}
}
