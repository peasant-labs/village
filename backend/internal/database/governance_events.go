package database

// GovernanceEventType is the kind of governance change recorded in the
// transcript_governance_events_audit log (migration 026). It mirrors the seeded
// governance_event_types reference table — a closed, developer-defined taxonomy,
// and NOT a wire type (governance history lives only on village; peasant never
// sees these).
//
// The seeded table is the runtime source of truth (the audit log's event_type FK
// validates against it); these constants are the compile-time mirror. The two are
// kept in lockstep by the seed-sync assertion in
// TestMigration026_AppliesLicenseGovernance — add a type to one and that test
// fails until you add it to the other.
//
// NOTE: provisional home. When the Phase-2 governance writer lands (handler), this
// may move next to it; for now it lives beside the migration + seed it mirrors.
type GovernanceEventType string

const (
	EventPublished         GovernanceEventType = "published"
	EventLicenseChanged    GovernanceEventType = "license_changed"
	EventVisibilityChanged GovernanceEventType = "visibility_changed"
	// EventGovernanceChanged records a single action that moved BOTH axes (license
	// and visibility) at once, as one snapshot row rather than two per-axis rows.
	EventGovernanceChanged GovernanceEventType = "governance_changed"
	EventRetracted         GovernanceEventType = "retracted"
)

// IsValid reports whether e is one of the known governance event types.
func (e GovernanceEventType) IsValid() bool {
	switch e {
	case EventPublished, EventLicenseChanged, EventVisibilityChanged, EventGovernanceChanged, EventRetracted:
		return true
	}
	return false
}

func (e GovernanceEventType) String() string { return string(e) }

// AllGovernanceEventTypes is the canonical set of governance event types. It must
// match the rows seeded into governance_event_types in migration 026.
var AllGovernanceEventTypes = []GovernanceEventType{
	EventPublished, EventLicenseChanged, EventVisibilityChanged, EventGovernanceChanged, EventRetracted,
}
