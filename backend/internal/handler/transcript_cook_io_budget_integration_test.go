//go:build integration

package handler

import (
	_ "embed"
	"testing"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/cook_number_io_budget.yaml
var cookNumberIOBudgetYAML []byte

// This checks the actual encrypted source and partition build against RustFS.
// The counter wraps the production decrypting store, not the source or cook.
func TestCookNumberIOBudget(t *testing.T) {
	cases, err := decodeFixtureRows[struct {
		Name string `yaml:"name"`
	}](cookNumberIOBudgetYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Name != "number-cold-load-and-partition-no-extra-object-reads" {
		t.Fatal("required cook/number I/O budget case missing or duplicated")
	}
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	t.Run(cases[0].Name, func(t *testing.T) {
		pool := publishLockPool(t, 8)
		t.Cleanup(pool.Close)
		world := newOrderingWorld(t, pool, publishEvidenceObjects(t), 53001, []byte(fixture.Content))
		counter := &decryptionCountingStore{TranscriptBlobStore: world.handler.blobs}
		world.handler.blobs = counter
		opened, err := world.handler.openTranscriptContent(world.ctx, world.row(), func(sqlc.Transcript) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		cold := counter.count()
		if cold != 1 {
			t.Fatalf("cold load performed %d encrypted reads, want exactly one", cold)
		}
		partition, err := opened.Index.Partition(mainPartitionKey())
		if err != nil || len(partition.Rows) == 0 {
			t.Fatalf("numbering partition: %v", err)
		}
		if got := counter.count(); got != cold {
			t.Fatalf("numbering performed %d additional object reads", got-cold)
		}
	})
}
