package comic

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// epubEpoch makes generated archives byte-for-byte reproducible, so the same
// comic file always maps to the same content-addressed EPUB.
var epubEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

const xmlHeader = `<?xml version="1.0" encoding="utf-8"?>` + "\n"

const containerXML = xmlHeader + `<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

// writeEPUB lays out the generated comic as a reflowable EPUB. Each page is
// its own spine document, which keeps the reader's chapter anchors and
// progress model intact without changing the EPUB pipeline.
func writeEPUB(dst io.Writer, pages []page, meta bookMetadata, identifier string) error {
	zw := zip.NewWriter(dst)
	fail := func(err error) error {
		zw.Close()
		return err
	}

	// The mimetype member must come first and be stored uncompressed.
	if err := writeEntry(zw, "mimetype", zip.Store, "application/epub+zip"); err != nil {
		return fail(err)
	}
	if err := writeEntry(zw, "META-INF/container.xml", zip.Deflate, containerXML); err != nil {
		return fail(err)
	}
	if err := writeEntry(zw, "OEBPS/content.opf", zip.Deflate, packageDocument(pages, meta, identifier)); err != nil {
		return fail(err)
	}
	if err := writeEntry(zw, "OEBPS/nav.xhtml", zip.Deflate, navigationDocument(len(pages))); err != nil {
		return fail(err)
	}
	for i, p := range pages {
		n := i + 1
		if err := writeEntry(zw, pagePath(n), zip.Deflate, pageDocument(n, p.ext)); err != nil {
			return fail(err)
		}
	}
	for i, p := range pages {
		if err := copyEntry(zw, imagePath(i+1, p.ext), p.file); err != nil {
			return fail(err)
		}
	}
	return zw.Close()
}

func writeEntry(zw *zip.Writer, name string, method uint16, content string) error {
	w, err := createEntry(zw, name, method)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

func copyEntry(zw *zip.Writer, name, filename string) error {
	src, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer src.Close()

	// Page images are already compressed; storing them avoids recompressing
	// an entire comic on import.
	w, err := createEntry(zw, name, zip.Store)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

func createEntry(zw *zip.Writer, name string, method uint16) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{
		Name:     name,
		Method:   method,
		Modified: epubEpoch,
	})
}

func packageDocument(pages []page, meta bookMetadata, identifier string) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">`)
	b.WriteString(`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">`)
	b.WriteString(`<dc:identifier id="book-id">` + xmlText(identifier) + `</dc:identifier>`)
	b.WriteString(`<dc:title>` + xmlText(meta.title) + `</dc:title>`)
	if meta.author != "" {
		b.WriteString(`<dc:creator>` + xmlText(meta.author) + `</dc:creator>`)
	}
	if meta.publisher != "" {
		b.WriteString(`<dc:publisher>` + xmlText(meta.publisher) + `</dc:publisher>`)
	}
	if meta.published != "" {
		b.WriteString(`<dc:date>` + xmlText(meta.published) + `</dc:date>`)
	}
	b.WriteString(`<dc:language>` + xmlText(meta.language) + `</dc:language>`)
	b.WriteString(`<meta property="dcterms:modified">1980-01-01T00:00:00Z</meta>`)
	if len(pages) > 0 {
		// EPUB 2 readers use this pointer; EPUB 3 readers use the
		// cover-image property on the manifest item below.
		b.WriteString(`<meta name="cover" content="i0001"/>`)
	}
	b.WriteString(`</metadata><manifest>`)
	b.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	for i := range pages {
		n := i + 1
		b.WriteString(`<item id="p` + fmt.Sprintf("%04d", n) + `" href="` + pageHref(n) + `" media-type="application/xhtml+xml"/>`)
	}
	for i, p := range pages {
		n := i + 1
		props := ""
		if i == 0 {
			props = ` properties="cover-image"`
		}
		b.WriteString(`<item id="i` + fmt.Sprintf("%04d", n) + `" href="` + imageHref(n, p.ext) + `" media-type="` + p.mediaType + `"` + props + `/>`)
	}
	b.WriteString(`</manifest><spine>`)
	for i := range pages {
		b.WriteString(`<itemref idref="p` + fmt.Sprintf("%04d", i+1) + `"/>`)
	}
	b.WriteString(`</spine></package>`)
	return b.String()
}

func navigationDocument(count int) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<!DOCTYPE html>`)
	b.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">`)
	b.WriteString(`<head><title>Contents</title></head><body>`)
	b.WriteString(`<nav epub:type="toc" id="toc"><h1>Contents</h1><ol>`)
	for n := 1; n <= count; n++ {
		title := pageTitle(n)
		b.WriteString(`<li><a href="` + pageHref(n) + `">` + title + `</a></li>`)
	}
	b.WriteString(`</ol></nav></body></html>`)
	return b.String()
}

func pageDocument(n int, ext string) string {
	title := pageTitle(n)
	return xmlHeader + `<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>` + title + `</title></head>
<body>
<h1 id="page-` + strconv.Itoa(n) + `" hidden="hidden">` + title + `</h1>
<div class="comic-page"><img src="../` + imageHref(n, ext) + `" alt="` + title + `"/></div>
</body>
</html>`
}

func pageTitle(n int) string {
	return "Page " + strconv.Itoa(n)
}

func pageHref(n int) string {
	return fmt.Sprintf("pages/page-%04d.xhtml", n)
}

func pagePath(n int) string {
	return "OEBPS/" + pageHref(n)
}

func imageHref(n int, ext string) string {
	return fmt.Sprintf("images/page-%04d%s", n, ext)
}

func imagePath(n int, ext string) string {
	return "OEBPS/" + imageHref(n, ext)
}

func xmlText(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
