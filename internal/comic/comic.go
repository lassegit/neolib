// Package comic converts comic book archives into the EPUB form that the
// rest of the application stores and renders. CBZ files are ZIP archives and
// CBR files are RAR archives; both normally contain nothing but page images
// and an optional ComicInfo.xml metadata file.
package comic

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nwaples/rardecode/v2"
)

const (
	// maxPages bounds the number of spine documents in one generated EPUB.
	maxPages = 5000
	// maxImageBytes bounds a single extracted page image.
	maxImageBytes = 256 << 20
	// maxTotalImageBytes bounds all extracted images together. Compression
	// formats must not let a small upload expand into an unbounded archive.
	maxTotalImageBytes = int64(1) << 30
	// maxComicInfoBytes bounds the optional metadata document.
	maxComicInfoBytes = 1 << 20
	// maxDictionaryBytes caps RAR decompression memory for untrusted input.
	maxDictionaryBytes = 256 << 20
)

// ErrNotComic marks a file that is not a readable comic archive, as opposed
// to an I/O or storage failure while converting one.
var ErrNotComic = errors.New("not a readable comic archive")

type imageType struct {
	ext       string
	mediaType string
}

var imageTypes = map[string]imageType{
	".jpg":  {ext: ".jpg", mediaType: "image/jpeg"},
	".jpeg": {ext: ".jpg", mediaType: "image/jpeg"},
	".png":  {ext: ".png", mediaType: "image/png"},
	".gif":  {ext: ".gif", mediaType: "image/gif"},
	".webp": {ext: ".webp", mediaType: "image/webp"},
	".avif": {ext: ".avif", mediaType: "image/avif"},
}

type page struct {
	source    string // normalized archive path, used for ordering
	file      string // extracted image in the temporary work directory
	ext       string
	mediaType string
	sum       [sha256.Size]byte
}

// Convert extracts the images from a CBZ/CBR archive, builds a deterministic
// EPUB, and returns the path of the temporary EPUB. Callers own the returned
// file and must remove it. Files that are not comics return ErrNotComic.
func Convert(filename, dir string) (string, error) {
	work, err := os.MkdirTemp(dir, "comic-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	pages, info, err := extract(filename, work)
	if err != nil {
		return "", err
	}
	sort.SliceStable(pages, func(i, j int) bool {
		return naturalLess(pages[i].source, pages[j].source)
	})

	out, err := os.CreateTemp(dir, "import-*.epub")
	if err != nil {
		return "", err
	}
	name := out.Name()
	if err := writeEPUB(out, pages, metadataFrom(info), identifierFor(pages)); err != nil {
		out.Close()
		os.Remove(name)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func extract(filename, work string) ([]page, *comicInfo, error) {
	kind, err := sniff(filename)
	if err != nil {
		return nil, nil, err
	}

	c := &collector{work: work}
	switch kind {
	case archiveZIP:
		err = c.extractZIP(filename)
	case archiveRAR:
		err = c.extractRAR(filename)
	default:
		err = ErrNotComic
	}
	if err != nil {
		return nil, nil, err
	}
	return c.result()
}

type archiveKind int

const (
	archiveUnknown archiveKind = iota
	archiveZIP
	archiveRAR
)

func sniff(filename string) (archiveKind, error) {
	f, err := os.Open(filename)
	if err != nil {
		return archiveUnknown, err
	}
	defer f.Close()

	var sig [8]byte
	n, err := io.ReadFull(f, sig[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return archiveUnknown, err
	}
	data := sig[:n]
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")),
		bytes.HasPrefix(data, []byte("PK\x05\x06")),
		bytes.HasPrefix(data, []byte("PK\x07\x08")):
		return archiveZIP, nil
	case bytes.HasPrefix(data, []byte("Rar!\x1a\x07\x00")),
		bytes.HasPrefix(data, []byte("Rar!\x1a\x07\x01\x00")):
		return archiveRAR, nil
	default:
		return archiveUnknown, ErrNotComic
	}
}

type collector struct {
	work     string
	pages    []page
	info     *comicInfo
	total    int64
	document bool // the archive also contains EPUB content documents
}

func (c *collector) extractZIP(filename string) error {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotComic, err)
	}
	defer r.Close()

	for _, entry := range r.File {
		name := normalizePath(entry.Name)
		if name == "" || entry.FileInfo().IsDir() || isJunk(name) {
			continue
		}
		if isComicInfo(name) {
			if c.info != nil {
				continue
			}
			rc, err := entry.Open()
			if err != nil {
				return fmt.Errorf("%w: open ComicInfo.xml: %v", ErrNotComic, err)
			}
			err = c.addInfo(rc)
			rc.Close()
			if err != nil {
				return err
			}
			continue
		}
		if isDocument(name) {
			c.document = true
			continue
		}
		typ, ok := imageTypeFor(name)
		if !ok {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("%w: open %q: %v", ErrNotComic, name, err)
		}
		err = c.addPage(name, rc, typ)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *collector) extractRAR(filename string) error {
	r, err := rardecode.OpenReader(filename, rardecode.MaxDictionarySize(maxDictionaryBytes))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotComic, err)
	}
	defer r.Close()

	for {
		header, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotComic, err)
		}
		name := normalizePath(header.Name)
		if name == "" || header.IsDir || isJunk(name) {
			continue
		}
		if header.Encrypted {
			return fmt.Errorf("%w: encrypted RAR archives are not supported", ErrNotComic)
		}
		if isComicInfo(name) {
			if c.info != nil {
				continue
			}
			if err := c.addInfo(r); err != nil {
				return err
			}
			continue
		}
		if isDocument(name) {
			c.document = true
			continue
		}
		typ, ok := imageTypeFor(name)
		if !ok {
			continue
		}
		if header.UnPackedSize > maxImageBytes {
			return fmt.Errorf("%w: page %q exceeds %d bytes", ErrNotComic, name, maxImageBytes)
		}
		if err := c.addPage(name, r, typ); err != nil {
			return err
		}
	}
}

func (c *collector) addPage(name string, r io.Reader, typ imageType) error {
	if len(c.pages) >= maxPages {
		return fmt.Errorf("%w: more than %d pages", ErrNotComic, maxPages)
	}

	dest := filepath.Join(c.work, fmt.Sprintf("page-%04d%s", len(c.pages)+1, typ.ext))
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(r, maxImageBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("%w: read %q: %v", ErrNotComic, name, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxImageBytes {
		return fmt.Errorf("%w: page %q exceeds %d bytes", ErrNotComic, name, maxImageBytes)
	}

	c.total += written
	if c.total > maxTotalImageBytes {
		return fmt.Errorf("%w: comic images exceed %d bytes", ErrNotComic, maxTotalImageBytes)
	}

	p := page{
		source:    name,
		file:      dest,
		ext:       typ.ext,
		mediaType: typ.mediaType,
	}
	copy(p.sum[:], hasher.Sum(nil))
	c.pages = append(c.pages, p)
	return nil
}

func (c *collector) addInfo(r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, maxComicInfoBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read ComicInfo.xml: %v", ErrNotComic, err)
	}
	if int64(len(data)) > maxComicInfoBytes {
		return fmt.Errorf("%w: ComicInfo.xml exceeds %d bytes", ErrNotComic, maxComicInfoBytes)
	}
	c.info = parseComicInfo(data)
	return nil
}

func (c *collector) result() ([]page, *comicInfo, error) {
	if c.document {
		return nil, nil, fmt.Errorf("%w: archive contains EPUB documents", ErrNotComic)
	}
	if len(c.pages) == 0 {
		return nil, nil, ErrNotComic
	}
	return c.pages, c.info, nil
}

func imageTypeFor(name string) (imageType, bool) {
	typ, ok := imageTypes[strings.ToLower(path.Ext(name))]
	return typ, ok
}

func normalizePath(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimLeft(name, "/")
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	name = path.Clean(name)
	if name == "." {
		return ""
	}
	return name
}

func isJunk(name string) bool {
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if lower == "__macosx" ||
			strings.HasPrefix(lower, "._") ||
			lower == ".ds_store" ||
			lower == "thumbs.db" ||
			lower == "desktop.ini" {
			return true
		}
	}
	return false
}

func isComicInfo(name string) bool {
	return strings.EqualFold(path.Base(name), "ComicInfo.xml")
}

// isDocument detects EPUB content documents. A ZIP that contains them is an
// EPUB (possibly broken), not a page-image comic, even if it also embeds
// images.
func isDocument(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".opf", ".xhtml", ".html", ".htm", ".ncx":
		return true
	}
	return strings.EqualFold(name, "META-INF/container.xml")
}

type comicInfo struct {
	Title       string `xml:"Title"`
	Series      string `xml:"Series"`
	Number      string `xml:"Number"`
	Writer      string `xml:"Writer"`
	Penciller   string `xml:"Penciller"`
	Publisher   string `xml:"Publisher"`
	Year        string `xml:"Year"`
	Month       string `xml:"Month"`
	Day         string `xml:"Day"`
	LanguageISO string `xml:"LanguageISO"`
}

func parseComicInfo(data []byte) *comicInfo {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var info comicInfo
	if err := xml.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}

type bookMetadata struct {
	title     string
	author    string
	publisher string
	published string
	language  string
}

func metadataFrom(info *comicInfo) bookMetadata {
	meta := bookMetadata{language: "und"}
	if info == nil {
		return meta
	}
	meta.title = info.bookTitle()
	meta.author = strings.TrimSpace(info.Writer)
	if meta.author == "" {
		meta.author = strings.TrimSpace(info.Penciller)
	}
	meta.publisher = strings.TrimSpace(info.Publisher)
	meta.published = info.published()
	if lang := strings.TrimSpace(info.LanguageISO); lang != "" {
		meta.language = lang
	}
	return meta
}

func (c *comicInfo) bookTitle() string {
	title := strings.TrimSpace(c.Title)
	series := strings.TrimSpace(c.Series)
	number := strings.TrimSpace(c.Number)
	if series == "" {
		return title
	}

	out := series
	if number != "" {
		out += " #" + number
	}
	if title != "" && !strings.EqualFold(title, series) && !strings.EqualFold(title, "Cover") {
		out += ": " + title
	}
	return out
}

func (c *comicInfo) published() string {
	year := atoi(c.Year)
	if year <= 0 {
		return ""
	}
	if month := atoi(c.Month); month >= 1 && month <= 12 {
		if day := atoi(c.Day); day >= 1 && day <= 31 {
			return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
		}
		return fmt.Sprintf("%04d-%02d", year, month)
	}
	return fmt.Sprintf("%04d", year)
}

func atoi(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func identifierFor(pages []page) string {
	h := sha256.New()
	for _, p := range pages {
		h.Write(p.sum[:])
	}
	return "urn:sha256:" + fmt.Sprintf("%x", h.Sum(nil))
}

func naturalLess(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if isASCIIDigit(ar[i]) && isASCIIDigit(br[j]) {
			si, sj := i, j
			for i < len(ar) && isASCIIDigit(ar[i]) {
				i++
			}
			for j < len(br) && isASCIIDigit(br[j]) {
				j++
			}
			an := strings.TrimLeft(string(ar[si:i]), "0")
			bn := strings.TrimLeft(string(br[sj:j]), "0")
			if len(an) != len(bn) {
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			if i-si != j-sj {
				return i-si < j-sj
			}
			continue
		}

		al, bl := unicode.ToLower(ar[i]), unicode.ToLower(br[j])
		if al != bl {
			return al < bl
		}
		i++
		j++
	}
	if i != len(ar) || j != len(br) {
		return i == len(ar)
	}
	return a < b
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
