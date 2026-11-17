package database

import (
	_ "embed"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed queries/testdata/transcript-identity-statements.yaml
var transcriptIdentityStatementsYAML []byte

type identityWriterKind string
type identityWriterVerb string

const (
	identityCreate      identityWriterKind = "create"
	identityInstall     identityWriterKind = "identity-install"
	identityReplacement identityWriterKind = "replacement"
	identityRewrap      identityWriterKind = "rewrap"
	identityMetadata    identityWriterKind = "metadata-visibility-license"
	identityRepair      identityWriterKind = "operator-repair"
	identityRestore     identityWriterKind = "restore-or-reinsert"
	identityOldBinary   identityWriterKind = "old-binary-compatible"
	identityInsert      identityWriterVerb = "INSERT"
	identityUpdate      identityWriterVerb = "UPDATE"
	identityDelete      identityWriterVerb = "DELETE"
)

type identityStatement struct {
	Name string             `yaml:"name"`
	File string             `yaml:"file"`
	Verb identityWriterVerb `yaml:"verb"`
	Kind identityWriterKind `yaml:"kind"`
}

type identityBoundary struct {
	Name        string             `yaml:"name"`
	Kind        identityWriterKind `yaml:"kind"`
	Explanation string             `yaml:"explanation"`
}

type identityFenceFixture struct {
	Statements []identityStatement `yaml:"statements"`
	Boundaries []identityBoundary  `yaml:"boundaries"`
	Checks     []struct {
		Name          string `yaml:"name"`
		Operation     string `yaml:"operation"`
		Statement     string `yaml:"statement"`
		StatementName string `yaml:"statement_name"`
	} `yaml:"checks"`
}

var requiredIdentityFenceChecks = []string{
	"fence-declares-every-identity-writer",
	"fence-refuses-undeclared-writer",
	"fence-parsed-verb-matches-declaration",
	"fence-declares-old-binary-absence",
	"fence-declares-restore-path",
}

func validateIdentityStatements(parsed []parsedStatement, declared []identityStatement) error {
	inventory := make(map[string]identityStatement)
	for _, row := range declared {
		if row.Name == "" || row.File == "" {
			return fmt.Errorf("identity inventory has an unnamed statement or file")
		}
		if _, exists := inventory[row.Name]; exists {
			return fmt.Errorf("identity inventory repeats statement %q", row.Name)
		}
		switch row.Kind {
		case identityCreate:
			if row.Verb != identityInsert {
				return fmt.Errorf("identity create %q must be an INSERT", row.Name)
			}
		case identityInstall, identityReplacement, identityRewrap, identityMetadata, identityRepair:
			if row.Verb != identityUpdate {
				return fmt.Errorf("identity writer %q must be an UPDATE", row.Name)
			}
		case identityRestore, identityOldBinary:
			if row.Verb != identityInsert && row.Verb != identityUpdate {
				return fmt.Errorf("identity boundary writer %q has an unsupported verb", row.Name)
			}
		default:
			return fmt.Errorf("identity writer %q has unknown kind %q", row.Name, row.Kind)
		}
		inventory[row.Name] = row
	}
	found := make(map[string]bool)
	for _, statement := range parsed {
		writesTranscript := false
		for _, target := range statement.WriteTargets {
			if strings.EqualFold(target, "transcripts") {
				writesTranscript = true
			}
		}
		if !writesTranscript || identityWriterVerb(statement.Verb) == identityDelete {
			continue
		}
		if statement.Verb != string(identityInsert) && statement.Verb != string(identityUpdate) {
			return fmt.Errorf("identity writer %q has unclassified verb %q; extend the fence parser", statement.Name, statement.Verb)
		}
		row, ok := inventory[statement.Name]
		if !ok {
			return fmt.Errorf("identity writer %q in %s is undeclared", statement.Name, statement.File)
		}
		if row.File != statement.File || string(row.Verb) != statement.Verb {
			return fmt.Errorf("identity writer %q has mismatched file or parsed verb", statement.Name)
		}
		found[statement.Name] = true
	}
	for name := range inventory {
		if !found[name] {
			return fmt.Errorf("declared identity writer %q is no longer present", name)
		}
	}
	return nil
}

func TestTranscriptIdentityWriteFence(t *testing.T) {
	var fixture identityFenceFixture
	if err := yaml.Unmarshal(transcriptIdentityStatementsYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Checks {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("identity fence has an empty or repeated check %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			parsed := parseQueryStatements(t)
			declared := append([]identityStatement(nil), fixture.Statements...)
			switch c.Operation {
			case "inventory":
				if err := validateIdentityStatements(parsed, declared); err != nil {
					t.Fatal(err)
				}
			case "undeclared":
				parsed = append(parsed, parseQueryFile(t, "synthetic.sql", c.Statement)...)
				if err := validateIdentityStatements(parsed, declared); err == nil || !strings.Contains(err.Error(), "undeclared") {
					t.Fatalf("undeclared writer escaped the fence: %v", err)
				}
			case "wrong-verb":
				found := false
				for i := range declared {
					if declared[i].Name == c.StatementName {
						declared[i].Verb = identityUpdate
						found = true
					}
				}
				if !found {
					t.Fatal("fixture names no declaration to mutate")
				}
				if err := validateIdentityStatements(parsed, declared); err == nil {
					t.Fatal("a mismatched parsed verb escaped the fence")
				}
			case "old-binary", "restore":
				want := identityOldBinary
				if c.Operation == "restore" {
					want = identityRestore
				}
				found := false
				for _, b := range fixture.Boundaries {
					if b.Kind == want && b.Name != "" && b.Explanation != "" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing documented identity boundary %q", want)
				}
			default:
				t.Fatalf("unknown fence operation %q", c.Operation)
			}
		})
	}
	for _, name := range requiredIdentityFenceChecks {
		if !seen[name] {
			t.Fatalf("identity fence fixture omits %q", name)
		}
	}
}
