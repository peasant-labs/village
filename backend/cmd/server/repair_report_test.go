package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/contentrepair"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/repair-report.yaml
var repairReportFixture []byte

func TestRepairReportKeys(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name         string   `yaml:"name"`
			Error        string   `yaml:"error"`
			Phase        string   `yaml:"phase"`
			FailureClass string   `yaml:"failure_class"`
			Stdout       []string `yaml:"stdout"`
			StdoutAbsent []string `yaml:"stdout_absent"`
			Fails        bool     `yaml:"fails"`
		} `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(repairReportFixture))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"applied-prints-phase-and-exits-zero":                       false,
		"refused-prints-failure-class-and-fails":                    false,
		"transcript-deleted-prints-outcome-without-phase-and-fails": false,
		"other-error-prints-nothing-and-fails":                      false,
	}
	for _, c := range fixture.Cases {
		if seen, known := required[c.Name]; !known || seen {
			t.Fatalf("unknown or duplicate repair report case %q", c.Name)
		}
		required[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			opts := contentrepair.Options{RepairID: uuid.New(), TranscriptID: uuid.New(), Action: contentrepair.RestoreAttestedGeneration}
			outcome := contentrepair.Outcome{RepairID: opts.RepairID, TranscriptID: opts.TranscriptID, Action: opts.Action,
				Phase: contentrepair.Phase(c.Phase), FailureClass: contentrepair.FailureClass(c.FailureClass), ContentRevision: 3}
			var runErr error
			switch c.Error {
			case "no-error":
			case "transcript-deleted":
				outcome = contentrepair.Outcome{}
				runErr = fmt.Errorf("the restore ended: %w", contentrepair.ErrTranscriptDeleted)
			case "database-failure":
				outcome = contentrepair.Outcome{}
				runErr = errors.New("database unavailable")
			default:
				t.Fatalf("fixture error %q is unknown", c.Error)
			}
			var out bytes.Buffer
			err := reportContentRepair(&out, opts, outcome, runErr)
			if (err != nil) != c.Fails {
				t.Fatalf("failure = %v, want fails=%v", err, c.Fails)
			}
			for _, want := range c.Stdout {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("stdout %q lacks %q", out.String(), want)
				}
			}
			for _, absent := range c.StdoutAbsent {
				if strings.Contains(out.String(), absent) {
					t.Fatalf("stdout %q carries %q", out.String(), absent)
				}
			}
			if c.Error == "transcript-deleted" && !errors.Is(err, contentrepair.ErrTranscriptDeleted) {
				t.Fatalf("the deleted outcome must keep its error, got %v", err)
			}
		})
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing repair report fixture case %q", name)
		}
	}
}
