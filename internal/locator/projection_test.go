package locator

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden projection fixtures")

type goldenFile struct {
	Projection  string      `json:"projection"`
	UTF16Length int         `json:"utf16Length"`
	Text        string      `json:"text"`
	Positions   []goldenPos `json:"positions"`
}

type goldenPos struct {
	Position         int     `json:"position"`
	Fragment         string  `json:"fragment"`
	Progression      float64 `json:"progression"`
	TotalProgression float64 `json:"totalProgression"`
}

// The fixtures in testdata are the shared contract for other implementations
// (the TypeScript client will read the same files). Run with -update to
// regenerate the expected JSON.
func TestProjectionGolden(t *testing.T) {
	dir := filepath.Join("testdata", "locators", "projection")
	inputs, err := filepath.Glob(filepath.Join(dir, "*.xhtml"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no projection fixtures found")
	}

	for _, input := range inputs {
		name := strings.TrimSuffix(filepath.Base(input), ".xhtml")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(input)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			chapter, anchors, err := project(0, "chapter.xhtml", "application/xhtml+xml", raw)
			if err != nil {
				t.Fatalf("project: %v", err)
			}
			got := goldenFile{
				Projection:  chapter.Projection,
				UTF16Length: chapter.UTF16Length,
				Text:        chapter.Text,
				Positions:   goldenPositions(buildPositions([]Chapter{chapter}, [][]anchor{anchors})),
			}

			golden := filepath.Join(dir, name+".json")
			if *update {
				data, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatalf("marshal golden: %v", err)
				}
				if err := os.WriteFile(golden, append(data, '\n'), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			data, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run: go test ./internal/locator -update): %v", err)
			}
			var want goldenFile
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatalf("parse golden: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("projection mismatch\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

func goldenPositions(positions []Position) []goldenPos {
	out := make([]goldenPos, 0, len(positions))
	for _, p := range positions {
		out = append(out, goldenPos{
			Position:         p.Position,
			Fragment:         p.Fragment,
			Progression:      p.Progression,
			TotalProgression: p.TotalProgression,
		})
	}
	return out
}

// Skipped elements keep their sibling index but contribute no text, and
// character-data chunks resume numbering around them. A br becomes one
// space and gets no anchor.
func TestProjectionRules(t *testing.T) {
	raw := []byte(`<html><body><p id="a">Keep<script>drop()</script><style>x{}</style><noscript>no</noscript><template><b>tpl</b></template> me<img src="x.png" alt="alt text"/>.<br/>Next</p></body></html>`)
	chapter, anchors, err := project(0, "chapter.xhtml", "application/xhtml+xml", raw)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	if want := "Keep me. Next"; chapter.Text != want {
		t.Fatalf("text = %q, want %q", chapter.Text, want)
	}
	if want := utf16Length(chapter.Text); chapter.UTF16Length != want {
		t.Fatalf("length = %d, want %d", chapter.UTF16Length, want)
	}

	wantPaths := []string{"/4/2[a]/1", "/4/2[a]/9", "/4/2[a]/11", "/4/2[a]/13"}
	if len(anchors) != len(wantPaths) {
		t.Fatalf("anchors = %d, want %d", len(anchors), len(wantPaths))
	}
	for i, want := range wantPaths {
		if anchors[i].path != want {
			t.Errorf("anchor %d path = %q, want %q", i, anchors[i].path, want)
		}
	}
}

func TestProjectionBR(t *testing.T) {
	chapter, anchors, err := project(0, "ch.xhtml", "application/xhtml+xml", []byte(`<html><body>a<br/>b</body></html>`))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if chapter.Text != "a b" {
		t.Fatalf("text = %q, want %q", chapter.Text, "a b")
	}
	if len(anchors) != 2 || anchors[1].path != "/4/3" {
		t.Fatalf("anchors = %+v, want second path /4/3", anchors)
	}
}

func TestEscapeID(t *testing.T) {
	got := escapeID(`a]b^c[d(e)f,g;h`)
	want := `a^]b^^c^[d^(e^)f^,g^;h`
	if got != want {
		t.Fatalf("escapeID = %q, want %q", got, want)
	}
}

func TestPositionsChunking(t *testing.T) {
	text := strings.Repeat("a", 3000)
	chapter, anchors, err := project(0, "ch.xhtml", "application/xhtml+xml", []byte("<html><body><p>"+text+"</p></body></html>"))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	positions := buildPositions([]Chapter{chapter}, [][]anchor{anchors})

	if len(positions) != 3 {
		t.Fatalf("positions = %d, want 3", len(positions))
	}
	wantFragments := []string{"/4/2/1:0", "/4/2/1:1024", "/4/2/1:2048"}
	for i, want := range wantFragments {
		if positions[i].Fragment != want {
			t.Errorf("position %d fragment = %q, want %q", i+1, positions[i].Fragment, want)
		}
	}
	if positions[1].Progression != 1024.0/3000.0 {
		t.Errorf("position 2 progression = %v", positions[1].Progression)
	}
	if positions[2].TotalProgression != 1.0 {
		t.Errorf("position 3 total progression = %v", positions[2].TotalProgression)
	}
}

// A boundary landing between the halves of a surrogate pair must move past
// the pair instead of splitting it.
func TestPositionsSnapSurrogate(t *testing.T) {
	text := strings.Repeat("a", 1023) + "🦊" + strings.Repeat("b", 100)
	chapter, anchors, err := project(0, "ch.xhtml", "application/xhtml+xml", []byte("<html><body><p>"+text+"</p></body></html>"))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	positions := buildPositions([]Chapter{chapter}, [][]anchor{anchors})

	if len(positions) != 2 {
		t.Fatalf("positions = %d, want 2", len(positions))
	}
	if want := "/4/2/1:1025"; positions[1].Fragment != want {
		t.Fatalf("fragment = %q, want %q", positions[1].Fragment, want)
	}
	if want := 1025.0 / float64(chapter.UTF16Length); positions[1].Progression != want {
		t.Fatalf("progression = %v, want %v", positions[1].Progression, want)
	}
}

// A chapter whose whole projection is synthetic (br only) has no anchors and
// therefore no positions; it must not panic or invent boundaries.
func TestPositionsNoAnchors(t *testing.T) {
	chapter, anchors, err := project(0, "ch.xhtml", "application/xhtml+xml", []byte(`<html><body><br/><br/></body></html>`))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	positions := buildPositions([]Chapter{chapter}, [][]anchor{anchors})
	if len(positions) != 0 {
		t.Fatalf("positions = %+v, want none", positions)
	}
}
