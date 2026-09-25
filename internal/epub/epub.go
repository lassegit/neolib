// Package epub extracts catalog metadata from EPUB files using only the
// standard library. The EPUB file itself is kept untouched: it is the
// canonical, portable format and the basis for stable references (CFIs).
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const maxCoverBytes = 8 << 20

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

// Read parses the OPF package document of the EPUB at filename.
func Read(filename string) (Metadata, error) {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return Metadata{}, fmt.Errorf("open epub: %w", err)
	}
	defer r.Close()

	opfPath, err := findPackagePath(&r.Reader)
	if err != nil {
		return Metadata{}, err
	}

	raw, err := readZipFile(&r.Reader, opfPath, 4<<20)
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
		if cover, err := readZipFile(&r.Reader, name, maxCoverBytes); err == nil && len(cover) > 0 {
			meta.Cover = cover
			meta.CoverMediaType = item.MediaType
			if meta.CoverMediaType == "" || meta.CoverMediaType == "application/octet-stream" {
				meta.CoverMediaType = sniffMediaType(name, cover)
			}
		}
	}
	return meta, nil
}

func findPackagePath(r *zip.Reader) (string, error) {
	raw, err := readZipFile(r, "META-INF/container.xml", 1<<20)
	if err != nil {
		return "", fmt.Errorf("read container: %w", err)
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &container); err != nil {
		return "", fmt.Errorf("parse container: %w", err)
	}
	for _, rootfile := range container.Rootfiles {
		if rootfile.FullPath != "" {
			return rootfile.FullPath, nil
		}
	}
	return "", fmt.Errorf("no rootfile in META-INF/container.xml")
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

func readZipFile(r *zip.Reader, name string, limit int64) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")
	file, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return data, nil
}

// resolveHref turns a package-relative manifest href into a zip path.
func resolveHref(opfPath, href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if decoded, err := url.PathUnescape(href); err == nil {
		href = decoded
	}
	return path.Join(path.Dir(opfPath), href)
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
