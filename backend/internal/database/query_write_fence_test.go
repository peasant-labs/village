package database

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The current-state share row is derived by a database trigger from the attempt
// history. Application SQL only ever READS it. Two successive hand-enumerations
// of the writer list each missed a live writer, so this file does not trust a
// list: it parses every statement in queries/*.sql and works out what each one
// actually does to the table.
//
// The declared inventory is a closed set. A new statement that touches the
// table - including a JOIN read added by unrelated work - has to be declared
// before this test passes, and it can only be declared as a read.

//go:embed queries/testdata/transcript-shares-statements.yaml
var transcriptSharesStatementsYAML []byte

const derivedShareTable = "transcript_shares"

type declaredStatement struct {
	Name string `yaml:"name"`
	File string `yaml:"file"`
	Kind string `yaml:"kind"`
}

// parsedStatement is one `-- name: X :kind` block. Verb is the statement's
// leading keyword, and WriteTargets is every table the statement itself writes:
// the tables named by an INSERT INTO, an UPDATE or a DELETE FROM anywhere in
// it, which in a data-modifying common-table expression includes the writes
// inside the WITH list. A table reached through a JOIN, a FROM or a subquery is
// a read and never appears here.
type parsedStatement struct {
	Name         string
	File         string
	Verb         string
	WriteTargets []string
	MentionsRef  bool
}

var (
	statementHeader = regexp.MustCompile(`^--\s*name:\s*(\w+)\s*:(\w+)\s*$`)
	leadingKeyword  = regexp.MustCompile(`(?i)^(WITH|SELECT|INSERT|UPDATE|DELETE)\b`)
	// The three write forms, with the word before them captured so the two
	// non-writing uses of the word UPDATE - `ON CONFLICT DO UPDATE SET` and the
	// `FOR UPDATE` row lock - are not mistaken for a write.
	writeTarget = regexp.MustCompile(`(?i)(\b\w+\s+)?\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func TestTranscriptSharesIsDerivedNotWritten(t *testing.T) {
	parsed := parseQueryStatements(t)

	declared := map[string]declaredStatement{}
	for _, row := range loadDeclaredStatements(t) {
		if _, repeated := declared[row.Name]; repeated {
			t.Fatalf("statement inventory declares %q twice; each statement is declared once", row.Name)
		}
		if row.Kind != "read" && row.Kind != "write" {
			t.Fatalf("statement inventory row %q has kind %q; the only kinds are read and write", row.Name, row.Kind)
		}
		declared[row.Name] = row
	}

	touching := map[string]parsedStatement{}
	for _, statement := range parsed {
		if statement.MentionsRef {
			touching[statement.Name] = statement
		}
	}

	for _, name := range sortedNames(touching) {
		statement := touching[name]
		row, ok := declared[name]
		if !ok {
			t.Errorf("query %q in %s touches %s but is not declared in queries/testdata/transcript-shares-statements.yaml. "+
				"Add it there with its file and kind. Reads (a JOIN, a FROM or a subquery) are declared read; a statement that "+
				"writes the table cannot be declared at all, because the table is written only by its derivation trigger - "+
				"write transcript_share_attempts instead.", name, statement.File, derivedShareTable)
			continue
		}
		if row.File != statement.File {
			t.Errorf("query %q is declared in file %q but lives in %q; correct the inventory", name, row.File, statement.File)
		}
		actual := parsedKind(statement)
		if row.Kind != actual {
			t.Errorf("query %q is declared %q but it is a %s: the statement writes %v. "+
				"A statement that writes %s cannot be declared or shipped: write transcript_share_attempts and let the "+
				"derivation maintain the current-state row.", name, row.Kind, actual, statement.WriteTargets, derivedShareTable)
		}
		if actual == "write" {
			t.Errorf("query %q writes %s directly (a %s statement whose write targets are %v). The table is maintained only "+
				"by its derivation trigger; a direct write is also refused by the database. Rewrite it against "+
				"transcript_share_attempts.", name, derivedShareTable, statement.Verb, statement.WriteTargets)
		}
	}

	for _, name := range sortedNames(declared) {
		if _, ok := touching[name]; !ok {
			t.Errorf("statement inventory declares %q, but no statement in queries/*.sql touches %s under that name. "+
				"Either the statement was renamed or removed - update the inventory - or the parser no longer recognises it.",
				name, derivedShareTable)
		}
	}
}

func parsedKind(statement parsedStatement) string {
	for _, target := range statement.WriteTargets {
		if target == derivedShareTable {
			return "write"
		}
	}
	return "read"
}

func sortedNames[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func loadDeclaredStatements(t *testing.T) []declaredStatement {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(transcriptSharesStatementsYAML))
	decoder.KnownFields(true)
	var rows []declaredStatement
	if err := decoder.Decode(&rows); err != nil {
		t.Fatalf("decode the statement inventory: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("the statement inventory must be exactly one YAML document; found a second: %v", trailing)
	}
	return rows
}

// parseQueryStatements reads every query file and splits it into named
// statements, recording what each one writes.
func parseQueryStatements(t *testing.T) []parsedStatement {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found to parse (err %v); this guard cannot be satisfied by an empty corpus", err)
	}
	var statements []parsedStatement
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		statements = append(statements, parseQueryFile(t, filepath.Base(file), string(content))...)
	}
	return statements
}

func parseQueryFile(t *testing.T, file, content string) []parsedStatement {
	t.Helper()
	var statements []parsedStatement
	var current *parsedStatement
	var body []string
	flush := func() {
		if current == nil {
			return
		}
		joined := strings.Join(body, "\n")
		verb, targets, err := classifyStatement(joined)
		if err != nil {
			t.Fatalf("query %q in %s could not be classified: %v. This guard must be able to name what every statement "+
				"writes; extend the parser deliberately rather than letting an unclassified statement pass.",
				current.Name, file, err)
		}
		current.Verb, current.WriteTargets = verb, targets
		current.MentionsRef = mentionsTable(joined, derivedShareTable)
		statements = append(statements, *current)
		current, body = nil, nil
	}
	for _, line := range strings.Split(content, "\n") {
		if match := statementHeader.FindStringSubmatch(strings.TrimRight(line, " \t\r")); match != nil {
			flush()
			current = &parsedStatement{Name: match[1], File: file}
			continue
		}
		if current != nil {
			body = append(body, line)
		}
	}
	flush()
	return statements
}

// classifyStatement strips comments and reports the statement's leading keyword
// together with every table the statement writes. Only the tables named by
// INSERT INTO, UPDATE and DELETE FROM are writes; everything else the statement
// mentions is a read.
func classifyStatement(body string) (string, []string, error) {
	sql := strings.TrimSpace(stripLineComments(body))
	if sql == "" {
		return "", nil, fmt.Errorf("the statement body is empty")
	}
	match := leadingKeyword.FindStringSubmatch(sql)
	if match == nil {
		return "", nil, fmt.Errorf("unrecognised leading keyword %q; the guard understands WITH, SELECT, INSERT, UPDATE and "+
			"DELETE. A statement form it cannot classify must not ship unclassified: teach the parser to find that form's "+
			"write targets first", strings.Fields(sql)[0])
	}
	verb := strings.ToUpper(match[1])

	var targets []string
	for _, write := range writeTarget.FindAllStringSubmatch(sql, -1) {
		preceding := strings.ToUpper(strings.TrimSpace(write[1]))
		form := strings.ToUpper(strings.Join(strings.Fields(write[2]), " "))
		// `DO UPDATE SET ...` resolves the conflict on the row the enclosing
		// INSERT already targets, and `FOR UPDATE` is a row lock on a read.
		// Neither names a new table.
		if form == "UPDATE" && (preceding == "DO" || preceding == "FOR") {
			continue
		}
		targets = append(targets, write[3])
	}
	return verb, targets, nil
}

func stripLineComments(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func mentionsTable(body string, table string) bool {
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(table) + `\b`)
	return pattern.MatchString(stripLineComments(body))
}
