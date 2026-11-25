package handler

import "testing"

// contractPath unwraps one of the contract's path builders, so a test requests
// the path the served document declares instead of spelling it. A builder
// refuses only an empty or dot segment, which a test never means to send.
func contractPath(t testing.TB) func(string, error) string {
	return func(path string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("build a contract path: %v", err)
		}
		return path
	}
}
