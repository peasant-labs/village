package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/peasant-labs/schema"
)

// This file is the comparer's perturbed-read self-test. The check mode runs it
// before it reads any stored transcript, so a comparer that has stopped
// detecting a rule break refuses to report a clean production run. The subject
// is synthetic: its bytes are built here, indexed by the production content
// source (newTranscriptIndex and Partition, the same pass the paged reads
// serve from), and served as the paged reads would serve them. A clean read of
// it must produce no finding, and each perturbation must produce the finding
// kind it is a break of.

// pagedReadPerturbation is the closed set of breaks the self-test injects.
type pagedReadPerturbation uint8

const (
	perturbRows pagedReadPerturbation = iota + 1
	perturbRefSpan
	perturbOffByOnePage
	perturbRefusedShape
	perturbWrongNumber

	perturbationCount // sentinel; not a perturbation
)

func allPagedReadPerturbations() []pagedReadPerturbation {
	out := make([]pagedReadPerturbation, 0, int(perturbationCount)-1)
	for p := perturbRows; p < perturbationCount; p++ {
		out = append(out, p)
	}
	return out
}

func (p pagedReadPerturbation) String() string {
	switch p {
	case perturbRows:
		return "perturbed-rows"
	case perturbRefSpan:
		return "perturbed-ref-span"
	case perturbOffByOnePage:
		return "off-by-one-page"
	case perturbRefusedShape:
		return "refused-shape"
	case perturbWrongNumber:
		return "wrong-number"
	default:
		return fmt.Sprintf("pagedReadPerturbation(%d)", uint8(p))
	}
}

// detectedAs is the finding kind the perturbation is a break of.
func (p pagedReadPerturbation) detectedAs() pagedReadFindingKind {
	switch p {
	case perturbRows:
		return findingRows
	case perturbRefSpan:
		return findingRefSpan
	case perturbOffByOnePage:
		return findingPage
	case perturbRefusedShape:
		return findingRefusal
	case perturbWrongNumber:
		return findingNumber
	default:
		return 0
	}
}

const (
	// selfTestDirectoryCap pages the synthetic subject's directory so the
	// paging rule is exercised without ten thousand segments.
	selfTestDirectoryCap = 1
	// selfTestContentMarker is in every synthetic turn, so a report or log can
	// be searched for any byte of turn content.
	selfTestContentMarker = "paged-read-self-test-content"
	selfTestMainTurns     = 30
	selfTestTurnBytes     = 80 << 10
)

// selfTestSubject builds the synthetic document: a main partition large enough
// to cut into several segments, with a row the cooking rule removes and a
// duplicated row, and one earlier-history section.
func selfTestSubject() []byte {
	type turn struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Depth   int    `json:"depth"`
		Index   int    `json:"index"`
	}
	pad := strings.Repeat("x", selfTestTurnBytes)
	var main []turn
	for i := 0; i < selfTestMainTurns; i++ {
		role := "assistant"
		if i%4 == 0 {
			role = "user"
		}
		main = append(main, turn{Role: role, Content: fmt.Sprintf("%s %d %s", selfTestContentMarker, i, pad), Index: i})
	}
	// A short system banner is cooked out; an exact repeat of the row before
	// it is collapsed.
	main = append(main[:3], append([]turn{{Role: "system", Content: "short", Index: 1000}}, main[3:]...)...)
	main = append(main, turn{Role: main[len(main)-1].Role, Content: main[len(main)-1].Content, Index: 1001})
	history := []turn{
		{Role: "user", Content: selfTestContentMarker + " history one", Index: 0},
		{Role: "assistant", Content: selfTestContentMarker + " history reply", Index: 1},
		{Role: "user", Content: selfTestContentMarker + " history two", Index: 2},
	}
	type section struct {
		State string `json:"state"`
		Turns []turn `json:"turns"`
	}
	document := struct {
		ContractVersion schema.PushContractVersion `json:"contractVersion"`
		Kind            schema.ContentKind         `json:"kind"`
		SessionDetail   struct {
			ID             string    `json:"id"`
			Harness        string    `json:"harness"`
			Turns          []turn    `json:"turns"`
			EarlierHistory []section `json:"earlierHistory"`
		} `json:"sessionDetail"`
	}{ContractVersion: currentContractVersion, Kind: schema.ContentKindSessionDetail}
	document.SessionDetail.ID = "paged-read-self-test"
	document.SessionDetail.Harness = string(schema.HarnessCodex)
	document.SessionDetail.Turns = main
	document.SessionDetail.EarlierHistory = []section{{State: "uncertain_migrated", Turns: history}}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(fmt.Sprintf("the self-test subject could not be encoded in handler.selfTestSubject: %v", err))
	}
	return encoded
}

// servedSelfTestObservation serves the synthetic subject the way the paged
// reads serve it: rows, numbers and refs from the production index, the
// directory paged at selfTestDirectoryCap.
func servedSelfTestObservation() (pagedReadObservation, error) {
	content := selfTestSubject()
	index, err := newTranscriptIndex(transcriptIndexInput{Revision: 1, Bytes: content, Validation: schema.TranscriptReadPublishedStrict})
	if err != nil {
		return pagedReadObservation{}, err
	}
	obs := pagedReadObservation{ContentStatus: http.StatusOK, Content: content}
	keys := []partitionKey{mainPartitionKey()}
	for section := 0; section < index.HistorySections(); section++ {
		keys = append(keys, historyPartitionKey(section))
	}
	for _, key := range keys {
		part, err := index.Partition(key)
		if err != nil {
			return pagedReadObservation{}, err
		}
		observed := observedPartition{Key: key, DirectoryStatus: http.StatusOK, SegmentStatus: map[int]int{}, Segments: map[int]schema.PositionedSegment{}}
		total := len(part.Refs)
		readContext := schema.TranscriptReadContext{TotalSegments: total, TotalTurns: part.TotalTurns}
		if total <= selfTestDirectoryCap {
			observed.Pages = append(observed.Pages, schema.PositionedDirectory{Context: readContext, Segments: append([]schema.PositionedSegmentRef(nil), part.Refs...)})
		}
		for start := 0; total > selfTestDirectoryCap && start < total; start += selfTestDirectoryCap {
			number, size := start/selfTestDirectoryCap+1, selfTestDirectoryCap
			end := min(start+selfTestDirectoryCap, total)
			observed.Pages = append(observed.Pages, schema.PositionedDirectory{Context: readContext, Segments: append([]schema.PositionedSegmentRef(nil), part.Refs[start:end]...), DirectoryPage: &number, DirectoryPerPage: &size})
		}
		for _, ref := range part.Refs {
			segment := schema.PositionedSegment{Index: ref.Index, ByteLength: ref.ByteLength}
			for _, row := range part.Rows[ref.SourceFirstPosition : ref.SourceLastPosition+1] {
				raw, err := index.ReadTurnBytes(context.Background(), row.Start, row.End)
				if err != nil {
					return pagedReadObservation{}, err
				}
				turn, err := schema.NewTranscriptServedTurn(raw)
				if err != nil {
					return pagedReadObservation{}, err
				}
				segment.Rows = append(segment.Rows, schema.TranscriptSegmentRow{Position: row.ServedPosition, DisplayNumber: row.DisplayNumber, Turn: turn})
			}
			observed.SegmentStatus[ref.Index] = http.StatusOK
			observed.Segments[ref.Index] = segment
		}
		obs.Partitions = append(obs.Partitions, observed)
	}
	return obs, nil
}

// perturb applies one break to a freshly served observation.
func perturb(obs *pagedReadObservation, p pagedReadPerturbation) {
	main := &obs.Partitions[0]
	switch p {
	case perturbRows:
		segment := main.Segments[0]
		segment.Rows[1].Turn = segment.Rows[0].Turn
		main.Segments[0] = segment
	case perturbRefSpan:
		main.Pages[0].Segments[0].SourceLastPosition--
	case perturbOffByOnePage:
		page := &main.Pages[1]
		shifted := make([]schema.PositionedSegmentRef, 0, len(page.Segments))
		shifted = append(shifted, main.Pages[0].Segments[len(main.Pages[0].Segments)-1])
		shifted = append(shifted, page.Segments[:len(page.Segments)-1]...)
		page.Segments = shifted
	case perturbRefusedShape:
		main.SegmentStatus[1] = http.StatusInternalServerError
		delete(main.Segments, 1)
	case perturbWrongNumber:
		segment := main.Segments[0]
		segment.Rows[0].DisplayNumber = "999"
		main.Segments[0] = segment
	}
}

// cloneObservation copies everything perturb can change, so one served
// observation is perturbed many times without being rebuilt.
func cloneObservation(in pagedReadObservation) pagedReadObservation {
	out := in
	out.Partitions = make([]observedPartition, len(in.Partitions))
	for i, part := range in.Partitions {
		copied := part
		copied.Pages = make([]schema.PositionedDirectory, len(part.Pages))
		for j, page := range part.Pages {
			page.Segments = append([]schema.PositionedSegmentRef(nil), page.Segments...)
			copied.Pages[j] = page
		}
		copied.SegmentStatus = make(map[int]int, len(part.SegmentStatus))
		for k, v := range part.SegmentStatus {
			copied.SegmentStatus[k] = v
		}
		copied.Segments = make(map[int]schema.PositionedSegment, len(part.Segments))
		for k, segment := range part.Segments {
			segment.Rows = append([]schema.TranscriptSegmentRow(nil), segment.Rows...)
			copied.Segments[k] = segment
		}
		out.Partitions[i] = copied
	}
	return out
}

// pagedReadSelfTestResult is one self-test read's findings.
type pagedReadSelfTestResult struct {
	Perturbation pagedReadPerturbation // zero for the clean read
	Findings     []pagedReadFinding
}

// runPagedReadSelfTest reads the synthetic subject clean and under every
// perturbation.
func runPagedReadSelfTest() ([]pagedReadSelfTestResult, error) {
	clean, err := servedSelfTestObservation()
	if err != nil {
		return nil, fmt.Errorf("the paged-read self-test could not index its synthetic subject in handler.runPagedReadSelfTest; the comparer is unproven, so no stored transcript was read; report this as a defect: %w", err)
	}
	if len(clean.Partitions[0].Pages) < 2 || len(clean.Partitions[0].Segments) < 2 {
		return nil, fmt.Errorf("the paged-read self-test subject cut into %d pages and %d segments in handler.runPagedReadSelfTest, too few to page and perturb; no stored transcript was read; report this as a defect", len(clean.Partitions[0].Pages), len(clean.Partitions[0].Segments))
	}
	results := []pagedReadSelfTestResult{{Findings: comparePagedReads(clean, selfTestDirectoryCap)}}
	for _, p := range allPagedReadPerturbations() {
		obs := cloneObservation(clean)
		perturb(&obs, p)
		results = append(results, pagedReadSelfTestResult{Perturbation: p, Findings: comparePagedReads(obs, selfTestDirectoryCap)})
	}
	return results, nil
}

// selfTestPagedReadComparer is the gate the check mode runs first: the clean
// read is clean and every perturbation is detected as its own kind.
func selfTestPagedReadComparer() error {
	results, err := runPagedReadSelfTest()
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Perturbation == 0 {
			if len(result.Findings) != 0 {
				return fmt.Errorf("the paged-read self-test found %d rule breaks in an unperturbed synthetic read in handler.selfTestPagedReadComparer; the comparer disagrees with the production index, so no stored transcript was read; report this as a defect", len(result.Findings))
			}
			continue
		}
		if summarizeFindings(result.Findings).Counts[result.Perturbation.detectedAs()] == 0 {
			return fmt.Errorf("the paged-read self-test did not detect the %s perturbation as a %s break in handler.selfTestPagedReadComparer; a clean report could hide that break, so no stored transcript was read; report this as a defect", result.Perturbation, result.Perturbation.detectedAs())
		}
	}
	return nil
}
