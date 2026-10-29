package handler

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/scry"
)

// integrityService is the background transcript integrity check. A nil
// service means the check is off.
type integrityService struct {
	checker scry.Checker
	// version is the checker version the service last reported; nil until
	// the first report.
	version atomic.Pointer[string]
	// wake shortens the wait for the next pass; one pending wake is enough.
	wake   chan struct{}
	wg     sync.WaitGroup
	cancel context.CancelFunc
	now    func() time.Time
	// callSpacing is the pause between two checks in one pass.
	callSpacing time.Duration
	// secretRejected is set while the service refuses the shared secret, so
	// the warning about it is logged once rather than on every call.
	secretRejected atomic.Bool
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
	return newIntegrityServiceFor(scry.NewHTTPClient(settings))
}

// newIntegrityServiceFor composes the service around one checker.
func newIntegrityServiceFor(checker scry.Checker) *integrityService {
	return &integrityService{
		checker:     checker,
		wake:        make(chan struct{}, 1),
		now:         time.Now,
		callSpacing: integrityCallSpacing,
	}
}

// currentVersion is the checker version the service last reported.
func (s *integrityService) currentVersion() (string, bool) {
	v := s.version.Load()
	if v == nil {
		return "", false
	}
	return *v, true
}

// setVersion records the version the service's health report names. It is
// what the service runs now, so it replaces the held version even when it is
// older (a rollback).
func (s *integrityService) setVersion(v string) {
	s.version.Store(&v)
}

// observeVersion records the version on one check answer. It only moves the
// held version forward: during a rollout an older instance may still answer,
// and that must not restart a sweep. Versions compare as numbers.
func (s *integrityService) observeVersion(v string) {
	for {
		held := s.version.Load()
		if held != nil && compareScryVersions(v, *held) <= 0 {
			return
		}
		if s.version.CompareAndSwap(held, &v) {
			return
		}
	}
}

// compareScryVersions orders two MAJOR.MINOR.PATCH versions numerically. The
// contract forbids leading zeros, so a longer component is the larger one
// and equal lengths compare as text; no component can overflow.
func compareScryVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if len(as[i]) != len(bs[i]) {
			if len(as[i]) < len(bs[i]) {
				return -1
			}
			return 1
		}
		if c := strings.Compare(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}
