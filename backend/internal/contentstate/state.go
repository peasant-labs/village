// Package contentstate owns the closed set of availability states stored for
// an extant transcript. Retraction is a hard delete, not a content state.
package contentstate

type State string

const (
	Present     State = "present"
	Unavailable State = "unavailable"
)

// All is the database menu in constraint order.
var All = []State{Present, Unavailable}

func (s State) IsValid() bool {
	switch s {
	case Present, Unavailable:
		return true
	default:
		return false
	}
}

func (s State) String() string { return string(s) }
