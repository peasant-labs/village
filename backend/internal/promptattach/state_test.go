package promptattach

import "testing"

// TestTransitionTableMatchesFixture proves the Go table and the fixture agree
// on every ordered pair AND that the fixture covers the complete cross-product,
// so a new state without fixture rows fails instead of silently reducing
// coverage. This is the pure half of the transition acceptance; the database
// half lives behind the integration build tag.
func TestTransitionTableMatchesFixture(t *testing.T) {
	cases := loadTransitionCases(t)

	if got, want := len(cases), len(All)*len(All); got != want {
		t.Fatalf("transitions fixture has %d rows, want %d (every ordered pair of the %d-state menu)", got, want, len(All))
	}

	pairs := make(map[string]int, len(cases))
	for _, c := range cases {
		if err := c.From.Validate(); err != nil {
			t.Fatalf("fixture row %q has an out-of-menu source: %v", c.Name, err)
		}
		if err := c.To.Validate(); err != nil {
			t.Fatalf("fixture row %q has an out-of-menu target: %v", c.Name, err)
		}
		key := string(c.From) + "->" + string(c.To)
		if _, repeated := pairs[key]; repeated {
			t.Fatalf("transitions fixture names pair %s more than once", key)
		}
		pairs[key] = 1
		if got := CanTransition(c.From, c.To); got != c.Allowed {
			t.Errorf("CanTransition(%s, %s) = %v, want %v (fixture row %q)", c.From, c.To, got, c.Allowed, c.Name)
		}
	}

	for _, from := range All {
		for _, to := range All {
			key := string(from) + "->" + string(to)
			if pairs[key] == 0 {
				t.Errorf("transitions fixture omits pair %s; every ordered pair must be present", key)
			}
		}
	}
}

// TestCanTransitionFailsClosedOnUnknownState proves a state outside the menu
// permits nothing, in either position, rather than being guessed at.
func TestCanTransitionFailsClosedOnUnknownState(t *testing.T) {
	if CanTransition("merged", Attached) {
		t.Error("an unknown source state must permit no transition")
	}
	if CanTransition(Attached, "merged") {
		t.Error("an unknown target state must be refused")
	}
	if CanTransition("", "") {
		t.Error("empty states must permit no transition")
	}
}

// TestStateValidateRejectsOffMenu pins the fail-closed trust boundary.
func TestStateValidateRejectsOffMenu(t *testing.T) {
	for _, rejected := range []string{"", "REQUESTED", "requested ", " merged", "open"} {
		if err := State(rejected).Validate(); err == nil {
			t.Errorf("State(%q).Validate() accepted a value outside the menu", rejected)
		}
	}
	for _, accepted := range All {
		if err := accepted.Validate(); err != nil {
			t.Errorf("menu member %q was rejected: %v", accepted, err)
		}
	}
}

// TestCheckModeMenu pins the check-mode menu and its fail-closed validation.
func TestCheckModeMenu(t *testing.T) {
	if got, want := len(AllCheckModes), 2; got != want {
		t.Fatalf("check-mode menu has %d members, want %d", got, want)
	}
	for _, mode := range AllCheckModes {
		if err := mode.Validate(); err != nil {
			t.Errorf("check-mode menu member %q was rejected: %v", mode, err)
		}
	}
	for _, rejected := range []string{"", "INFORMATIONAL", "required ", "blocking"} {
		if err := CheckMode(rejected).Validate(); err == nil {
			t.Errorf("CheckMode(%q).Validate() accepted a value outside the menu", rejected)
		}
	}
}
