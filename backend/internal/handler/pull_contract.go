package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/peasant-labs/schema"
)

// The pull envelopes Village serves, as the schema module names them. A
// client asks for one in the schema.PullContractHeader request header; the
// schema module's doc on that header is the rule implemented here.
//
//   - The default, served to a request without the header or with a malformed
//     one, is the 0.2.0 envelope every released CLI reads. Released CLIs read
//     the advertised pullContractVersion as "the envelope I will get", so the
//     default and the advertisement are the same constant.
//   - The minimum is the oldest envelope served; a request below it is 406.
//   - The maximum is the newest envelope served, advertised as
//     pullContractMaxVersion.
const (
	pullContractVersion    = schema.LegacyPullContractVersion
	minPullContractVersion = schema.LegacyPullContractVersion
	maxPullContractVersion = schema.PublicOwnerPullContractVersion
)

// servedPullContracts lists every envelope Village can render, newest first.
// Negotiation serves the first one that is no newer than the request.
var servedPullContracts = []schema.ContractVersion{
	schema.PublicOwnerPullContractVersion,
	schema.LegacyPullContractVersion,
}

// pullContractRequest is a well-formed pull contract version: three runs of
// ASCII digits. The parts stay strings so a long run of digits compares by
// value without overflowing an integer.
type pullContractRequest [3]string

// parsePullContract reads a header value as MAJOR.MINOR.PATCH digits. Anything
// else (0.3, v0.3.0, 0.3.0-rc1, a list of versions, an empty value) is not
// well-formed, and the caller treats it as absent.
func parsePullContract(raw string) (pullContractRequest, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return pullContractRequest{}, false
	}
	var version pullContractRequest
	for i, part := range parts {
		if part == "" {
			return pullContractRequest{}, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return pullContractRequest{}, false
			}
		}
		version[i] = part
	}
	return version, true
}

// comparePullContract orders two parsed versions part by part, numerically.
func comparePullContract(a, b pullContractRequest) int {
	for i := range a {
		x, y := strings.TrimLeft(a[i], "0"), strings.TrimLeft(b[i], "0")
		if len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return 0
}

// mustParsePullContract parses a version the schema module names. Those are
// constants, so a failure is a programming error in the pin, not input.
func mustParsePullContract(v schema.ContractVersion) pullContractRequest {
	parsed, ok := parsePullContract(string(v))
	if !ok {
		panic(fmt.Sprintf("the schema module names pull contract %q, which is not MAJOR.MINOR.PATCH digits", v))
	}
	return parsed
}

// selectPullContract picks the envelope for a request's header values. It
// returns ok=false only when the request names a well-formed version below
// the minimum.
func selectPullContract(values []string) (schema.ContractVersion, bool) {
	if len(values) != 1 {
		// No header, or the header sent twice: there is no single request to
		// honour, so the default is served.
		return pullContractVersion, true
	}
	requested, ok := parsePullContract(values[0])
	if !ok {
		return pullContractVersion, true
	}
	if comparePullContract(requested, mustParsePullContract(minPullContractVersion)) < 0 {
		return "", false
	}
	for _, served := range servedPullContracts {
		if comparePullContract(mustParsePullContract(served), requested) <= 0 {
			return served, true
		}
	}
	// Unreachable while the minimum is the oldest served envelope; refuse
	// rather than serve an envelope newer than the client reads.
	return "", false
}

// negotiatePullContract selects the envelope for a pull request and reports
// it in the response header, which every later response on the request
// carries. A request below the minimum is answered 406 with a
// VillageErrorResponse here, and the caller stops.
func negotiatePullContract(w http.ResponseWriter, r *http.Request) (schema.ContractVersion, bool) {
	values := r.Header.Values(schema.PullContractHeader)
	served, ok := selectPullContract(values)
	if !ok {
		writeJSON(w, http.StatusNotAcceptable, schema.VillageErrorResponse{Error: fmt.Sprintf(
			"this client reads pull contract %s, which Village no longer serves; Village serves pull contracts %s to %s; upgrade peasant to pull",
			values[0], minPullContractVersion, maxPullContractVersion)})
		return "", false
	}
	w.Header().Set(schema.PullContractHeader, served.String())
	return served, true
}

// servesLegacyPull reports whether the negotiated envelope is 0.2.0, which
// the schema module's Legacy* converters render from the 0.3.0 values.
func servesLegacyPull(served schema.ContractVersion) bool {
	return served == schema.LegacyPullContractVersion
}
