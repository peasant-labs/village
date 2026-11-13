package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// result is one measured workload under one configuration. Every value in the
// results document is copied from a line of results.jsonl written here.
type result struct {
	Dataset     string  `json:"dataset"`
	Config      string  `json:"config"`
	Workload    string  `json:"workload"`
	Mode        string  `json:"mode"` // warm, cold, or write
	Samples     int     `json:"samples"`
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	P99Ms       float64 `json:"p99_ms"`
	MinMs       float64 `json:"min_ms"`
	MaxMs       float64 `json:"max_ms"`
	Rows        int64   `json:"rows,omitempty"`
	Bytes       int64   `json:"bytes,omitempty"`
	AllocBytes  uint64  `json:"alloc_bytes,omitempty"`
	PeakHeap    uint64  `json:"peak_heap_bytes,omitempty"`
	WalBytesAvg float64 `json:"wal_bytes_avg,omitempty"`
	WalSyncs    int64   `json:"wal_syncs,omitempty"`
	Plan        string  `json:"plan,omitempty"`
	BuildMs     float64 `json:"build_ms,omitempty"`
	IndexBytes  int64   `json:"index_bytes,omitempty"`
	WriterOps   int     `json:"writer_ops,omitempty"`
	WriterMaxMs float64 `json:"writer_max_ms,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// percentile returns the nearest-rank percentile of samples in milliseconds.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// summarizeSamples fills the latency fields of r from raw samples.
func summarizeSamples(r *result, samples []float64) {
	s := append([]float64(nil), samples...)
	sort.Float64s(s)
	r.Samples = len(s)
	if len(s) == 0 {
		return
	}
	r.MinMs = s[0]
	r.MaxMs = s[len(s)-1]
	r.P50Ms = percentile(s, 0.50)
	r.P95Ms = percentile(s, 0.95)
	r.P99Ms = percentile(s, 0.99)
}

// appendResult appends one JSON line to dir/results.jsonl.
func appendResult(dir string, r result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "results.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// cmdSummarize writes summary.md under the output directory from every
// results.jsonl found below it. The tables are generated, not hand-edited.
func cmdSummarize(e *env) error {
	var rows []result
	err := filepath.WalkDir(e.out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "results.jsonl" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var r result
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			rows = append(rows, r)
		}
		return sc.Err()
	})
	if err != nil {
		return err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Dataset != b.Dataset {
			return a.Dataset < b.Dataset
		}
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		if a.Mode != b.Mode {
			return a.Mode < b.Mode
		}
		return a.Config < b.Config
	})
	var sb strings.Builder
	sb.WriteString("| dataset | workload | mode | config | samples | p50 ms | p95 ms | p99 ms | max ms | rows | bytes | WAL bytes avg | syncs | peak heap |\n")
	sb.WriteString("|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %d | %.2f | %.2f | %.2f | %.2f | %d | %d | %.0f | %d | %d |\n",
			r.Dataset, r.Workload, r.Mode, r.Config, r.Samples, r.P50Ms, r.P95Ms, r.P99Ms, r.MaxMs,
			r.Rows, r.Bytes, r.WalBytesAvg, r.WalSyncs, r.PeakHeap)
	}
	path := filepath.Join(e.out, "summary.md")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d rows)\n", path, len(rows))
	return nil
}
