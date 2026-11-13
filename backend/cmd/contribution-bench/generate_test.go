package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// generateDigest hashes every emitted row, in order, so two runs can be
// compared without holding the whole dataset in memory.
func generateDigest(t *testing.T, p Preset, seed uint64) (string, Manifest, []Row) {
	t.Helper()
	h := sha256.New()
	var rows []Row
	m, err := Generate(p, seed, func(r Row) error {
		fmt.Fprintf(h, "%x|%x|%s|%v|%s|%s|%v|%s|%d|%v|%v|%s\n",
			r.ID, r.Owner, r.LocalID, r.Title != nil, r.Visibility, r.ProjectHash,
			r.ParentSessionID != nil, r.Attempt, r.BlobSize, r.SessionStart != nil,
			r.GitRemote != nil, r.ProjectName)
		rows = append(rows, r)
		return nil
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil)), m, rows
}

func TestGenerateIsDeterministicFromSeed(t *testing.T) {
	p := Preset{Name: "unit", Total: 400, Named: []namedProject{{"project-120", 120}, {"project-40", 40}}}
	a, _, _ := generateDigest(t, p, 7)
	b, _, _ := generateDigest(t, p, 7)
	c, _, _ := generateDigest(t, p, 8)
	if a != b {
		t.Fatalf("same seed produced different rows: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("different seeds produced identical rows")
	}
}

func TestGenerateShapeAndReferentialShape(t *testing.T) {
	p := Preset{Name: "unit", Total: 600, Named: []namedProject{{"project-120", 120}, {"project-40", 40}}}
	_, m, rows := generateDigest(t, p, 3)

	if m.Transcripts != 600 || len(rows) != 600 {
		t.Fatalf("want 600 transcripts, manifest says %d, emitted %d", m.Transcripts, len(rows))
	}
	local := map[string]Row{}
	for _, r := range rows {
		if _, dup := local[r.LocalID]; dup {
			t.Fatalf("duplicate local id %s", r.LocalID)
		}
		local[r.LocalID] = r
	}
	nested, cross, orphans := 0, 0, 0
	for _, r := range rows {
		if len(r.ProjectHash) != 64 || strings.ToLower(r.ProjectHash) != r.ProjectHash {
			t.Fatalf("project hash %q is not 64 lowercase hex", r.ProjectHash)
		}
		if r.ParentSessionID == nil {
			continue
		}
		parent, ok := local[*r.ParentSessionID]
		if !ok {
			orphans++
			continue
		}
		if parent.ParentSessionID != nil {
			nested++
		}
		if parent.ProjectHash != r.ProjectHash {
			cross++
		}
	}
	if nested == 0 {
		t.Errorf("expected nested helpers, found none")
	}
	if cross == 0 {
		t.Errorf("expected cross-project helpers, found none")
	}
	if orphans != m.OrphanRoots {
		t.Errorf("orphan count mismatch: rows %d manifest %d", orphans, m.OrphanRoots)
	}
	if m.Roots+m.Helpers != 600 {
		t.Errorf("roots %d + helpers %d != 600", m.Roots, m.Helpers)
	}
}

func TestPresetsAreConsistent(t *testing.T) {
	for name, p := range presets {
		named := 0
		for _, np := range p.Named {
			named += np.Size
		}
		if named > p.Total {
			t.Errorf("preset %s names %d rows but totals %d", name, named, p.Total)
		}
	}
	if presets["reported"].Total != 12148 {
		t.Errorf("reported dataset must hold 12,148 transcripts")
	}
}
