package digest

import (
	"fmt"
	"strings"

	"github.com/peasant-labs/schema"
)

// Tier is one rendering budget. InlinePrompts is how many prompts render inline;
// MaxBytes caps the rendered size (0 means no cap).
type Tier struct {
	Name          string
	InlinePrompts int
	MaxBytes      int
}

// The three rendering tiers. The comment and check-run tiers are bounded; the
// Village page shows the complete chain.
var (
	CommentTier  = Tier{Name: "comment", InlinePrompts: 10, MaxBytes: 20000}
	CheckRunTier = Tier{Name: "check-run", InlinePrompts: 25, MaxBytes: 60000}
	VillageTier  = Tier{Name: "village", InlinePrompts: 0, MaxBytes: 0}
)

// Render renders a validated digest to GitHub Markdown within tier's budget.
// The complete chain is always in the digest; the tier decides how much is shown
// inline. When the inline prompt budget is exhausted, or the next line would
// cross the byte cap, the remaining sessions collapse into one line that names
// the omitted prompt count and links to Village. It refuses to render an invalid
// digest, so a caller cannot post an inconsistent chain.
func Render(d schema.PromptDigest, tier Tier) (string, error) {
	if err := d.Validate(); err != nil {
		return "", fmt.Errorf("refusing to render an invalid prompt digest: %w", err)
	}

	var b strings.Builder
	b.WriteString(headerLine(d))
	b.WriteByte('\n')

	inlinedPrompts := 0
	for i := 0; i < len(d.Items); i++ {
		item := d.Items[i]
		if item.Kind == schema.DigestItemPrompt && tier.InlinePrompts > 0 && inlinedPrompts >= tier.InlinePrompts {
			return collapse(b.String(), d, countPrompts(d.Items[i:]), tier)
		}
		line := itemLine(item)
		if tier.MaxBytes > 0 && b.Len()+len(line) > tier.MaxBytes {
			return collapse(b.String(), d, countPrompts(d.Items[i:]), tier)
		}
		b.WriteString(line)
		if item.Kind == schema.DigestItemPrompt {
			inlinedPrompts++
		}
	}
	return finalize(b.String(), tier)
}

func headerLine(d schema.PromptDigest) string {
	parts := []string{
		string(d.Header.Harness),
		plural(d.Header.SessionCount, "session"),
		plural(d.Header.PromptCount, "prompt"),
		fmt.Sprintf("%d/%d commits", d.Header.CommitsCovered, d.Header.CommitsTotal),
	}
	// The header carries RedactionLevel for Village's own use, but it is never
	// rendered: a session's redaction level is not a peer-facing fact.
	line := "**peasant / prompts** - " + strings.Join(parts, " - ")
	if d.Header.VillageURL != "" {
		line += " - " + d.Header.VillageURL
	}
	return line + "\n"
}

func itemLine(item schema.PromptDigestItem) string {
	switch item.Kind {
	case schema.DigestItemSession:
		return fmt.Sprintf("- session: %s, %s\n", plural(deref(item.PromptCount), "prompt"), plural(deref(item.CommitCount), "commit"))
	case schema.DigestItemPrompt:
		return fmt.Sprintf("- %d. %s\n", deref(item.Ordinal), strings.ReplaceAll(item.Text, "\n", " "))
	case schema.DigestItemSkill:
		if item.Args != "" {
			return fmt.Sprintf("- %s %s\n", item.Text, item.Args)
		}
		return fmt.Sprintf("- %s\n", item.Text)
	case schema.DigestItemCommit:
		line := fmt.Sprintf("- commit %s", item.CommitSHA)
		if item.Additions != nil {
			line += fmt.Sprintf(" (+%d/-%d, %d files)", deref(item.Additions), deref(item.Deletions), deref(item.FilesChanged))
		}
		return line + "\n"
	}
	return ""
}

// collapse emits the omitted-prompts line if it fits, then finalizes. If even the
// collapse line would cross the cap, the prefix is returned unchanged, which is
// already within the cap.
func collapse(prefix string, d schema.PromptDigest, remainingPrompts int, tier Tier) (string, error) {
	link := d.Header.VillageURL
	line := fmt.Sprintf("\n_%d more prompts omitted; full digest: %s_\n", remainingPrompts, link)
	if remainingPrompts > 0 && tier.MaxBytes > 0 && len(prefix)+len(line) > tier.MaxBytes {
		return prefix, nil
	}
	if remainingPrompts == 0 {
		return finalize(prefix, tier)
	}
	return finalize(prefix+line, tier)
}

func finalize(out string, tier Tier) (string, error) {
	if tier.MaxBytes > 0 && len(out) > tier.MaxBytes {
		return "", fmt.Errorf("rendered %s digest is %d bytes, over the %d-byte cap", tier.Name, len(out), tier.MaxBytes)
	}
	return out, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func countPrompts(items []schema.PromptDigestItem) int {
	n := 0
	for _, item := range items {
		if item.Kind == schema.DigestItemPrompt {
			n++
		}
	}
	return n
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
