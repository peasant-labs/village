package handler

import (
	"fmt"
	"strings"

	schemaRoute "github.com/peasant-labs/schema/route"
)

// APIPrefix is the prefix the router mounts the Village API group under. It is
// read from a route the Village API document declares, so the group prefix and
// the declared patterns cannot disagree; every declared pattern starts with it.
var APIPrefix = strings.SplitN(schemaRoute.VillageGetTranscriptSegments.Pattern(), "/transcripts/", 2)[0]

// MountPattern returns a contract route's pattern relative to APIPrefix, the
// form a route takes when it is mounted inside the router's API group. The
// pattern comes from the schema module's route member, so the mounted path and
// the declared path are one value.
//
// It panics when the pattern does not start with APIPrefix followed by a path,
// because mounting the remainder would serve a different path than the one the
// contract declares. The router is built once at startup, so the panic stops
// the server before it serves any request.
func MountPattern(route schemaRoute.Route) string {
	relative, ok := strings.CutPrefix(route.Pattern(), APIPrefix)
	if !ok || !strings.HasPrefix(relative, "/") {
		panic(fmt.Sprintf("handler.MountPattern: route %s (%s %s) is not under %q; "+
			"the router mounts contract routes inside its %s group, so this route would be served at the wrong path; "+
			"mount it outside the API group from its own pattern, or fix the route in the schema module",
			route.OperationID(), route.Method(), route.Pattern(), APIPrefix, APIPrefix))
	}
	return relative
}
