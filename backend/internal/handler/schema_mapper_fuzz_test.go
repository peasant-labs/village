package handler

import (
	"math"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

// FuzzIntToPgInt4 fuzzes intToPgInt4 with arbitrary int values.
// Verifies: no panics, result is always Valid, and int32 overflow wraps predictably.
func FuzzIntToPgInt4(f *testing.F) {
	// Seed corpus: boundaries and interesting values
	seeds := []int{
		0, 1, -1,
		math.MaxInt32, math.MinInt32,
		math.MaxInt32 + 1, math.MinInt32 - 1,
		math.MaxInt64, math.MinInt64,
		42, -42,
		1<<31 - 1, -(1 << 31),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, v int) {
		result := intToPgInt4(v)

		// Must always be Valid (function treats all values as "provided")
		if !result.Valid {
			t.Errorf("intToPgInt4(%d): expected Valid=true, got Valid=false", v)
		}

		// The stored Int32 must equal int32(v) — Go truncation semantics
		expected := int32(v)
		if result.Int32 != expected {
			t.Errorf("intToPgInt4(%d): expected Int32=%d, got Int32=%d", v, expected, result.Int32)
		}
	})
}

// FuzzIntToPgInt8 fuzzes intToPgInt8 with arbitrary int values.
// Verifies: no panics, result is always Valid, int64 conversion is identity on 64-bit.
func FuzzIntToPgInt8(f *testing.F) {
	seeds := []int{
		0, 1, -1,
		math.MaxInt64, math.MinInt64,
		math.MaxInt32, math.MinInt32,
		42, -42,
		1000000, -1000000,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, v int) {
		result := intToPgInt8(v)

		if !result.Valid {
			t.Errorf("intToPgInt8(%d): expected Valid=true, got Valid=false", v)
		}

		expected := int64(v)
		if result.Int64 != expected {
			t.Errorf("intToPgInt8(%d): expected Int64=%d, got Int64=%d", v, expected, result.Int64)
		}
	})
}

// FuzzInt64ToTimestamptz fuzzes int64ToTimestamptz with arbitrary int64 values.
// Verifies: no panics, negative → Invalid, zero/positive → Valid with correct time.
func FuzzInt64ToTimestamptz(f *testing.F) {
	seeds := []int64{
		0, 1, -1,
		math.MaxInt64, math.MinInt64,
		1700000000000,   // realistic timestamp (Nov 2023)
		1700000060000,   // realistic timestamp
		-1000,           // negative
		253402300799000, // far future (9999-12-31)
		86400000,        // one day in ms
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, ms int64) {
		result := int64ToTimestamptz(ms)

		if ms < 0 {
			// Negative timestamps must be marked Invalid
			if result.Valid {
				t.Errorf("int64ToTimestamptz(%d): negative ms should produce Valid=false, got Valid=true", ms)
			}
		} else {
			// Zero and positive timestamps must be Valid
			if !result.Valid {
				t.Errorf("int64ToTimestamptz(%d): non-negative ms should produce Valid=true, got Valid=false", ms)
			}
			expected := time.UnixMilli(ms)
			if !result.Time.Equal(expected) {
				t.Errorf("int64ToTimestamptz(%d): expected time %v, got %v", ms, expected, result.Time)
			}
		}
	})
}

// FuzzOptionalStringToPgText fuzzes optionalStringToPgText with arbitrary strings
// including adversarial inputs (SQL injection, XSS, null bytes, unicode).
func FuzzOptionalStringToPgText(f *testing.F) {
	seeds := []string{
		"",
		"hello",
		"main",
		// SQL injection attempts
		"'; DROP TABLE transcripts; --",
		"' OR '1'='1",
		"1; SELECT * FROM users",
		"UNION SELECT password FROM users--",
		// XSS payloads
		"<script>alert('xss')</script>",
		"<img src=x onerror=alert(1)>",
		`"><svg onload=alert(1)>`,
		// Null bytes and control characters
		"hello\x00world",
		"\x00",
		"\x01\x02\x03",
		"line1\nline2\rline3",
		"\t\n\r",
		// Unicode edge cases
		"こんにちは",
		"🎉🔥💀",
		"\u0000",                   // null
		"\uFFFD",                   // replacement character
		"\uFEFF",                   // BOM
		"café",                     // accented
		"naïve",                    // diaeresis
		string([]byte{0xfe, 0xff}), // invalid UTF-8 BOM
		// Path traversal
		"../../etc/passwd",
		"/dev/null",
		// Very long string
		string(make([]byte, 10000)),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		result := optionalStringToPgText(&s)

		// Non-nil pointer must always produce Valid=true
		if !result.Valid {
			t.Errorf("optionalStringToPgText(%q): expected Valid=true, got Valid=false", s)
		}

		// String content must be preserved exactly (no corruption)
		if result.String != s {
			t.Errorf("optionalStringToPgText: string content not preserved. len(input)=%d, len(output)=%d", len(s), len(result.String))
		}

		// Also test nil case (not fuzzable, but verify invariant)
		nilResult := optionalStringToPgText(nil)
		if nilResult.Valid {
			t.Error("optionalStringToPgText(nil): expected Valid=false, got Valid=true")
		}
	})
}

// FuzzSchemaToTranscriptParams fuzzes the full schemaToTranscriptParams mapper
// with random PublishRequest data. Seeds the corpus from existing fixtures.
func FuzzSchemaToTranscriptParams(f *testing.F) {
	// Seed corpus from existing fixtures.
	// We decompose each fixture into fuzzable primitives:
	//   provider, model, harnessVersion string
	//   startMs, endMs int64
	//   turnCount, toolCallCount, subagentCount, tokensIn, tokensOut int
	//   durationMs int64
	//   filePath, format, projectHash, projectName string
	//   blobKey string, blobSize int64, schemaVersion string
	//   hasQuality bool
	type seed struct {
		provider, model, harnessVersion            string
		startMs, endMs                             int64
		turnCount, toolCallCount, subagentCount    int
		tokensIn, tokensOut                        int
		durationMs                                 int64
		filePath, format, projectHash, projectName string
		blobKey                                    string
		blobSize                                   int64
		schemaVersionStr                           string
		hasQuality                                 bool
	}

	seeds := []seed{
		// ValidMinimal
		{
			provider: "openai", model: "gpt-4", harnessVersion: "",
			startMs: 1700000000000, endMs: 1700000060000,
			turnCount: 10, toolCallCount: 25, subagentCount: 0,
			tokensIn: 5000, tokensOut: 10000, durationMs: 60000,
			filePath: "/path/to/transcript.jsonl", format: "jsonl",
			projectHash: "abc123", projectName: "test-project",
			blobKey: "blobs/test-key", blobSize: 1024, schemaVersionStr: "2",
			hasQuality: false,
		},
		// ValidFull
		{
			provider: "openai", model: "gpt-4", harnessVersion: "1.0",
			startMs: 1700000000000, endMs: 1700000060000,
			turnCount: 10, toolCallCount: 25, subagentCount: 2,
			tokensIn: 5000, tokensOut: 10000, durationMs: 60000,
			filePath: "/path/to/transcript.jsonl", format: "jsonl",
			projectHash: "abc123def456", projectName: "test-project",
			blobKey: "blobs/full-key", blobSize: 2048, schemaVersionStr: "2",
			hasQuality: true,
		},
		// ZeroValues
		{
			provider: "", model: "", harnessVersion: "",
			startMs: 0, endMs: 0,
			turnCount: 0, toolCallCount: 0, subagentCount: 0,
			tokensIn: 0, tokensOut: 0, durationMs: 0,
			filePath: "", format: "",
			projectHash: "", projectName: "",
			blobKey: "", blobSize: 0, schemaVersionStr: "0",
			hasQuality: false,
		},
		// NegativeTimes
		{
			provider: "openai", model: "gpt-4", harnessVersion: "",
			startMs: -1000, endMs: -500,
			turnCount: 10, toolCallCount: 0, subagentCount: 0,
			tokensIn: 0, tokensOut: 0, durationMs: 0,
			filePath: "/path/to/transcript.jsonl", format: "jsonl",
			projectHash: "", projectName: "",
			blobKey: "blobs/neg", blobSize: 512, schemaVersionStr: "2",
			hasQuality: false,
		},
	}

	for _, s := range seeds {
		f.Add(
			s.provider, s.model, s.harnessVersion,
			s.startMs, s.endMs,
			s.turnCount, s.toolCallCount, s.subagentCount,
			s.tokensIn, s.tokensOut,
			s.durationMs,
			s.filePath, s.format, s.projectHash, s.projectName,
			s.blobKey, s.blobSize, s.schemaVersionStr,
			s.hasQuality,
		)
	}

	f.Fuzz(func(t *testing.T,
		provider, model, harnessVersion string,
		startMs, endMs int64,
		turnCount, toolCallCount, subagentCount int,
		tokensIn, tokensOut int,
		durationMs int64,
		filePath, format, projectHash, projectName string,
		blobKey string, blobSize int64, schemaVersionStr string,
		hasQuality bool,
	) {
		req := schema.PublishRequest{
			Identity: schema.SessionIdentity{
				SessionID:     schema.SessionID("550e8400-e29b-41d4-a716-446655440000"),
				SchemaVersion: 2,
			},
			Model: schema.ModelInfo{
				Harness:        schema.Harness(provider),
				Model:          schema.ModelID(model),
				HarnessVersion: harnessVersion,
			},
			Timestamp: schema.TimestampInfo{
				Start: startMs,
				End:   endMs,
			},
			Source: schema.SourceInfo{
				FilePath: filePath,
				Format:   schema.SourceFormat(format),
			},
			Git: schema.GitContext{},
			Project: schema.ProjectContext{
				Hash:     schema.ProjectHash(projectHash),
				FilePath: filePath,
				Name:     projectName,
			},
			Stats: schema.SessionStats{
				TurnCount:     turnCount,
				ToolCallCount: toolCallCount,
				SubagentCount: subagentCount,
				DurationMs:    durationMs,
				TokensIn:      tokensIn,
				TokensOut:     tokensOut,
			},
			Diagnostics: schema.DiagnosticsInfo{},
		}

		if hasQuality {
			req.Quality = &schema.QualityMetrics{}
		}

		// The primary assertion: schemaToTranscriptParams must not panic
		// for any combination of inputs.
		result := schemaToTranscriptParams(req, blobKey, blobSize, schemaVersionStr, sessionorigin.Unknown)

		// Structural invariants that must hold for all inputs:

		// Visibility must always be the default
		if result.Visibility != DefaultVisibility {
			t.Errorf("Visibility=%q, want %q", result.Visibility, DefaultVisibility)
		}

		// BlobKey must be passed through unchanged
		if result.BlobKey != blobKey {
			t.Errorf("BlobKey=%q, want %q", result.BlobKey, blobKey)
		}

		// SchemaVersion must be passed through unchanged
		if result.SchemaVersion != schemaVersionStr {
			t.Errorf("SchemaVersion=%q, want %q", result.SchemaVersion, schemaVersionStr)
		}

		// BlobSizeBytes must equal int64ToPgInt8(blobSize)
		expectedBlobSize := int64ToPgInt8(blobSize)
		if result.BlobSizeBytes != expectedBlobSize {
			t.Errorf("BlobSizeBytes=%v, want %v", result.BlobSizeBytes, expectedBlobSize)
		}

		// Provider string must be passed through
		if result.ModelProvider != provider {
			t.Errorf("ModelProvider=%q, want %q", result.ModelProvider, provider)
		}

		// Timestamp Valid/Invalid must match int64ToTimestamptz semantics
		if startMs < 0 && result.SessionStart.Valid {
			t.Error("SessionStart should be Invalid for negative startMs")
		}
		if startMs >= 0 && !result.SessionStart.Valid {
			t.Error("SessionStart should be Valid for non-negative startMs")
		}
		if endMs < 0 && result.SessionEnd.Valid {
			t.Error("SessionEnd should be Invalid for negative endMs")
		}
		if endMs >= 0 && !result.SessionEnd.Valid {
			t.Error("SessionEnd should be Valid for non-negative endMs")
		}

		// TurnCount must equal intToPgInt4(turnCount)
		expectedTurnCount := intToPgInt4(turnCount)
		if result.TurnCount != expectedTurnCount {
			t.Errorf("TurnCount=%v, want %v", result.TurnCount, expectedTurnCount)
		}

		// TokenCount must always be Invalid (hardcoded in mapper)
		if result.TokenCount.Valid {
			t.Error("TokenCount should always be Invalid (not provided)")
		}
	})
}
