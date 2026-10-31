package comic

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lassegit/neolib/internal/epub"
)

type zipEntry struct {
	name string
	data []byte
}

func buildZIP(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
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
	return buf.Bytes()
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	filename := filepath.Join(dir, name)
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	return filename
}

// buildRAR writes a minimal RAR 4.x archive with stored (uncompressed)
// entries. It exists so the CBR path can be tested without an external RAR
// writer.
func buildRAR(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("Rar!\x1a\x07\x00")

	main := make([]byte, 11)
	main[0] = 0x73 // archive block
	binary.LittleEndian.PutUint16(main[3:5], 13)
	writeRARBlock(&buf, main)

	for _, entry := range entries {
		if len(entry.name) > 0xffff {
			t.Fatalf("RAR entry name too long: %q", entry.name)
		}
		headerSize := 32 + len(entry.name)
		h := make([]byte, headerSize-2)
		h[0] = 0x74 // file block
		binary.LittleEndian.PutUint16(h[1:3], 0x8000)
		binary.LittleEndian.PutUint16(h[3:5], uint16(headerSize))
		binary.LittleEndian.PutUint32(h[5:9], uint32(len(entry.data)))
		binary.LittleEndian.PutUint32(h[9:13], uint32(len(entry.data)))
		binary.LittleEndian.PutUint32(h[14:18], crc32.ChecksumIEEE(entry.data))
		h[22] = 20   // unpack version
		h[23] = 0x30 // store
		binary.LittleEndian.PutUint16(h[24:26], uint16(len(entry.name)))
		copy(h[30:], entry.name)
		writeRARBlock(&buf, h)
		buf.Write(entry.data)
	}
	return buf.Bytes()
}

func writeRARBlock(buf *bytes.Buffer, data []byte) {
	var crc [2]byte
	binary.LittleEndian.PutUint16(crc[:], uint16(crc32.ChecksumIEEE(data)&0xffff))
	buf.Write(crc[:])
	buf.Write(data)
}

func jpegPage(marker byte) []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, marker}
}

func TestConvertCBZ(t *testing.T) {
	first := jpegPage(1)
	second := jpegPage(2)
	tenth := jpegPage(10)
	archive := buildZIP(t, []zipEntry{
		{name: "__MACOSX/._1.jpg", data: []byte("junk")},
		{name: "10.jpg", data: tenth},
		{name: "2.jpg", data: second},
		{name: "1.jpg", data: first},
		{name: "ComicInfo.xml", data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<ComicInfo>
  <Series>Example Series</Series>
  <Number>2</Number>
  <Title>The Second Issue</Title>
  <Writer>Ada Lovelace</Writer>
  <Publisher>Example Press</Publisher>
  <Year>2021</Year>
  <Month>3</Month>
  <Day>4</Day>
  <LanguageISO>en</LanguageISO>
</ComicInfo>`)},
	})
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbz", archive)

	path, err := Convert(filename, dir)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer os.Remove(path)

	meta, err := epub.Read(path)
	if err != nil {
		t.Fatalf("read converted EPUB: %v", err)
	}
	if meta.Title != "Example Series #2: The Second Issue" {
		t.Errorf("title = %q", meta.Title)
	}
	if meta.Author != "Ada Lovelace" {
		t.Errorf("author = %q", meta.Author)
	}
	if meta.Publisher != "Example Press" {
		t.Errorf("publisher = %q", meta.Publisher)
	}
	if meta.Published != "2021-03-04" {
		t.Errorf("published = %q", meta.Published)
	}
	if meta.Language != "en" {
		t.Errorf("language = %q", meta.Language)
	}
	if !bytes.Equal(meta.Cover, first) {
		t.Errorf("cover is not the first page: %x", meta.Cover)
	}

	pub, err := epub.Open(path)
	if err != nil {
		t.Fatalf("open converted EPUB: %v", err)
	}
	defer pub.Close()

	spine := pub.Spine()
	wantImages := [][]byte{first, second, tenth}
	if len(spine) != len(wantImages) {
		t.Fatalf("spine has %d documents, want %d", len(spine), len(wantImages))
	}
	for i, want := range wantImages {
		if spine[i].Href != pagePath(i+1) {
			t.Errorf("spine[%d] = %q, want %q", i, spine[i].Href, pagePath(i+1))
		}
		got, err := pub.Read(imagePath(i+1, ".jpg"), 1<<20)
		if err != nil {
			t.Fatalf("read image %d: %v", i+1, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("image %d = %x, want %x", i+1, got, want)
		}
	}

	doc, err := pub.Read(pagePath(1), 1<<20)
	if err != nil {
		t.Fatalf("read page document: %v", err)
	}
	for _, want := range []string{`hidden="hidden"`, `>Page 1</h1>`, `src="../images/page-0001.jpg"`} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("page document missing %q:\n%s", want, doc)
		}
	}
}

func TestConvertCBR(t *testing.T) {
	first := jpegPage(1)
	second := jpegPage(2)
	archive := buildRAR(t, []zipEntry{
		{name: "2.jpg", data: second},
		{name: "ComicInfo.xml", data: []byte(`<?xml version="1.0"?>
<ComicInfo>
  <Series>RAR Comic</Series>
  <Number>4</Number>
  <Writer>Grace Hopper</Writer>
</ComicInfo>`)},
		{name: "1.jpg", data: first},
	})
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbr", archive)

	path, err := Convert(filename, dir)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer os.Remove(path)

	meta, err := epub.Read(path)
	if err != nil {
		t.Fatalf("read converted EPUB: %v", err)
	}
	if meta.Title != "RAR Comic #4" {
		t.Errorf("title = %q", meta.Title)
	}
	if meta.Author != "Grace Hopper" {
		t.Errorf("author = %q", meta.Author)
	}
	if !bytes.Equal(meta.Cover, first) {
		t.Errorf("cover is not the first page: %x", meta.Cover)
	}
}

func TestConvertIsDeterministic(t *testing.T) {
	archive := buildZIP(t, []zipEntry{
		{name: "2.jpg", data: jpegPage(2)},
		{name: "1.jpg", data: jpegPage(1)},
	})
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbz", archive)

	firstDir := t.TempDir()
	first, err := Convert(filename, firstDir)
	if err != nil {
		t.Fatalf("first Convert: %v", err)
	}
	defer os.Remove(first)

	secondDir := t.TempDir()
	second, err := Convert(filename, secondDir)
	if err != nil {
		t.Fatalf("second Convert: %v", err)
	}
	defer os.Remove(second)

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("converting the same comic produced different EPUBs")
	}
}

func TestConvertRejectsEPUBArchive(t *testing.T) {
	archive := buildZIP(t, []zipEntry{
		{name: "META-INF/container.xml", data: []byte("<container/>")},
		{name: "1.jpg", data: jpegPage(1)},
	})
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbz", archive)

	if _, err := Convert(filename, dir); !errors.Is(err, ErrNotComic) {
		t.Fatalf("Convert error = %v, want ErrNotComic", err)
	}
}

func TestConvertRejectsArchiveWithoutImages(t *testing.T) {
	archive := buildZIP(t, []zipEntry{
		{name: "readme.txt", data: []byte("not a comic")},
	})
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbz", archive)

	if _, err := Convert(filename, dir); !errors.Is(err, ErrNotComic) {
		t.Fatalf("Convert error = %v, want ErrNotComic", err)
	}
}

func TestConvertRejectsNonArchive(t *testing.T) {
	dir := t.TempDir()
	filename := writeFile(t, dir, "example.cbz", []byte("not an archive"))

	if _, err := Convert(filename, dir); !errors.Is(err, ErrNotComic) {
		t.Fatalf("Convert error = %v, want ErrNotComic", err)
	}
}

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.jpg", "2.jpg", true},
		{"2.jpg", "10.jpg", true},
		{"10.jpg", "2.jpg", false},
		{"2.jpg", "02.jpg", true},
		{"Chapter 2/1.jpg", "Chapter 10/1.jpg", true},
		{"page.jpeg", "page.JPG", true},
	}
	for _, test := range tests {
		if got := naturalLess(test.a, test.b); got != test.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", test.a, test.b, got, test.want)
		}
	}
}
