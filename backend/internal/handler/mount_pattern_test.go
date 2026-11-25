package handler_test

import (
	"strings"
	"testing"

	schemaRoute "github.com/peasant-labs/schema/route"

	"github.com/peasant-labs/village/backend/internal/handler"
)

// mountedOutsideAPIGroup names the declared routes the router mounts outside
// its API group, each from its own pattern: the health probe answers at the
// server root, where a load balancer looks for it.
var mountedOutsideAPIGroup = map[string]schemaRoute.Route{
	schemaRoute.VillageGetHealth.OperationID(): schemaRoute.VillageGetHealth,
}

// Every other route the Village API document declares mounts inside the API
// group: prefixing its relative pattern with the group prefix gives back the
// declared pattern exactly.
func TestMountPatternRoundTripsEveryVillageRoute(t *testing.T) {
	for id, route := range mountedOutsideAPIGroup {
		if strings.HasPrefix(route.Pattern(), handler.APIPrefix+"/") {
			t.Errorf("%s (%s) is now under %q; mount it inside the API group and remove it from mountedOutsideAPIGroup",
				id, route.Pattern(), handler.APIPrefix)
		}
	}
	for _, route := range schemaRoute.AllVillage {
		if _, outside := mountedOutsideAPIGroup[route.OperationID()]; outside {
			continue
		}
		relative := handler.MountPattern(route)
		if !strings.HasPrefix(relative, "/") {
			t.Errorf("MountPattern(%s) = %q; want a path starting with /", route.OperationID(), relative)
		}
		if got := handler.APIPrefix + relative; got != route.Pattern() {
			t.Errorf("APIPrefix + MountPattern(%s) = %q; want the declared pattern %q", route.OperationID(), got, route.Pattern())
		}
	}
}

// A pattern outside the API group would be served at a different path than
// the contract declares, so MountPattern refuses it instead of returning it.
func TestMountPatternRefusesARouteOutsideTheAPIGroup(t *testing.T) {
	defer func() {
		recovered := recover()
		message, ok := recovered.(string)
		if !ok || !strings.Contains(message, "is not under \"/api/v1\"") {
			t.Fatalf("MountPattern(zero Route) recovered %#v; want a panic naming the API prefix", recovered)
		}
	}()
	handler.MountPattern(schemaRoute.Route{})
}
