//go:build integration

package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// wireKeyOf returns the JSON object key a struct field is served under. The
// caller passes two values of one type that differ in exactly that field, so
// the field is named by a compile-checked struct literal and its wire name is
// read from the type's own encoding instead of being restated as a string.
//
// It panics when the two values do not differ in exactly one key: that is a
// mistake in the test, not a condition to handle.
func wireKeyOf[T any](base, changed T) string {
	encode := func(value T) map[string]json.RawMessage {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(fmt.Sprintf("wireKeyOf: cannot encode %T: %v", value, err))
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			panic(fmt.Sprintf("wireKeyOf: %T does not encode as a JSON object: %v", value, err))
		}
		return object
	}
	before, after := encode(base), encode(changed)
	var differing []string
	for key, value := range after {
		if previous, present := before[key]; !present || !bytes.Equal(previous, value) {
			differing = append(differing, key)
		}
	}
	for key := range before {
		if _, present := after[key]; !present {
			differing = append(differing, key)
		}
	}
	if len(differing) != 1 {
		panic(fmt.Sprintf("wireKeyOf: the two %T values differ in the keys %v; pass two values that differ in exactly one field", base, differing))
	}
	return differing[0]
}
