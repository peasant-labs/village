package handler

import (
	"log"
	"strings"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/scry"
)

// integrityService is the background transcript integrity check. A nil
// service means the check is off.
type integrityService struct {
	checker scry.Checker
}

// newIntegrityService returns nil, with one log line naming only variables,
// unless SCRY_URL and SCRY_SHARED_SECRET are both valid.
func newIntegrityService(cfg *config.Config) *integrityService {
	settings := scry.Settings{URL: cfg.ScryURL, SharedSecret: cfg.ScrySharedSecret}
	if settings.Unset() {
		log.Println("Integrity check not configured; transcripts are not checked (set SCRY_URL and SCRY_SHARED_SECRET to enable)")
		return nil
	}
	if problems := settings.Problems(); len(problems) > 0 {
		log.Printf("Integrity check configuration incomplete; transcripts are not checked: %s", strings.Join(problems, "; "))
		return nil
	}
	log.Println("Integrity check configured")
	return &integrityService{checker: scry.NewHTTPClient(settings)}
}
