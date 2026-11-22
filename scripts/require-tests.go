//go:build ignore

// Command require-tests fails closed on missing, failed or skipped Go test
// events. It reads a go test -json stream on stdin and checks it against one
// group of the required-test manifest:
//
//	go run scripts/require-tests.go <manifest> <group|auto> < go-test.jsonl
//
// The manifest is a JSON object mapping a group name to its required test
// names (JSON is a strict subset of YAML, so the manifest keeps its .yaml
// name). A required name must both run and pass. Any skip or fail event fails
// the check. With auto, the one group whose names appear in the stream is
// selected.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

func main() {
	if len(os.Args) != 3 {
		fail(errors.New("usage: go run scripts/require-tests.go <manifest> <group|auto> < go-test.jsonl"))
	}
	if err := check(os.Args[1], os.Args[2]); err != nil {
		fail(err)
	}
}

func check(manifestPath, selected string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read the required-test manifest %s: %w", manifestPath, err)
	}
	var groups map[string][]string
	if err := json.Unmarshal(raw, &groups); err != nil {
		return fmt.Errorf("the required-test manifest %s is not a JSON object of name lists: %w; restore its shape", manifestPath, err)
	}

	started, passed := map[string]bool{}, map[string]bool{}
	var problems []string
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	line := 0
	for scanner.Scan() {
		line++
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("line %d of the test event stream is not a go test -json event: %w; pass the raw go test -json output", line, err)
		}
		switch event.Action {
		case "skip", "fail":
			name := event.Test
			if name == "" {
				name = event.Package
			}
			if name == "" {
				name = "unknown package"
			}
			problems = append(problems, event.Action+": "+name)
		case "run":
			if event.Test != "" {
				started[event.Test] = true
			}
		case "pass":
			if event.Test != "" {
				passed[event.Test] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read the test event stream after line %d: %w", line, err)
	}

	if selected == "auto" {
		var candidates []string
		for group, tests := range groups {
			for _, name := range tests {
				if started[name] || passed[name] {
					candidates = append(candidates, group)
					break
				}
			}
		}
		if len(candidates) != 1 {
			return fmt.Errorf("cannot uniquely identify the required-test group from the test events (matched %d groups); name the group explicitly", len(candidates))
		}
		selected = candidates[0]
	}
	required, ok := groups[selected]
	if !ok || len(required) == 0 {
		return fmt.Errorf("unknown or empty required-test group %q in %s; use one of the manifest's groups", selected, manifestPath)
	}
	seen := map[string]bool{}
	for _, name := range required {
		if seen[name] {
			return fmt.Errorf("required-test group %q lists %q twice in %s; list each name once", selected, name, manifestPath)
		}
		seen[name] = true
	}

	var missing []string
	for _, name := range required {
		if !(started[name] && passed[name]) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		problems = append(problems, "required test did not pass: "+name)
	}
	if len(passed) == 0 {
		problems = append(problems, "zero tests passed")
	}
	if len(problems) != 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	fmt.Println("required tests passed: " + strings.Join(required, ", "))
	return nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "require-tests: %v\n", err)
	os.Exit(1)
}
