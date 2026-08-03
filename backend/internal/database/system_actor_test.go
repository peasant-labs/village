package database

import "testing"

// TestIsReservedSystemID is the no-DB mirror of the users.id storage-boundary
// CHECK (027): the system-identity range 00000000-0000-0000-0000-* is reserved,
// every other UUID (v4 defaults, explicit non-reserved ids) is a user id.
func TestIsReservedSystemID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{name: "system actor sentinel", id: SystemActorID, want: true},
		{name: "reserved prefix, nonzero suffix", id: "00000000-0000-0000-0000-000000000001", want: true},
		{name: "reserved prefix, uppercase suffix", id: "00000000-0000-0000-0000-00000000000A", want: true},
		{name: "typical v4 id", id: "3f2504e0-4f89-41d3-9a0c-0305e82c3301", want: false},
		{name: "explicit non-reserved seed id", id: "a0000000-0000-0000-0000-000000000001", want: false},
		{name: "near-miss: 80th bit set", id: "00000000-0000-0000-0001-000000000000", want: false},
		{name: "empty string", id: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReservedSystemID(tc.id); got != tc.want {
				t.Fatalf("IsReservedSystemID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
