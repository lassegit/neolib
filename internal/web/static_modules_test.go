package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The reader modules are plain ES modules served from disk; Go cannot type
// check them. This test keeps named imports and exports in sync, which was a
// real regression: progress.js imported scrollToRange while annotations.js
// only defined it locally, so the whole reader failed to boot.
var (
	jsExportDecl = regexp.MustCompile(`(?m)^export\s+(?:async\s+)?(?:function|class|const|let|var)\s+([A-Za-z_$][\w$]*)`)
	jsExportList = regexp.MustCompile(`(?m)^export\s*\{([^}]*)\}`)
	jsImportList = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*["']\./([\w.-]+\.js)["']`)
)

func TestReaderModuleImportsResolve(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("static", "reader", "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no reader modules found: %v", err)
	}

	exports := make(map[string]map[string]bool)
	sources := make(map[string]string)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		name := filepath.Base(file)
		sources[name] = string(data)
		set := make(map[string]bool)
		for _, match := range jsExportDecl.FindAllStringSubmatch(string(data), -1) {
			set[match[1]] = true
		}
		for _, match := range jsExportList.FindAllStringSubmatch(string(data), -1) {
			for _, item := range strings.Split(match[1], ",") {
				item = strings.TrimSpace(item)
				if item == "" {
					continue
				}
				if before, _, found := strings.Cut(item, " as "); found {
					item = strings.TrimSpace(before)
				}
				set[item] = true
			}
		}
		exports[name] = set
	}

	for name, source := range sources {
		for _, match := range jsImportList.FindAllStringSubmatch(source, -1) {
			target := match[2]
			available, ok := exports[target]
			if !ok {
				t.Errorf("%s imports from unknown module %s", name, target)
				continue
			}
			for _, item := range strings.Split(match[1], ",") {
				imported := strings.TrimSpace(item)
				if imported == "" {
					continue
				}
				if before, _, found := strings.Cut(imported, " as "); found {
					imported = strings.TrimSpace(before)
				}
				if !available[imported] {
					t.Errorf("%s imports %q from %s, which does not export it", name, imported, target)
				}
			}
		}
	}
}
