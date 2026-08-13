package handler

// Unit coverage for inTxAs's fail-closed actor gate (REVIEW-C A1): a non-Valid
// pgtype.UUID renders as 00000000-… — the SYSTEM actor — so it must be rejected
// BEFORE any transaction work, or an uninitialized actor would silently
// impersonate the system. The pool==nil seam makes the guard unit-testable.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestInTxAs_RejectsUnsetActor_FailClosed(t *testing.T) {
	h := &Handler{queries: &mockQuerier{}} // pool == nil: unit seam

	called := false
	err := h.inTxAs(context.Background(), pgtype.UUID{}, func(q Querier) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("inTxAs accepted a non-Valid actor; want fail-closed error")
	}
	if !strings.Contains(err.Error(), "fail-closed") || !strings.Contains(err.Error(), "inTxAsSystem") {
		t.Errorf("error must name the failure mode and the sanctioned system route: %q", err.Error())
	}
	if called {
		t.Error("fn ran despite the rejected actor — the gate must fire before any work")
	}
}

func TestInTxAs_ValidActorRunsFn(t *testing.T) {
	h := &Handler{queries: &mockQuerier{}}
	called := false
	err := h.inTxAs(context.Background(), pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, func(q Querier) error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("valid actor: err=%v called=%v, want nil/true", err, called)
	}
}
