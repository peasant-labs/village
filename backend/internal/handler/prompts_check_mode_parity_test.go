package handler

import (
	"reflect"
	"sort"
	"testing"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// TestPromptsCheckModeMenuParity ties the mode menu's three copies together.
//
// The closed set lives in the served contract (which refuses a body outside it),
// in `promptattach.CheckMode` (which the group update validates against and the
// attachment lifecycle reads), and in the database CHECK that migration 037
// installed. The database CHECK is already tied to the Go menu by the migration
// test; this ties the contract to it, so a one-sided widening cannot make the
// write path reject a value the read path accepts, or the reverse.
func TestPromptsCheckModeMenuParity(t *testing.T) {
	contract := make([]string, 0, len(schema.AllVillagePromptsCheckModes))
	for _, mode := range schema.AllVillagePromptsCheckModes {
		contract = append(contract, string(mode))
	}
	engine := make([]string, 0, len(promptattach.AllCheckModes))
	for _, mode := range promptattach.AllCheckModes {
		engine = append(engine, string(mode))
	}
	sort.Strings(contract)
	sort.Strings(engine)
	if !reflect.DeepEqual(contract, engine) {
		t.Fatalf("the contract's prompts-check-mode menu %v is not the engine menu %v; "+
			"widening one without the other makes the update path and the read path disagree",
			contract, engine)
	}
}
