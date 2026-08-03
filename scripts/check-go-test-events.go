//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run ../scripts/check-go-test-events.go <expected-package>")
	}
	expectedPackage := os.Args[1]
	decoder := json.NewDecoder(bufio.NewReader(os.Stdin))
	runs, passes, skips := 0, 0, 0
	for {
		var event testEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			fail("decode go test -json event failed for %s: %v; preserve JSON output and repair the test invocation", expectedPackage, err)
		}
		if event.Package != "" && event.Package != expectedPackage {
			fail("received event for package %q while validating %q; run one isolated package per event stream", event.Package, expectedPackage)
		}
		switch event.Action {
		case "run":
			if event.Test != "" {
				runs++
				fmt.Printf("=== RUN   %s\n", event.Test)
			}
		case "pass":
			if event.Test != "" {
				passes++
				fmt.Printf("--- PASS: %s\n", event.Test)
			}
		case "skip":
			skips++
			name := event.Test
			if name == "" {
				name = expectedPackage
			}
			fmt.Fprintf(os.Stderr, "--- SKIP: %s\n", name)
		case "output":
			trimmed := strings.TrimSpace(event.Output)
			if event.Output != "" &&
				!strings.HasPrefix(trimmed, "=== RUN") &&
				!strings.HasPrefix(trimmed, "--- PASS:") &&
				!strings.HasPrefix(trimmed, "--- SKIP:") {
				fmt.Print(event.Output)
			}
		}
	}
	if skips != 0 {
		fail("%s emitted %d Go skip event(s), including nested subtests; a required real-service skip is not a passing gate", expectedPackage, skips)
	}
	if runs == 0 || passes == 0 {
		fail("%s completed without test run and pass events; the required integration path may not have executed", expectedPackage)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scripts/check-go-test-events.go: "+format+"\n", args...)
	os.Exit(1)
}
