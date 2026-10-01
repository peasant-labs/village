package digest

import (
	"fmt"
	"sort"

	"github.com/peasant-labs/schema"
)

// Restrict is the digest one reader may see: every item of a transcript keep
// refuses is removed, and what the chain says about itself is recounted over
// what remains, so neither a prompt nor a count describes a transcript the
// reader cannot open.
//
// It equals Build over the kept transcripts, with two exceptions, both in the
// direction of saying less:
//
//   - Build keeps one anchor per commit and attributes it to the transcript that
//     recorded the commit earliest. When that transcript is removed, its anchor
//     goes with it, even if a kept transcript recorded the same commit, so the
//     restricted chain can cover fewer commits than a fresh build would.
//   - The header's harness and redaction level stay those of the first session
//     of the whole chain, even when no session is kept: the contract requires
//     both, they name a harness and a level, never content, and the chain does
//     not say which harness a later session used.
//
// A keep that accepts every transcript returns the digest unchanged.
func Restrict(d schema.PromptDigest, keep func(schema.TranscriptID) bool) (schema.PromptDigest, error) {
	items := make([]schema.PromptDigestItem, 0, len(d.Items))
	for _, item := range d.Items {
		if keep(item.TranscriptID) {
			items = append(items, item)
		}
	}
	if len(items) == len(d.Items) {
		return d, nil
	}

	sessions, prompts, commits := 0, 0, 0
	skillCounts := map[string]int{}
	for i := range items {
		switch items[i].Kind {
		case schema.DigestItemSession:
			sessions++
			items[i].Text = fmt.Sprintf("session %d", sessions)
		case schema.DigestItemPrompt:
			prompts++
			items[i].Ordinal = intPtr(prompts)
		case schema.DigestItemSkill:
			skillCounts[items[i].Text]++
		case schema.DigestItemCommit:
			commits++
		}
	}

	skills := make([]schema.PromptDigestSkill, 0, len(skillCounts))
	for name, count := range skillCounts {
		skills = append(skills, schema.PromptDigestSkill{Name: name, InvocationCount: count})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })

	header := d.Header
	header.SessionCount = sessions
	header.PromptCount = prompts
	header.CommitsCovered = commits

	restricted := schema.PromptDigest{Header: header, Skills: skills, Items: items}
	if err := restricted.Validate(); err != nil {
		return schema.PromptDigest{}, fmt.Errorf("restricted prompt digest did not validate: %w", err)
	}
	return restricted, nil
}
