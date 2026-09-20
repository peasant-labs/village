package database

import (
	"strings"
	"testing"
)

// TestMigration042AttachmentTranscriptReverseIndex pins the index and its
// column without a database.
//
// The shape is the point: the visibility trigger reads the attachments binding
// one transcript, and the binding table's primary key is
// (attachment_id, transcript_id), so a lookup by transcript alone has no
// leading column to use. An index on the wrong column, or on the pair, would
// not serve that read.
func TestMigration042AttachmentTranscriptReverseIndex(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/042_attachment_transcript_reverse_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/042_attachment_transcript_reverse_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	// The statement, not the file: the migration's prose explains why the primary
	// key does not serve this lookup, and reading the whole file would match it.
	upSQL := string(up)
	statementStart := strings.Index(upSQL, "CREATE INDEX")
	if statementStart < 0 {
		t.Fatal("migration 042 up SQL declares no CREATE INDEX")
	}
	statement := upSQL[statementStart:]
	if end := strings.Index(statement, ";"); end >= 0 {
		statement = statement[:end]
	}

	if !strings.Contains(statement, "CREATE INDEX idx_pull_request_attachment_transcripts_transcript") {
		t.Fatalf("migration 042 must create the reverse index by name; statement=%q", statement)
	}
	if !strings.Contains(statement, "ON pull_request_attachment_transcripts (transcript_id)") {
		t.Fatalf("migration 042 must index transcript_id as the leading (and only) column, or the lookup by transcript cannot use it; statement=%q", statement)
	}
	if strings.Contains(statement, "attachment_id") {
		t.Fatalf("migration 042 must not index attachment_id: the primary key already leads with it, and the reverse lookup is by transcript; statement=%q", statement)
	}

	downSQL := string(down)
	if !strings.Contains(downSQL, "DROP INDEX IF EXISTS idx_pull_request_attachment_transcripts_transcript") {
		t.Fatal("migration 042 down must drop the index it created")
	}
}
