package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/peasant-labs/village/backend/internal/backfill"
	"github.com/peasant-labs/village/backend/internal/config"
)

const (
	defaultRewrapLimit = 100
	maximumRewrapLimit = 1000
)

// runtimeMode is the closed set of mutually exclusive server operations.
type runtimeMode uint8

const (
	runtimeModeServe runtimeMode = iota + 1
	runtimeModeMigrateOnly
	runtimeModeContentIdentityBackfill
	runtimeModeTitleBackfill
	runtimeModeOriginBackfill
	runtimeModeRewrap
	runtimeModeSeedCore
	runtimeModeSeedPrivacy
	runtimeModeShareStateCheck
)

// runtimeSelection is the validated result of parsing process arguments. It is
// safe to use to select authority before constructing a job or listener.
type runtimeSelection struct {
	mode        runtimeMode
	authority   config.AuthorityRequirements
	rewrapLimit int
	titleMode   backfill.TitleBackfillMode
	originMode  backfill.OriginBackfillMode
}

func (s runtimeSelection) Mode() runtimeMode { return s.mode }

func (s runtimeSelection) AuthorityRequirements() config.AuthorityRequirements {
	return s.authority
}

func (s runtimeSelection) RewrapLimit() int { return s.rewrapLimit }

func (s runtimeSelection) TitleBackfillMode() backfill.TitleBackfillMode { return s.titleMode }

func (s runtimeSelection) OriginBackfillMode() backfill.OriginBackfillMode { return s.originMode }

// parseRuntimeSelection performs no I/O other than parsing the supplied
// arguments. In particular, it does not read authority, construct a job, or
// start a listener.
func parseRuntimeSelection(args []string) (runtimeSelection, error) {
	flags := flag.NewFlagSet("village-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	migrateOnly := flags.Bool("migrate-only", false, "apply database migrations and exit")
	contentBackfill := flags.Bool("backfill-content-identity", false, "repair pending transcript content identities and exit")
	titleBackfill := flags.String("backfill-titles", "", "repair historical transcript titles in dry-run or apply mode and exit")
	originBackfill := flags.String("backfill-origins", "", "reclassify historical transcript session origins in dry-run or apply mode and exit")
	rewrap := flags.Bool("rewrap-transcript-keys", false, "rewrap a bounded batch of transcript data keys and exit")
	seedCore := flags.Bool("seed-core", false, "load the encrypted core development profile and exit")
	seedPrivacy := flags.Bool("seed-privacy", false, "load the encrypted privacy development profile and exit")
	shareStateCheck := flags.Bool("check-share-state", false, "report whether the derived transcript_shares projection still equals a latest-event fold over transcript_share_attempts, and exit")
	rewrapLimit := flags.Int("rewrap-limit", defaultRewrapLimit, "maximum transcript data keys to inspect")

	if err := flags.Parse(args); err != nil {
		return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed because an argument is unknown or malformed in parseRuntimeSelection before authority loading; no job or listener was started; select exactly one documented mode and use -rewrap-limit only with -rewrap-transcript-keys: %w", err)
	}
	if flags.NArg() != 0 {
		return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed because positional argument %q is not supported in parseRuntimeSelection before authority loading; no job or listener was started; remove positional arguments and select exactly one documented flag mode", flags.Arg(0))
	}

	selected := 0
	for _, enabled := range []bool{*migrateOnly, *contentBackfill, *titleBackfill != "", *originBackfill != "", *rewrap, *seedCore, *seedPrivacy, *shareStateCheck} {
		if enabled {
			selected++
		}
	}
	if selected > 1 {
		return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed because %d mutually exclusive operations were selected in parseRuntimeSelection before authority loading; no job or listener was started; choose only one of serve, migrate-only, content-identity backfill, title backfill, origin backfill, rewrap, core seed, privacy seed, or share-state check", selected)
	}
	rewrapLimitSupplied := false
	flags.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "rewrap-limit" {
			rewrapLimitSupplied = true
		}
	})
	if rewrapLimitSupplied && !*rewrap {
		return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed because -rewrap-limit was supplied without -rewrap-transcript-keys in parseRuntimeSelection before authority loading; no job or listener was started; remove the limit or select the bounded rewrap mode")
	}
	if *rewrap && (*rewrapLimit < 1 || *rewrapLimit > maximumRewrapLimit) {
		return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed because rewrap limit %d is outside the bounded range 1..%d in parseRuntimeSelection before authority loading; no key job or listener was started; set -rewrap-limit within that range", *rewrapLimit, maximumRewrapLimit)
	}
	var parsedTitleMode backfill.TitleBackfillMode
	if *titleBackfill != "" {
		var parseErr error
		parsedTitleMode, parseErr = backfill.ParseTitleBackfillMode(*titleBackfill)
		if parseErr != nil {
			return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed before authority loading; no database, object storage, or key authority was accessed and no job or listener started; select -backfill-titles=dry-run or -backfill-titles=apply: %w", parseErr)
		}
	}

	var parsedOriginMode backfill.OriginBackfillMode
	if *originBackfill != "" {
		var parseErr error
		parsedOriginMode, parseErr = backfill.ParseOriginBackfillMode(*originBackfill)
		if parseErr != nil {
			return runtimeSelection{}, fmt.Errorf("runtime mode parsing failed before authority loading; no database, object storage, or key authority was accessed and no job or listener started; select -backfill-origins=dry-run or -backfill-origins=apply: %w", parseErr)
		}
	}

	switch {
	case *migrateOnly:
		return runtimeSelection{mode: runtimeModeMigrateOnly, authority: config.PostgreSQLAuthority}, nil
	case *contentBackfill:
		return runtimeSelection{mode: runtimeModeContentIdentityBackfill, authority: config.BlobProcessingAuthority}, nil
	case *titleBackfill != "":
		return runtimeSelection{mode: runtimeModeTitleBackfill, authority: config.BlobProcessingAuthority, titleMode: parsedTitleMode}, nil
	case *originBackfill != "":
		return runtimeSelection{mode: runtimeModeOriginBackfill, authority: config.BlobProcessingAuthority, originMode: parsedOriginMode}, nil
	case *rewrap:
		return runtimeSelection{mode: runtimeModeRewrap, authority: config.BlobProcessingAuthority, rewrapLimit: *rewrapLimit}, nil
	case *seedCore:
		return runtimeSelection{mode: runtimeModeSeedCore, authority: config.BlobProcessingAuthority}, nil
	case *seedPrivacy:
		return runtimeSelection{mode: runtimeModeSeedPrivacy, authority: config.BlobProcessingAuthority}, nil
	case *shareStateCheck:
		// Reads two tables and writes nothing, so it needs no object storage and
		// no key authority - deliberately, because a maintenance job that cannot
		// decrypt anything is a maintenance job that cannot leak anything.
		return runtimeSelection{mode: runtimeModeShareStateCheck, authority: config.PostgreSQLAuthority}, nil
	default:
		return runtimeSelection{mode: runtimeModeServe, authority: config.ServingAuthority}, nil
	}
}
