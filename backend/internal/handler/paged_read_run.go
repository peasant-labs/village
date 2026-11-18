package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/contentstate"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// This file is the operator surface of the paged-read parity check: the
// content-free shape census, the bounded sampler, and the report. The report is
// key=value lines. It names transcripts only in the sample list, which is
// written before any content is read so the subjects are on record first, and
// it never carries a byte of content: findings are a kind, a partition and a
// position.

// PagedReadCheckMode is the closed set of check-mode operations.
type PagedReadCheckMode uint8

const (
	// PagedReadCheckCensus counts stored rows per read shape. It reads no
	// object and no content.
	PagedReadCheckCensus PagedReadCheckMode = iota + 1
	// PagedReadCheckSample reads a bounded sample of every non-empty shape
	// both ways and compares them.
	PagedReadCheckSample
	// PagedReadCheckTranscripts reads the named transcripts both ways, as their
	// owners, and compares them.
	PagedReadCheckTranscripts
)

func (m PagedReadCheckMode) String() string {
	switch m {
	case PagedReadCheckCensus:
		return "census"
	case PagedReadCheckSample:
		return "sample"
	case PagedReadCheckTranscripts:
		return "transcripts"
	default:
		return fmt.Sprintf("PagedReadCheckMode(%d)", uint8(m))
	}
}

// ReadsContent reports whether the mode decrypts stored transcripts and so
// needs object storage, the key authority and a recorded authorization.
func (m PagedReadCheckMode) ReadsContent() bool { return m != PagedReadCheckCensus }

// The sample bounds. A run never reads more than these, whatever the census.
const (
	pagedReadSamplePerShape = 5
	pagedReadSampleRows     = 40
	pagedReadSampleBytes    = int64(256 << 20)
	pagedReadSampleWall     = 10 * time.Minute
	// pagedReadMaxNamedTranscripts bounds the transcripts mode by the same
	// row ceiling as a sample.
	pagedReadMaxNamedTranscripts = pagedReadSampleRows
)

// PagedReadCheckOptions is a validated check-mode request.
type PagedReadCheckOptions struct {
	Mode             PagedReadCheckMode
	Transcripts      []uuid.UUID
	AuthorizationRef string
}

// authorizationRefSyntax keeps the echoed reference to a short opaque token,
// so the report cannot carry free text.
var authorizationRefSyntax = regexp.MustCompile(`^[A-Za-z0-9._:/#-]{1,200}$`)

// ParsePagedReadCheckOptions validates the operator's arguments before any
// authority is loaded.
func ParsePagedReadCheckOptions(mode, transcripts, authorizationRef string) (PagedReadCheckOptions, error) {
	const where = "paged-read check arguments were refused in handler.ParsePagedReadCheckOptions before any authority was loaded; nothing was read"
	var opts PagedReadCheckOptions
	switch mode {
	case PagedReadCheckCensus.String():
		opts.Mode = PagedReadCheckCensus
	case PagedReadCheckSample.String():
		opts.Mode = PagedReadCheckSample
	case PagedReadCheckTranscripts.String():
		opts.Mode = PagedReadCheckTranscripts
	default:
		return opts, fmt.Errorf("%s: the mode is not one of census, sample or transcripts; select one of them", where)
	}
	if transcripts != "" && opts.Mode != PagedReadCheckTranscripts {
		return opts, fmt.Errorf("%s: transcript ids were given to the %s mode; give ids only with -check-paged-reads=transcripts", where, opts.Mode)
	}
	if opts.Mode == PagedReadCheckTranscripts {
		seen := map[uuid.UUID]bool{}
		for _, raw := range strings.Split(transcripts, ",") {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil {
				return opts, fmt.Errorf("%s: a transcript id is not a UUID; give a comma-separated list of transcript UUIDs", where)
			}
			if !seen[id] {
				seen[id] = true
				opts.Transcripts = append(opts.Transcripts, id)
			}
		}
		if len(opts.Transcripts) > pagedReadMaxNamedTranscripts {
			return opts, fmt.Errorf("%s: %d transcripts were named and a run reads at most %d; split the list into several authorized runs", where, len(opts.Transcripts), pagedReadMaxNamedTranscripts)
		}
	}
	if opts.Mode.ReadsContent() {
		if !authorizationRefSyntax.MatchString(authorizationRef) {
			return opts, fmt.Errorf("%s: the %s mode reads stored transcripts and needs the recorded operator authorization; pass -check-authorization-ref with the reference recorded for this run (letters, digits and ._:/#- only)", where, opts.Mode)
		}
		opts.AuthorizationRef = authorizationRef
	} else if authorizationRef != "" {
		return opts, fmt.Errorf("%s: the census reads no content and takes no authorization reference; remove -check-authorization-ref", where)
	}
	return opts, nil
}

// rowShapeFacts are the content-free facts of one stored row, as the census
// and the sampler read them.
type rowShapeFacts struct {
	Identity     rowIdentityFacts
	Present      bool // content_state is present
	Public       bool
	Shared       bool
	MemberRoles  []string // roles of non-owner members of approved-share collectives
	PreviewOwner bool     // a non-owner owns a collective the transcript is submitted to
}

// audienceKinds is every audience kind through which some viewer reaches the
// row, decided by the same role rule transcriptViewAccess applies.
func (f rowShapeFacts) audienceKinds() []transcriptAudienceKind {
	if f.Public {
		return []transcriptAudienceKind{audiencePublic, audienceOwner}
	}
	kinds := []transcriptAudienceKind{audienceOwner}
	if f.Shared {
		for _, role := range f.MemberRoles {
			if canReadData(role, "contributors") {
				kinds = append(kinds, audienceCollectiveMember)
				break
			}
		}
	}
	if f.PreviewOwner {
		kinds = append(kinds, audienceCollectiveOwnerPreview)
	}
	return kinds
}

// collectiveOwnerRole is the group_members role that owns a collective.
const collectiveOwnerRole = "owner"

// shapeFactColumns selects the facts of rowShapeFacts for transcripts t, in
// order. $1 is the hash syntax, $2 the present content state, $3 and $4 the
// public and shared visibilities, $5 the approved share status, $6 the
// collective owner role. It selects no hash, key, size value or content.
const shapeFactColumns = `
	t.content_hash IS NULL,
	COALESCE(t.content_hash ~ $1, false),
	CASE WHEN t.blob_size_bytes IS NULL THEN 0 WHEN t.blob_size_bytes < 0 THEN 1 ELSE 2 END,
	t.segmented_identity_hash IS NOT NULL,
	t.segmented_identity_size IS NOT NULL,
	LEAST(t.content_revision, 2),
	t.publish_metadata_key IS NOT NULL,
	t.content_state = $2,
	t.visibility = $3,
	t.visibility = $4,
	COALESCE((SELECT array_agg(DISTINCT gm.role ORDER BY gm.role)
		FROM transcript_shares ts JOIN group_members gm ON gm.group_id = ts.group_id
		WHERE ts.transcript_id = t.id AND ts.status = $5 AND gm.user_id <> t.owner_id), '{}'::text[]),
	EXISTS (SELECT 1 FROM transcript_shares ts JOIN group_members gm ON gm.group_id = ts.group_id
		WHERE ts.transcript_id = t.id AND gm.role = $6 AND gm.user_id <> t.owner_id)`

const shapeFactCount = 12

// pagedReadCensusQuery is the one GROUP BY over the shape facts. It returns
// counts only.
const pagedReadCensusQuery = `SELECT ` + shapeFactColumns + `, count(*) FROM transcripts t GROUP BY 1,2,3,4,5,6,7,8,9,10,11,12`

// pagedReadSampleQuery streams the same facts per row, with the row's id, owner
// and plaintext size for the sample bounds.
const pagedReadSampleQuery = `SELECT t.id, t.owner_id, t.blob_size_bytes, ` + shapeFactColumns + ` FROM transcripts t ORDER BY t.id`

func shapeFactArgs() []any {
	return []any{storage.CanonicalContentHashPattern, string(contentstate.Present), dbVisibilityPublic, dbVisibilityShared, string(ShareStatusApproved), collectiveOwnerRole}
}

func scanShapeFacts(dest []any, f *rowShapeFacts) []any {
	var size int32
	var revision int64
	f.Identity = rowIdentityFacts{}
	return append(dest, &f.Identity.HashNull, &f.Identity.HashCanonical, &size, &f.Identity.SegmentedHash, &f.Identity.SegmentedSize, &revision, &f.Identity.Metadata, &f.Present, &f.Public, &f.Shared, &f.MemberRoles, &f.PreviewOwner)
}

// finishShapeFacts copies the scanned size class and revision into f. The scan
// targets for those two are the 3rd and 6th fact columns.
func finishShapeFacts(targets []any, offset int, f *rowShapeFacts) {
	f.Identity.Size = identitySizeClass(*targets[offset+2].(*int32))
	f.Identity.Revision = *targets[offset+5].(*int64)
}

// pagedReadCensus is the census result.
type pagedReadCensus struct {
	Shapes      map[transcriptReadShape]int64
	Unexplained int64
	Unavailable int64
}

// runPagedReadCensus counts rows per read shape with one GROUP BY. A row
// counts once per audience kind it is reachable through.
func runPagedReadCensus(ctx context.Context, pool *pgxpool.Pool) (pagedReadCensus, error) {
	census := pagedReadCensus{Shapes: map[transcriptReadShape]int64{}}
	rows, err := pool.Query(ctx, pagedReadCensusQuery, shapeFactArgs()...)
	if err != nil {
		return census, fmt.Errorf("the paged-read shape census query failed in handler.runPagedReadCensus; no count was produced; check database connectivity and that migrations are applied: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f rowShapeFacts
		var count int64
		targets := scanShapeFacts(nil, &f)
		if err := rows.Scan(append(targets, &count)...); err != nil {
			return census, fmt.Errorf("a paged-read census row could not be read in handler.runPagedReadCensus; no count was produced: %w", err)
		}
		finishShapeFacts(targets, 0, &f)
		census.add(f, count)
	}
	if err := rows.Err(); err != nil {
		return census, fmt.Errorf("the paged-read census ended early in handler.runPagedReadCensus; no count was produced: %w", err)
	}
	return census, nil
}

func (c *pagedReadCensus) add(f rowShapeFacts, count int64) {
	if !f.Present {
		c.Unavailable += count
		return
	}
	state, ok := classifyTranscriptRowState(f.Identity)
	if !ok {
		c.Unexplained += count
		return
	}
	for _, audience := range f.audienceKinds() {
		c.Shapes[transcriptReadShape{State: state, Audience: audience}] += count
	}
}

// pagedReadSubject is one transcript chosen to be read.
type pagedReadSubject struct {
	ID     uuid.UUID
	Owner  uuid.UUID
	Shape  transcriptReadShape
	Weight int64
}

// shapeWeight is the plaintext bytes a read of the row may hold. A state that
// refuses before any object read weighs nothing; an unknown size weighs the
// cache's conservative unknown weight.
func shapeWeight(state transcriptRowState, size pgtype.Int8) int64 {
	switch state {
	case rowStateHashOnly, rowStateMalformedHash, rowStateDivergent:
		return 0
	}
	if size.Valid && size.Int64 >= 0 {
		return size.Int64
	}
	return transcriptContentCacheUnknownWeight
}

// selectPagedReadSample streams every row's facts once, keeps at most
// pagedReadSamplePerShape candidates per shape in id order, and then takes them
// round-robin across shapes until a row or byte bound is reached.
func selectPagedReadSample(ctx context.Context, pool *pgxpool.Pool) ([]pagedReadSubject, error) {
	rows, err := pool.Query(ctx, pagedReadSampleQuery, shapeFactArgs()...)
	if err != nil {
		return nil, fmt.Errorf("the paged-read sample query failed in handler.selectPagedReadSample; nothing was read; check database connectivity: %w", err)
	}
	defer rows.Close()
	candidates := map[transcriptReadShape][]pagedReadSubject{}
	for rows.Next() {
		var id, owner pgtype.UUID
		var size pgtype.Int8
		var f rowShapeFacts
		targets := scanShapeFacts([]any{&id, &owner, &size}, &f)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("a paged-read sample row could not be read in handler.selectPagedReadSample; nothing was read: %w", err)
		}
		finishShapeFacts(targets, 3, &f)
		if !f.Present {
			continue
		}
		state, ok := classifyTranscriptRowState(f.Identity)
		if !ok {
			continue
		}
		for _, audience := range f.audienceKinds() {
			shape := transcriptReadShape{State: state, Audience: audience}
			if len(candidates[shape]) < pagedReadSamplePerShape {
				candidates[shape] = append(candidates[shape], pagedReadSubject{ID: uuid.UUID(id.Bytes), Owner: uuid.UUID(owner.Bytes), Shape: shape, Weight: shapeWeight(state, size)})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("the paged-read sample scan ended early in handler.selectPagedReadSample; nothing was read: %w", err)
	}
	var sample []pagedReadSubject
	var bytes int64
	for round := 0; round < pagedReadSamplePerShape && len(sample) < pagedReadSampleRows; round++ {
		for _, shape := range allTranscriptReadShapes() {
			if round >= len(candidates[shape]) || len(sample) >= pagedReadSampleRows {
				continue
			}
			subject := candidates[shape][round]
			if bytes+subject.Weight > pagedReadSampleBytes {
				continue
			}
			bytes += subject.Weight
			sample = append(sample, subject)
		}
	}
	return sample, nil
}

// errNoAudienceViewer is a subject whose audience has no viewer left to read
// it as; the subject is reported skipped, not compared.
var errNoAudienceViewer = errors.New("no viewer currently reaches this transcript through its audience kind")

// audienceViewer chooses who reads a subject: nobody for public, the owner for
// owner, one current member or collective owner for the collective kinds. The
// choice is confirmed against transcriptViewAccess before any read.
func (h *Handler) audienceViewer(ctx context.Context, subject pagedReadSubject) (*AuthUser, error) {
	row, err := h.queries.GetTranscriptByID(ctx, toPgUUID(subject.ID))
	if err != nil {
		return nil, err
	}
	var viewer *AuthUser
	switch subject.Shape.Audience {
	case audiencePublic:
	case audienceOwner:
		viewer = &AuthUser{ID: subject.Owner}
	case audienceCollectiveMember:
		members, err := h.pool.Query(ctx, `SELECT gm.user_id, gm.role FROM transcript_shares ts JOIN group_members gm ON gm.group_id = ts.group_id
			WHERE ts.transcript_id = $1 AND ts.status = $2 AND gm.user_id <> $3 ORDER BY gm.user_id LIMIT 100`, row.ID, string(ShareStatusApproved), row.OwnerID)
		if err != nil {
			return nil, err
		}
		for members.Next() {
			var user pgtype.UUID
			var role string
			if err := members.Scan(&user, &role); err != nil {
				members.Close()
				return nil, err
			}
			if canReadData(role, "contributors") {
				viewer = &AuthUser{ID: uuid.UUID(user.Bytes)}
				break
			}
		}
		members.Close()
	case audienceCollectiveOwnerPreview:
		owners, err := h.queries.ListGroupOwnersForTranscript(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		for _, owner := range owners {
			if owner != row.OwnerID {
				viewer = &AuthUser{ID: uuid.UUID(owner.Bytes)}
				break
			}
		}
	}
	if (subject.Shape.Audience != audiencePublic && viewer == nil) || !h.canViewTranscript(ctx, viewer, row) {
		return nil, errNoAudienceViewer
	}
	return viewer, nil
}

// pagedReadReport writes the check's key=value lines.
type pagedReadReport struct {
	out io.Writer
	err error
}

func (r *pagedReadReport) line(format string, args ...any) {
	if r.err == nil {
		_, r.err = fmt.Fprintf(r.out, format+"\n", args...)
	}
}

func (r *pagedReadReport) census(c pagedReadCensus) {
	for _, shape := range allTranscriptReadShapes() {
		r.line("census shape=%s rows=%d", shape, c.Shapes[shape])
	}
	r.line("census unexplained=%d unavailable=%d", c.Unexplained, c.Unavailable)
}

func (r *pagedReadReport) result(subject int, findings []pagedReadFinding) {
	summary := summarizeFindings(findings)
	outcome := "clean"
	if !summary.Clean() {
		outcome = "broken"
	}
	parts := []string{fmt.Sprintf("result subject=%d outcome=%s", subject, outcome)}
	for _, kind := range allPagedReadFindingKinds() {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, summary.Counts[kind]))
	}
	r.line("%s", strings.Join(parts, " "))
	for _, f := range findings {
		r.line("finding subject=%d kind=%s partition=%s ordinal=%d position=%d", subject, f.Kind, partitionLabel(f.Partition), f.Ordinal, f.Position)
	}
}

// RunPagedReadCensus writes the content-free shape census. It fails when any
// stored row has a state the closed set does not explain.
func RunPagedReadCensus(ctx context.Context, pool *pgxpool.Pool, out io.Writer) error {
	report := &pagedReadReport{out: out}
	report.line("paged_read_check mode=%s", PagedReadCheckCensus)
	census, err := runPagedReadCensus(ctx, pool)
	if err != nil {
		return err
	}
	report.census(census)
	if report.err != nil {
		return fmt.Errorf("the paged-read census report could not be written in handler.RunPagedReadCensus: %w", report.err)
	}
	if census.Unexplained != 0 {
		return fmt.Errorf("the paged-read census found %d stored rows whose content-identity state is outside the closed set in handler.RunPagedReadCensus; nothing was changed; investigate those rows before activating the paged reads", census.Unexplained)
	}
	return nil
}

// RunPagedReadCheck runs a content-reading check mode: the self-test first,
// then the subjects, each read both ways through the production handlers and
// compared. It fails when the self-test fails or any subject breaks a rule.
func (h *Handler) RunPagedReadCheck(ctx context.Context, out io.Writer, opts PagedReadCheckOptions) error {
	if !opts.Mode.ReadsContent() {
		return RunPagedReadCensus(ctx, h.pool, out)
	}
	report := &pagedReadReport{out: out}
	report.line("paged_read_check mode=%s authorization_ref=%s", opts.Mode, opts.AuthorizationRef)
	if err := selfTestPagedReadComparer(); err != nil {
		report.line("self_test=failed")
		return err
	}
	report.line("self_test=passed perturbations=%d", len(allPagedReadPerturbations()))

	ctx, cancel := context.WithTimeout(ctx, pagedReadSampleWall)
	defer cancel()
	var subjects []pagedReadSubject
	switch opts.Mode {
	case PagedReadCheckSample:
		census, err := runPagedReadCensus(ctx, h.pool)
		if err != nil {
			return err
		}
		report.census(census)
		subjects, err = selectPagedReadSample(ctx, h.pool)
		if err != nil {
			return err
		}
	case PagedReadCheckTranscripts:
		for _, id := range opts.Transcripts {
			row, err := h.queries.GetTranscriptByID(ctx, toPgUUID(id))
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("a named transcript does not exist in handler.RunPagedReadCheck; nothing was read; correct the transcript list")
			}
			if err != nil {
				return fmt.Errorf("a named transcript could not be looked up in handler.RunPagedReadCheck; nothing was read; retry: %w", err)
			}
			state, _ := classifyTranscriptRowState(rowIdentityFactsOf(row))
			subjects = append(subjects, pagedReadSubject{ID: id, Owner: uuid.UUID(row.OwnerID.Bytes), Shape: transcriptReadShape{State: state, Audience: audienceOwner}})
		}
	}
	// The sample list is on record before any content is read.
	for i, subject := range subjects {
		report.line("sample subject=%d shape=%s transcript=%s", i+1, subject.Shape, subject.ID)
	}
	if report.err != nil {
		return fmt.Errorf("the paged-read sample list could not be written in handler.RunPagedReadCheck; nothing was read: %w", report.err)
	}

	routes := h.pagedReadRoutes()
	clean, broken, skipped := 0, 0, 0
	busyDeadline := false
	for i, subject := range subjects {
		if busyDeadline {
			skipped++
			report.line("skipped subject=%d reason=busy", i+1)
			continue
		}
		viewer, err := h.audienceViewer(ctx, subject)
		if err != nil {
			skipped++
			report.line("skipped subject=%d reason=no_viewer", i+1)
			continue
		}
		obs, err := h.observePagedReads(ctx, routes, subject.ID, viewer)
		if errors.Is(err, errPagedReadBusy) {
			busyDeadline = true
			skipped++
			report.line("skipped subject=%d reason=busy", i+1)
			continue
		}
		if err != nil {
			return fmt.Errorf("subject %d could not be read in handler.RunPagedReadCheck after %d subjects; the run stopped and its report is partial; retry within the bounds: %w", i+1, i, err)
		}
		findings := comparePagedReads(obs, schema.MaxTranscriptDirectoryRefs)
		obs = pagedReadObservation{} // release the content before the next read
		report.result(i+1, findings)
		if len(findings) == 0 {
			clean++
		} else {
			broken++
		}
	}
	report.line("summary subjects=%d clean=%d broken=%d skipped=%d", len(subjects), clean, broken, skipped)
	if report.err != nil {
		return fmt.Errorf("the paged-read report could not be written in handler.RunPagedReadCheck: %w", report.err)
	}
	if broken != 0 {
		return fmt.Errorf("the paged-read check found rule breaks in %d of %d subjects in handler.RunPagedReadCheck; nothing was changed; stop the rollout and read the finding lines", broken, len(subjects))
	}
	return nil
}

// rowIdentityFactsOf reads the identity facts of one loaded row, the same
// facts the census selects.
func rowIdentityFactsOf(row sqlc.Transcript) rowIdentityFacts {
	f := rowIdentityFacts{
		HashNull:      !row.ContentHash.Valid,
		HashCanonical: row.ContentHash.Valid && storedContentHashSyntax.MatchString(row.ContentHash.String),
		SegmentedHash: row.SegmentedIdentityHash.Valid,
		SegmentedSize: row.SegmentedIdentitySize.Valid,
		Revision:      row.ContentRevision,
		Metadata:      row.PublishMetadataKey.Valid,
	}
	switch {
	case !row.BlobSizeBytes.Valid:
		f.Size = identitySizeNull
	case row.BlobSizeBytes.Int64 < 0:
		f.Size = identitySizeNegative
	default:
		f.Size = identitySizeUsable
	}
	return f
}
