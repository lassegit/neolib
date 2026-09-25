// Package epub extracts catalog metadata from EPUB files using only the
// standard library. The EPUB file itself is kept untouched: it is the
// canonical, portable format and the basis for stable references (CFIs).
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxContainerBytes = 1 << 20
	maxPackageBytes   = 4 << 20
	maxCoverBytes     = 8 << 20

	// maxUnwrappedBytes caps how much data may be extracted from a ZIP
	// that wraps an EPUB, guarding against decompression bombs.
	maxUnwrappedBytes = int64(1) << 30

	// maxWrapperDepth limits how many nested ZIP wrappers are unwrapped.
	maxWrapperDepth = 2
)

// Metadata is the catalog-worthy subset of an EPUB.
type Metadata struct {
	Title          string
	Author         string
	Identifier     string
	Cover          []byte
	CoverMediaType string
}

type packageDoc struct {
	Metadata struct {
		Titles      []string  `xml:"title"`
		Creators    []string  `xml:"creator"`
		Identifiers []string  `xml:"identifier"`
		Metas       []metaTag `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []manifestItem `xml:"item"`
	} `xml:"manifest"`
}

type metaTag struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type manifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

// Read parses the OPF package document of the EPUB at filename. Some
// downloads deliver the EPUB inside an outer ZIP archive (for example a
// file named "book.epub.zip"); Read looks through such wrappers
// transparently.
func Read(filename string) (Metadata, error) {
	canonical, err := Canonicalize(filename)
	if err != nil {
		return Metadata{}, err
	}
	if canonical != filename {
		defer os.Remove(canonical)
	}
	return readEPUB(canonical)
}

// Canonicalize resolves filename to an actual EPUB. If filename already
// is one it is returned unchanged. If it is a ZIP archive containing an
// EPUB, the inner EPUB is extracted to a temporary file next to it and
// that path is returned. The caller owns the returned file and must
// remove it when it differs from filename.
func Canonicalize(filename string) (string, error) {
	return canonicalize(filename, 0)
}

func canonicalize(filename string, depth int) (string, error) {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return "", fmt.Errorf("open epub: %w", err)
	}
	defer r.Close()

	a := newArchive(&r.Reader)
	container, hasContainer := a.findContainer()
	if hasContainer {
		if prefix := epubRootPrefix(container); prefix != "" && repackageable(a, prefix) {
			if packed, err := repackageEPUB(filename, a, prefix); err == nil {
				return packed, nil
			}
			// If repackaging fails the archive is still readable by our
			// own parser, so fall through and use it as-is.
		}
	}

	_, pkgErr := findPackagePath(a)
	if hasContainer || pkgErr == nil {
		return filename, nil
	}

	// No package document at the root. A common download layout wraps
	// the EPUB in another ZIP; unwrap and retry.
	inner, ok := findWrappedEPUB(a)
	if !ok {
		return "", pkgErr
	}
	if depth >= maxWrapperDepth {
		return "", fmt.Errorf("nested ZIP wrapper exceeds %d levels", maxWrapperDepth)
	}
	extracted, err := extractZipFile(filename, inner)
	if err != nil {
		return "", err
	}
	resolved, err := canonicalize(extracted, depth+1)
	if err != nil {
		os.Remove(extracted)
		return "", err
	}
	if resolved != extracted {
		os.Remove(extracted)
	}
	return resolved, nil
}

func readEPUB(filename string) (Metadata, error) {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return Metadata{}, fmt.Errorf("open epub: %w", err)
	}
	defer r.Close()

	a := newArchive(&r.Reader)
	opfPath, err := findPackagePath(a)
	if err != nil {
		return Metadata{}, err
	}

	raw, err := a.read(opfPath, maxPackageBytes)
	if err != nil {
		return Metadata{}, fmt.Errorf("read package document: %w", err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	var pkg packageDoc
	if err := xml.Unmarshal(raw, &pkg); err != nil {
		return Metadata{}, fmt.Errorf("parse package document: %w", err)
	}

	meta := Metadata{
		Title:      strings.TrimSpace(firstNonEmpty(pkg.Metadata.Titles)),
		Author:     strings.TrimSpace(firstNonEmpty(pkg.Metadata.Creators)),
		Identifier: strings.TrimSpace(firstNonEmpty(pkg.Metadata.Identifiers)),
	}

	if item, ok := findCoverItem(pkg); ok {
		name := resolveHref(opfPath, item.Href)
		if cover, err := a.read(name, maxCoverBytes); err == nil && len(cover) > 0 {
			meta.Cover = cover
			meta.CoverMediaType = item.MediaType
			if meta.CoverMediaType == "" || meta.CoverMediaType == "application/octet-stream" {
				meta.CoverMediaType = sniffMediaType(name, cover)
			}
		}
	}
	return meta, nil
}

// archive indexes the entries of a ZIP so that lookups tolerate the
// layout quirks seen in real EPUB downloads: an extra top-level
// directory, backslash separators, "./" prefixes, and case differences.
type archive struct {
	r     *zip.Reader
	files map[string]*zip.File
	names []string
}

func newArchive(r *zip.Reader) *archive {
	a := &archive{r: r, files: make(map[string]*zip.File, len(r.File))}
	for _, f := range r.File {
		name := normalizePath(f.Name)
		if name == "" {
			continue
		}
		if _, ok := a.files[name]; !ok {
			a.files[name] = f
			a.names = append(a.names, name)
		}
	}
	return a
}

// normalizePath canonicalizes a ZIP entry name into a slash-separated
// path relative to the archive root.
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

// lookup returns the archive member matching an exact normalized path,
// falling back to a case-insensitive match.
func (a *archive) lookup(name string) (string, *zip.File, error) {
	name = normalizePath(name)
	if f, ok := a.files[name]; ok {
		return name, f, nil
	}
	lower := strings.ToLower(name)
	for _, candidate := range a.names {
		if strings.ToLower(candidate) == lower {
			return candidate, a.files[candidate], nil
		}
	}
	return "", nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// resolve is lookup plus a suffix fallback for entries carrying an extra
// leading directory (the result of zipping a directory rather than its
// contents). It prefers the shortest matching path.
func (a *archive) resolve(name string) (string, *zip.File, error) {
	if p, f, err := a.lookup(name); err == nil {
		return p, f, nil
	}
	suffix := "/" + strings.ToLower(normalizePath(name))
	var (
		bestPath string
		bestFile *zip.File
	)
	for _, candidate := range a.names {
		if !strings.HasSuffix(strings.ToLower(candidate), suffix) {
			continue
		}
		if bestFile == nil || len(candidate) < len(bestPath) {
			bestPath, bestFile = candidate, a.files[candidate]
		}
	}
	if bestFile == nil {
		return "", nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return bestPath, bestFile, nil
}

func (a *archive) read(name string, limit int64) ([]byte, error) {
	_, f, err := a.resolve(name)
	if err != nil {
		return nil, err
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return data, nil
}

// findContainer locates META-INF/container.xml, allowing a leading
// directory and case differences.
func (a *archive) findContainer() (string, bool) {
	for _, name := range a.names {
		if strings.EqualFold(name, "META-INF/container.xml") {
			return name, true
		}
	}
	for _, name := range a.names {
		parts := strings.Split(name, "/")
		if len(parts) >= 2 &&
			strings.EqualFold(parts[len(parts)-2], "META-INF") &&
			strings.EqualFold(parts[len(parts)-1], "container.xml") {
			return name, true
		}
	}
	return "", false
}

func findPackagePath(a *archive) (string, error) {
	container, ok := a.findContainer()
	if !ok {
		return a.findOPF()
	}

	raw, err := a.read(container, maxContainerBytes)
	if err != nil {
		if opf, oerr := a.findOPF(); oerr == nil {
			return opf, nil
		}
		return "", fmt.Errorf("read container: %w", err)
	}

	var doc struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		if opf, oerr := a.findOPF(); oerr == nil {
			return opf, nil
		}
		return "", fmt.Errorf("parse container: %w", err)
	}

	for _, rootfile := range doc.Rootfiles {
		fullPath := normalizePath(unescapeURL(rootfile.FullPath))
		if fullPath == "" {
			continue
		}
		if actual, _, err := a.resolve(fullPath); err == nil {
			return actual, nil
		}
	}
	if opf, err := a.findOPF(); err == nil {
		return opf, nil
	}
	return "", errors.New("no package document referenced by META-INF/container.xml")
}

// findOPF scans the archive for a package document, used when
// META-INF/container.xml is missing or unusable.
func (a *archive) findOPF() (string, error) {
	best := ""
	for _, name := range a.names {
		if a.files[name].FileInfo().IsDir() || isJunkPath(name) {
			continue
		}
		if !strings.EqualFold(path.Ext(name), ".opf") {
			continue
		}
		if best == "" || len(name) < len(best) {
			best = name
		}
	}
	if best == "" {
		return "", errors.New("no package document found in EPUB")
	}
	return best, nil
}

// findWrappedEPUB returns the largest .epub member of an outer archive,
// if any.
func findWrappedEPUB(a *archive) (*zip.File, bool) {
	var best *zip.File
	for _, f := range a.r.File {
		name := normalizePath(f.Name)
		if name == "" || f.FileInfo().IsDir() || isJunkPath(name) {
			continue
		}
		if !strings.EqualFold(path.Ext(name), ".epub") {
			continue
		}
		if best == nil || f.UncompressedSize64 > best.UncompressedSize64 {
			best = f
		}
	}
	return best, best != nil
}

// extractZipFile writes one archive member to a temporary file next to
// outer, so the result shares a filesystem with the caller's rename
// target. The caller owns the returned file.
func extractZipFile(outer string, f *zip.File) (string, error) {
	src, err := f.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(outer), "import-*.epub")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	written, err := io.Copy(tmp, io.LimitReader(src, maxUnwrappedBytes+1))
	if err == nil && written > maxUnwrappedBytes {
		err = fmt.Errorf("wrapped EPUB exceeds %d bytes", maxUnwrappedBytes)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// isJunkPath reports macOS metadata entries, which some tools emit
// alongside the real files.
func isJunkPath(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") ||
		strings.HasPrefix(base, "._") ||
		base == ".DS_Store"
}

// epubRootPrefix returns the directory holding META-INF when the
// archive was made by zipping an extracted EPUB folder (for example
// "Book.epub/META-INF/container.xml"). It is empty for a conforming
// archive.
func epubRootPrefix(containerPath string) string {
	dir := path.Dir(containerPath)
	if !strings.EqualFold(path.Base(dir), "META-INF") {
		return ""
	}
	parent := path.Dir(dir)
	if parent == "." || parent == "" {
		return ""
	}
	return parent + "/"
}

// repackageable reports whether every meaningful entry lives under
// prefix, so the archive can safely be rewritten without it.
func repackageable(a *archive, prefix string) bool {
	found := false
	for _, entry := range a.r.File {
		name := normalizePath(entry.Name)
		if name == "" || isJunkPath(name) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			return false
		}
		found = true
	}
	return found
}

// epubEpoch is the fixed modification time used when repackaging, so
// the same input always produces the same bytes and content-addressed
// deduplication keeps working.
var epubEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// repackageEPUB rewrites an archive whose EPUB lives under a leading
// directory into a conforming EPUB with that directory removed. The
// caller keeps the original on error.
func repackageEPUB(outer string, a *archive, prefix string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(outer), "import-*.epub")
	if err != nil {
		return "", err
	}
	name := tmp.Name()

	if err := writeRepackaged(tmp, a, prefix); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := verifyEPUB(name); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("verify repackaged EPUB: %w", err)
	}
	return name, nil
}

func writeRepackaged(dst io.Writer, a *archive, prefix string) error {
	zw := zip.NewWriter(dst)
	mimetype := prefix + "mimetype"
	write := func(entry *zip.File, rel string, store bool) error {
		src, err := entry.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		hdr := &zip.FileHeader{
			Name:     rel,
			Method:   zip.Deflate,
			Modified: epubEpoch,
			NonUTF8:  entry.NonUTF8,
		}
		if store {
			hdr.Method = zip.Store
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		written, err := io.Copy(w, io.LimitReader(src, maxUnwrappedBytes+1))
		if err == nil && written > maxUnwrappedBytes {
			return fmt.Errorf("%s exceeds %d bytes", rel, maxUnwrappedBytes)
		}
		return err
	}

	// The mimetype member must come first and be stored uncompressed.
	for _, entry := range a.r.File {
		if normalizePath(entry.Name) == mimetype && !entry.FileInfo().IsDir() {
			if err := write(entry, "mimetype", true); err != nil {
				zw.Close()
				return err
			}
			break
		}
	}
	for _, entry := range a.r.File {
		name := normalizePath(entry.Name)
		if name == "" || name == mimetype || entry.FileInfo().IsDir() || isJunkPath(name) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if err := write(entry, strings.TrimPrefix(name, prefix), false); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

func verifyEPUB(filename string) error {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = findPackagePath(newArchive(&r.Reader))
	return err
}

// findCoverItem implements the three cover conventions in the wild, in order
// of reliability: the EPUB3 "cover-image" property, the EPUB2
// <meta name="cover"> pointer, then a manifest item named "cover".
func findCoverItem(pkg packageDoc) (manifestItem, bool) {
	for _, item := range pkg.Manifest.Items {
		for _, prop := range strings.Fields(item.Properties) {
			if prop == "cover-image" {
				return item, true
			}
		}
	}
	for _, meta := range pkg.Metadata.Metas {
		if strings.EqualFold(meta.Name, "cover") && meta.Content != "" {
			for _, item := range pkg.Manifest.Items {
				if item.ID == meta.Content {
					return item, true
				}
			}
		}
	}
	for _, item := range pkg.Manifest.Items {
		if id := strings.ToLower(item.ID); id == "cover" || id == "cover-image" {
			return item, true
		}
	}
	return manifestItem{}, false
}

// resolveHref turns a package-relative manifest href into a zip path.
func resolveHref(opfPath, href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	href = unescapeURL(href)
	return path.Join(path.Dir(opfPath), href)
}

func unescapeURL(s string) string {
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}

func sniffMediaType(name string, data []byte) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	}
	return http.DetectContentType(data)
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
