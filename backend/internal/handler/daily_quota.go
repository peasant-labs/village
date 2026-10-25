package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// The daily limits, each counted per account and UTC day in user_daily_quotas
// so that a restart or another instance cannot reset them.
const (
	quotaInvitationSent    = "invitation_sent"
	quotaInvitationEmail   = "invitation_email"
	quotaVerificationEmail = "verification_email"
)

// takeDailyQuota takes one slot of a daily limit for an account. It reports
// false, counting nothing, when the limit is already reached. Run on a
// transaction's queries, the slot is returned if the transaction rolls back.
func takeDailyQuota(ctx context.Context, q Querier, account pgtype.UUID, quota string, limit int) (bool, error) {
	if err := q.DeleteStaleDailyQuotas(ctx); err != nil {
		return false, fmt.Errorf("clear old %s counts: %w", quota, err)
	}
	_, err := q.TakeDailyQuota(ctx, sqlc.TakeDailyQuotaParams{UserID: account, Quota: quota, DailyLimit: int32(limit)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("count the %s limit: %w", quota, err)
	}
	return true, nil
}
