package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// inCollectiveAuthorityTx serializes the final decision with policy and member
// writers. Transcript publish locks, when needed, precede these sorted,
// deduplicated transaction locks. No caller acquires a publish lock while these
// locks are held; attachment refresh runs only after commit.
func (h *Handler) inCollectiveAuthorityTx(ctx context.Context, conn *pgxpool.Conn, actor pgtype.UUID, groups []pgtype.UUID, fn func(context.Context, Querier) error) error {
	budget := h.collectiveAuthorityTimeout
	if budget <= 0 {
		budget = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if !actor.Valid {
		return errors.New("collective mutation requires an authenticated actor before opening its transaction; sign in and retry")
	}
	keys := []string{"village:collective-actor:" + uuid.UUID(actor.Bytes).String()}
	seen := map[string]bool{}
	for _, group := range groups {
		key := "village:collective-authority:" + uuid.UUID(group.Bytes).String()
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	var beginner txBeginner
	if conn != nil {
		beginner = conn
	} else if h.pool != nil {
		beginner = h.pool
	}
	return h.inTxRawOnConn(ctx, beginner, uuid.UUID(actor.Bytes).String(), func(q Querier, tx pgx.Tx) error {
		if tx != nil {
			// PostgreSQL cancels blocked statements before the overall context
			// expires, leaving time to roll back and unlock the pinned session.
			statementBudget := budget / 2
			if statementBudget < time.Millisecond {
				statementBudget = time.Millisecond
			}
			limit := fmt.Sprintf("%dms", statementBudget.Milliseconds())
			if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout',$1,true), set_config('statement_timeout',$1,true)", limit); err != nil {
				return err
			}
			for _, key := range keys {
				if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key); err != nil {
					return fmt.Errorf("lock collective authority before mutation; nothing committed, retry the request: %w", err)
				}
			}
		}
		return fn(ctx, q)
	})
}

// collectiveMutationResponse delays HTTP success until the transaction commits.
// A handler refusal rolls back any earlier writes in that same operation.
type collectiveMutationResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *collectiveMutationResponse) Header() http.Header { return b.header }
func (b *collectiveMutationResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *collectiveMutationResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

var errCollectiveMutationRefused = errors.New("collective mutation refused")

// Authority checks retain their role/policy rows until commit, even when a
// foreign-key account cascade (rather than a collective route) removes them.
// Ordinary browsing continues to use non-locking queries and read-only snapshots.
type collectiveAuthorityQueries struct{ Querier }

func (q collectiveAuthorityQueries) GetGroupMember(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
	return q.Querier.GetGroupMemberForShare(ctx, sqlc.GetGroupMemberForShareParams{GroupID: arg.GroupID, UserID: arg.UserID})
}

func (q collectiveAuthorityQueries) GetGroupByID(ctx context.Context, id pgtype.UUID) (sqlc.Group, error) {
	return q.Querier.GetGroupByIDForShare(ctx, id)
}

type shareOperationRefusal struct {
	status  int
	message string
}

func (e *shareOperationRefusal) Error() string { return e.message }

// finalShareStatus uses the same acceptance rule as preflight, but reads its
// inputs again on the mutation transaction after acquiring authority locks.
func finalShareStatus(ctx context.Context, q Querier, user *AuthUser, groupID pgtype.UUID) (ShareStatus, error) {
	member, err := q.GetGroupMemberForShare(ctx, sqlc.GetGroupMemberForShareParams{GroupID: groupID, UserID: user.PgID()})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && member.Role != "owner" && member.Role != "member" && member.Role != "contributor") {
		return "", &shareOperationRefusal{http.StatusForbidden, "Current accepted membership is required for every requested collective. Nothing was submitted; join the collective or correct the selection and explicitly retry."}
	}
	if err != nil {
		return "", err
	}
	group, err := q.GetGroupByIDForShare(ctx, groupID)
	if err != nil {
		return "", err
	}
	status, refusal := shareStatusForGroup(ctx, q, user, group)
	if refusal != nil {
		return "", &shareOperationRefusal{http.StatusForbidden, refusal.Message}
	}
	return status, nil
}

// mutateCollective runs the existing handler's reads and writes under the same
// authority transaction. The scoped handler has no GitHub client: external
// attachment work must not hold database authority or publish locks.
func (h *Handler) mutateCollective(w http.ResponseWriter, r *http.Request, mutation func(*Handler, http.ResponseWriter, *http.Request), refreshGrants bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}
	groupID := toPgUUID(id)
	// Drain bounded request bytes before taking database locks. The original
	// handler still performs its canonical contract validation and decoding,
	// against memory rather than a potentially slow client connection.
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		original := r.Body
		defer original.Close()
		raw, err := io.ReadAll(http.MaxBytesReader(w, original, maxContractBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds the %d byte limit", maxContractBodyBytes))
			} else {
				writeError(w, http.StatusBadRequest, "Invalid request body")
			}
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	buffer := &collectiveMutationResponse{header: make(http.Header)}
	var affected []sqlc.PullRequestAttachment
	err = h.inCollectiveAuthorityTx(r.Context(), nil, GetUser(r.Context()).PgID(), []pgtype.UUID{groupID}, func(ctx context.Context, q Querier) error {
		r = r.WithContext(ctx)
		if refreshGrants && h.gh != nil {
			// Capture before retracting share rows, or the affected attachments
			// would disappear from the grant join before their checks refresh.
			var err error
			affected, err = q.ListAttachmentsForCollectiveGrants(r.Context(), groupID)
			if err != nil {
				return err
			}
		}
		// These policy/member handlers need only configuration and queries.
		// Do not copy Handler: its unrelated caches contain live mutexes.
		scoped := &Handler{cfg: h.cfg, queries: collectiveAuthorityQueries{q}}
		mutation(scoped, buffer, r)
		if buffer.status >= http.StatusBadRequest {
			return errCollectiveMutationRefused
		}
		return nil
	})
	if err != nil && !errors.Is(err, errCollectiveMutationRefused) {
		writeError(w, http.StatusInternalServerError, "The collective mutation could not be confirmed by the database. Reload current membership and policy before explicitly retrying; no automatic retry was queued.")
		return
	}
	if err == nil && len(affected) > 0 {
		ctx, cancel := attachmentWorkContext(r.Context())
		defer cancel()
		if err := h.refreshKnownAttachments(ctx, affected); err != nil {
			slog.Error("attachment refresh failed after committed collective grant mutation", "operation", "collective_grant_refresh", "stage", "post_commit", "consequence", "the database change remains committed but an external check may be stale", "remediation", "retry the attachment refresh", "cause_type", fmt.Sprintf("%T", err))
		}
	}
	for key, values := range buffer.header {
		w.Header()[key] = values
	}
	if buffer.status != 0 {
		w.WriteHeader(buffer.status)
	}
	_, _ = w.Write(buffer.body.Bytes())
}
