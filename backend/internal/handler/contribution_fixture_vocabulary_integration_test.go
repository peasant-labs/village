//go:build integration

package handler

// The words the contribution fixtures use for their own settings. They are the
// fixture's vocabulary, not wire values: a case names the second user "other"
// and asks for a field to be "none" or "present", and the tests read those
// words back through these names.
const (
	// fixtureOwnerOther names the transcript owner who is not the viewer.
	fixtureOwnerOther = "other"
	// fixtureCollectiveUnknown asks for a well-formed collective id that names
	// no collective; fixtureCollectiveMalformed asks for one that is not an id.
	fixtureCollectiveUnknown   = "unknown"
	fixtureCollectiveMalformed = "malformed"
	// fixtureMalformedCollectiveID is the path segment a malformed collective
	// sends.
	fixtureMalformedCollectiveID = "not-an-identifier"
	// fixtureExpectAbsent and fixtureExpectPresent are the expectations for an
	// optional field.
	fixtureExpectAbsent  = "none"
	fixtureExpectPresent = "present"
	// fixtureNameLabel asks for the project name the server derives from the
	// project hash when no name is recorded.
	fixtureNameLabel = "label"
	// fixturePreLive asks for a transcript to be contributed before the request.
	fixturePreLive = "live"
)

// The concurrent writes a selection-write race can run while the selection's
// transaction is held.
const (
	selectionRaceMakePrivate       = "make_private"
	selectionRaceMakePrivateBefore = "make_private_before"
	selectionRaceAppendApproved    = "append_approved"
	selectionRaceWithdraw          = "withdraw"
	selectionRaceReject            = "reject"
	selectionRaceMove              = "move"
	selectionRaceSetAcceptance     = "set_acceptance"
	selectionRaceRemoveMembership  = "remove_membership"
	selectionRaceCompete           = "compete"
)
