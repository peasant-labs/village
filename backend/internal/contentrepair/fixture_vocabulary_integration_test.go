//go:build integration

package contentrepair

// fixtureValue is the spelling a repair fixture uses to name a seeded shape.
// It is a vocabulary of the fixtures alone, so the cases switch on its members
// and never on a bare string.
type fixtureValue string

const (
	fixtureMalformed fixtureValue = "malformed"
	fixtureNone      fixtureValue = "none"
	fixtureMissing   fixtureValue = "missing"
)
