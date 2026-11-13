package main

// Deterministic synthetic dataset generator for the contribution benchmark.
//
// Every value is derived from the recorded seed. The generator produces only
// synthetic identifiers: UUIDs and project hashes come from SHA-256 over
// labelled counters, titles come from a fixed vocabulary, and git remotes use
// the reserved example.invalid domain. No real names, accounts or credentials
// are produced.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// namedProject is a project whose size is fixed by the dataset preset.
type namedProject struct {
	Name string
	Size int
}

// Preset describes one synthetic dataset. Total counts the target owner's
// transcripts; the named projects come first and a long tail fills the rest.
type Preset struct {
	Name  string
	Total int
	Named []namedProject
}

var presets = map[string]Preset{
	"small": {
		Name:  "small",
		Total: 1000,
		Named: []namedProject{{"project-250", 250}, {"project-40", 40}},
	},
	"reported": {
		Name:  "reported",
		Total: 12148,
		Named: []namedProject{{"project-3000", 3000}, {"project-40", 40}},
	},
	"large": {
		Name:  "large",
		Total: 1000000,
		Named: []namedProject{{"project-250000", 250000}, {"project-3000", 3000}, {"project-40", 40}},
	},
}

// ProjectInfo describes one generated project in the manifest.
type ProjectInfo struct {
	Name           string `json:"name"`
	Hash           string `json:"project_hash"`
	Size           int    `json:"size"`
	PrivacyLabeled bool   `json:"privacy_labeled"`
	CrossIn        int    `json:"cross_project_helpers_in"`
}

// Row is one generated transcript for the target owner.
type Row struct {
	ID              [16]byte
	Owner           [16]byte
	LocalID         string
	Title           *string
	Visibility      string
	Provider        string
	Model           string
	SessionStart    *time.Time
	PublishedAt     time.Time
	BlobKey         string
	BlobSize        int64
	ParentSessionID *string
	GitBranch       *string
	GitRemote       *string
	ProjectHash     string
	ProjectName     string
	Origin          string
	WrappedKey      []byte
	TurnCount       int32
	Attempt         string // "none" or the single attempt status for the group
	ProjectIndex    int
}

// Manifest records what was generated. It is written next to the results so a
// reader can check the shape of the data each measurement ran against.
type Manifest struct {
	Dataset             string         `json:"dataset"`
	Seed                uint64         `json:"seed"`
	OwnerID             string         `json:"owner_id"`
	GroupID             string         `json:"group_id"`
	CreatorID           string         `json:"creator_id"`
	MemberID            string         `json:"member_id"`
	Transcripts         int            `json:"transcripts"`
	Roots               int            `json:"roots"`
	Helpers             int            `json:"helpers"`
	NestedHelpers       int            `json:"nested_helpers"`
	CrossProjectHelpers int            `json:"cross_project_helpers"`
	OrphanRoots         int            `json:"orphan_roots"`
	NoRemote            int            `json:"no_remote"`
	Visibility          map[string]int `json:"visibility"`
	Attempts            map[string]int `json:"attempts"`
	Projects            []ProjectInfo  `json:"projects"`
}

// derivedUUID returns a deterministic RFC 4122 version-4-shaped identifier.
func derivedUUID(seed uint64, label string, n int) [16]byte {
	var buf [8]byte
	h := sha256.New()
	binary.BigEndian.PutUint64(buf[:], seed)
	h.Write(buf[:])
	h.Write([]byte(label))
	binary.BigEndian.PutUint64(buf[:], uint64(n))
	h.Write(buf[:])
	var out [16]byte
	copy(out[:], h.Sum(nil)[:16])
	out[6] = (out[6] & 0x0f) | 0x40
	out[8] = (out[8] & 0x3f) | 0x80
	return out
}

func uuidString(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hexDigest returns 64 lowercase hex characters, the project hash shape the
// handler requires.
func hexDigest(seed uint64, label string, n int) string {
	var buf [8]byte
	h := sha256.New()
	binary.BigEndian.PutUint64(buf[:], seed)
	h.Write(buf[:])
	h.Write([]byte(label))
	binary.BigEndian.PutUint64(buf[:], uint64(n))
	h.Write(buf[:])
	return hex.EncodeToString(h.Sum(nil))
}

var (
	titleVerbs = []string{"fix", "add", "refactor", "explore", "review", "write", "debug", "port", "trim", "measure"}
	titleNouns = []string{"parser", "auth flow", "migration", "README", "billing report", "search index", "export job", "cache layer", "upload path", "lint setup"}
	providers  = []string{"anthropic", "openai", "other"}
	origins    = []string{"user", "agent", "unknown"}
	branches   = []string{"main", "work/a", "work/b", "feature/search", "fix/export"}
	attemptSet = []string{"pending", "approved", "rejected", "retracted", "revoked"}
)

type generator struct {
	preset   Preset
	seed     uint64
	rng      *rand.Rand
	owner    [16]byte
	n        int
	allRoots []string
	m        Manifest
}

// Generate emits every transcript of the preset in a fixed order and returns
// the manifest. The same preset and seed always produce the same rows.
func Generate(p Preset, seed uint64, emit func(Row) error) (Manifest, error) {
	g := &generator{
		preset: p,
		seed:   seed,
		rng:    rand.New(rand.NewPCG(seed, 0x7a11)),
		owner:  derivedUUID(seed, "owner", 0),
	}
	g.m = Manifest{
		Dataset:    p.Name,
		Seed:       seed,
		OwnerID:    uuidString(g.owner),
		GroupID:    uuidString(derivedUUID(seed, "group", 0)),
		CreatorID:  uuidString(derivedUUID(seed, "creator", 0)),
		MemberID:   uuidString(derivedUUID(seed, "member", 0)),
		Visibility: map[string]int{},
		Attempts:   map[string]int{},
	}

	type plannedProject struct {
		name    string
		size    int
		privacy bool
	}
	var planned []plannedProject
	remaining := p.Total
	for _, np := range p.Named {
		planned = append(planned, plannedProject{np.Name, np.Size, false})
		remaining -= np.Size
	}
	if remaining < 0 {
		return g.m, fmt.Errorf("preset %q names more rows than its total", p.Name)
	}
	for t := 0; remaining > 0; t++ {
		// Heavy-tailed sizes: most tail projects are small, a few reach 60.
		size := 1 + int(math.Pow(g.rng.Float64(), 3)*60)
		if size > remaining {
			size = remaining
		}
		privacy := t%25 == 24
		name := fmt.Sprintf("tail-%05d", t)
		if privacy {
			name = fmt.Sprintf("private-%05d", t)
		}
		planned = append(planned, plannedProject{name, size, privacy})
		remaining -= size
	}

	for pi, pr := range planned {
		info := ProjectInfo{Name: pr.name, Hash: hexDigest(seed, "project", pi), Size: pr.size, PrivacyLabeled: pr.privacy}
		helpers := 0
		if pr.size >= 3 {
			helpers = pr.size * 35 / 100
		}
		roots := pr.size - helpers
		var projRoots, projHelpers []string
		depth := map[string]int{}

		for i := 0; i < roots; i++ {
			row := g.newRow(pi, info)
			if g.rng.Float64() < 0.03 {
				orphan := "gone-" + row.LocalID
				row.ParentSessionID = &orphan
				g.m.OrphanRoots++
			}
			if err := g.emit(row, emit); err != nil {
				return g.m, err
			}
			projRoots = append(projRoots, row.LocalID)
			depth[row.LocalID] = 0
			g.m.Roots++
		}

		for j := 0; j < helpers; j++ {
			row := g.newRow(pi, info)
			var parent string
			var d int
			x := g.rng.Float64()
			switch {
			case x < 0.04 && len(g.allRoots) > 0:
				// Helper whose parent is a root of an earlier project.
				parent = g.allRoots[g.rng.IntN(len(g.allRoots))]
				d = 1
				g.m.CrossProjectHelpers++
				info.CrossIn++
			case x < 0.25 && len(projHelpers) > 0:
				cand := projHelpers[g.rng.IntN(len(projHelpers))]
				if depth[cand] < 3 {
					parent = cand
					d = depth[cand] + 1
					g.m.NestedHelpers++
				} else {
					parent = projRoots[g.rng.IntN(len(projRoots))]
					d = 1
				}
			default:
				parent = projRoots[g.rng.IntN(len(projRoots))]
				d = 1
			}
			row.ParentSessionID = &parent
			depth[row.LocalID] = d
			if err := g.emit(row, emit); err != nil {
				return g.m, err
			}
			projHelpers = append(projHelpers, row.LocalID)
			g.m.Helpers++
		}
		g.allRoots = append(g.allRoots, projRoots...)
		g.m.Projects = append(g.m.Projects, info)
	}
	return g.m, nil
}

func (g *generator) emit(row Row, emit func(Row) error) error {
	g.m.Transcripts++
	g.m.Visibility[row.Visibility]++
	g.m.Attempts[row.Attempt]++
	if row.GitRemote == nil {
		g.m.NoRemote++
	}
	return emit(row)
}

// newRow fills every common field for one transcript of project pi. The
// caller sets ParentSessionID for helpers and orphaned roots.
func (g *generator) newRow(pi int, info ProjectInfo) Row {
	g.n++
	n := g.n
	r := Row{
		ID:           derivedUUID(g.seed, "transcript", n),
		Owner:        g.owner,
		LocalID:      fmt.Sprintf("s-%08d", n),
		ProjectHash:  info.Hash,
		ProjectName:  info.Name,
		ProjectIndex: pi,
		Provider:     providers[g.rng.IntN(len(providers))],
		Model:        fmt.Sprintf("model-%d", g.rng.IntN(4)),
		Origin:       origins[g.rng.IntN(len(origins))],
		BlobKey:      "transcripts/" + uuidString(derivedUUID(g.seed, "blob", n)) + ".bin",
		BlobSize:     1000 + g.rng.Int64N(500000),
		TurnCount:    int32(1 + g.rng.IntN(120)),
	}
	r.WrappedKey = make([]byte, 32)
	for i := range r.WrappedKey {
		r.WrappedKey[i] = byte(g.rng.IntN(256))
	}
	if g.rng.Float64() < 0.9 {
		t := titleVerbs[g.rng.IntN(len(titleVerbs))] + " " + titleNouns[g.rng.IntN(len(titleNouns))] + fmt.Sprintf(" %d", g.rng.IntN(1000))
		r.Title = &t
	}
	if g.rng.Float64() < 0.97 {
		s := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(g.rng.Int64N(2*365*86400)) * time.Second)
		r.SessionStart = &s
	}
	r.PublishedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(g.rng.Int64N(300*86400)) * time.Second)
	b := branches[g.rng.IntN(len(branches))]
	r.GitBranch = &b
	if !info.PrivacyLabeled && g.rng.Float64() >= 0.15 {
		remote := fmt.Sprintf("https://example.invalid/org-%d/repo-%d.git", pi%7, pi)
		r.GitRemote = &remote
	}

	switch {
	case info.PrivacyLabeled:
		r.Visibility = "private"
	default:
		x := g.rng.Float64()
		switch {
		case x < 0.60:
			r.Visibility = "private"
		case x < 0.85:
			r.Visibility = "shared"
		default:
			r.Visibility = "public"
		}
	}
	if g.rng.Float64() < 0.30 {
		r.Attempt = attemptSet[g.rng.IntN(len(attemptSet))]
	} else {
		r.Attempt = "none"
	}
	return r
}
