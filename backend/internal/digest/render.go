package digest

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

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
// Village tier shows every listed prompt.
var (
	CommentTier  = Tier{Name: "comment", InlinePrompts: 10, MaxBytes: 20000}
	CheckRunTier = Tier{Name: "check-run", InlinePrompts: 25, MaxBytes: 60000}
	VillageTier  = Tier{Name: "village", InlinePrompts: 0, MaxBytes: 0}
)

// Row is what the pull request's comment and check say about one attached
// transcript besides its prompts. The digest payload is the wire contract and
// carries neither a title nor an author, so the caller supplies them.
type Row struct {
	TranscriptID schema.TranscriptID
	// Title is the transcript's title. It is rendered only when Listed.
	Title string
	// Author is the transcript owner's village handle.
	Author string
	// Listed says anyone can read the transcript. Only a listed transcript's
	// title and prompts appear on the pull request: everyone who can read the
	// pull request reads the comment and the check, and Village cannot know who
	// that is. Every other row shows its author, the commits it traced and a
	// link to read it on village, where its own readers can open it.
	Listed bool
	// URL is the transcript's page on village, linked from a listed row.
	URL string
}

// PullRequest is what the comment and the check render: the digest stored for
// the attachment (every attached transcript), one Row per transcript in it, and
// the pull request's commit set, in the order GitHub lists it. The digest
// header's VillageURL is the pull request's page on village, which every "read
// on village" link opens.
type PullRequest struct {
	Digest    schema.PromptDigest
	Rows      []Row
	CommitSet []string
}

// Per-line caps. A title or a prompt is user text of any length, and one pasted
// log must not take the whole budget: the complete text is on village. The
// commit lists are bounded so a pull request with many commits still renders a
// readable line.
const (
	maxTitleRunes       = 120
	maxPromptRunes      = 300
	maxRowCommits       = 5
	maxUncoveredCommits = 3
	shortSHA            = 7
)

const tableHead = "\n| transcript | author | commits |\n| --- | --- | --- |\n"

// Render renders the pull request's comment or check summary as plain GitHub
// Markdown within tier's budget: a bold line, a table of the attached
// transcripts (transcript, author, commits) with reference-style links, a
// <details> list of the listed transcripts' prompts, and a line naming the
// commits no transcript traces.
//
// A transcript's title and prompts appear only when its Row is Listed. Every
// piece of user text (a title, a prompt, a skill and its arguments, a handle) is
// escaped, so it renders as the text it is and never as a table cell break,
// code, HTML, a link, or a mention.
//
// It refuses an invalid digest, and a digest session with no Row, so a caller
// cannot post an inconsistent chain or leave what a transcript may show to a
// default.
func Render(in PullRequest, tier Tier) (string, error) {
	d := in.Digest
	if err := d.Validate(); err != nil {
		return "", fmt.Errorf("refusing to render an invalid prompt digest: %w", err)
	}
	if strings.TrimSpace(d.Header.VillageURL) == "" {
		return "", fmt.Errorf("refusing to render: the digest names no village page for the pull request, so no row could link to it")
	}
	rows := make(map[schema.TranscriptID]Row, len(in.Rows))
	for _, row := range in.Rows {
		rows[row.TranscriptID] = row
	}

	var order []Row
	commitsOf := map[schema.TranscriptID][]string{}
	covered := map[string]bool{}
	for _, item := range d.Items {
		switch item.Kind {
		case schema.DigestItemSession:
			row, ok := rows[item.TranscriptID]
			if !ok {
				return "", fmt.Errorf("refusing to render: the digest's transcript %s has no row, so what it may show is unknown", item.TranscriptID)
			}
			order = append(order, row)
		case schema.DigestItemCommit:
			commitsOf[item.TranscriptID] = append(commitsOf[item.TranscriptID], item.CommitSHA)
			covered[item.CommitSHA] = true
		}
	}

	// One table line per transcript with the reference definition it needs. A
	// listed row links its own page; every other row links the pull request's
	// page, where its own readers can open it.
	type line struct {
		text string
		def  string
	}
	lines := make([]line, 0, len(order))
	ref := 0
	for _, row := range order {
		label, def := "[read on village][pr]", ""
		if row.Listed {
			ref++
			title := row.Title
			if strings.TrimSpace(title) == "" {
				title = "untitled transcript"
			}
			label = fmt.Sprintf("[%s][%d]", inline(clip(title, maxTitleRunes)), ref)
			def = fmt.Sprintf("[%d]: %s\n", ref, row.URL)
		}
		lines = append(lines, line{
			text: fmt.Sprintf("| %s | %s | %s |\n", label, handle(row.Author), commitList(commitsOf[row.TranscriptID])),
			def:  def,
		})
	}

	head := boldLine(len(order), d.Header.CommitsCovered, d.Header.CommitsTotal)
	footer := "\n[view on village][pr]"
	if uncovered := uncoveredCommits(in.CommitSet, covered); len(uncovered) > 0 {
		footer += " · " + uncoveredLine(uncovered)
	}
	footer += "\n"
	prDef := "[pr]: " + d.Header.VillageURL + "\n"

	// The table is what the pull request must say, so it is laid out before any
	// prompt. Rows that would cross the cap are counted on one last row linking
	// the pull request's page.
	var table, defs strings.Builder
	if len(lines) > 0 {
		size := len(head) + len(tableHead) + len(footer) + 1 + len(prDef)
		kept := 0
		for i, l := range lines {
			reserve := 0
			if i < len(lines)-1 {
				reserve = len(moreRow(len(lines) - i))
			}
			if tier.MaxBytes > 0 && size+len(l.text)+len(l.def)+reserve > tier.MaxBytes {
				break
			}
			size += len(l.text) + len(l.def)
			kept++
		}
		table.WriteString(tableHead)
		for _, l := range lines[:kept] {
			table.WriteString(l.text)
			defs.WriteString(l.def)
		}
		if kept < len(lines) {
			table.WriteString(moreRow(len(lines) - kept))
		}
	}
	defs.WriteString(prDef)

	fixed := head + table.String()
	tail := footer + "\n" + defs.String()
	if tier.MaxBytes > 0 && len(fixed)+len(tail) > tier.MaxBytes {
		return "", fmt.Errorf("the %s digest's table is %d bytes, over the %d-byte cap", tier.Name, len(fixed)+len(tail), tier.MaxBytes)
	}

	details, err := promptDetails(d, rows, tier, tier.MaxBytes-len(fixed)-len(tail))
	if err != nil {
		return "", err
	}
	return fixed + details + tail, nil
}

// promptDetails renders the listed transcripts' prompts as a <details> block
// within budget bytes (ignored when the tier has no cap). The prompts are
// numbered over the listed transcripts alone, so the numbering says nothing
// about prompts that are not shown. A skill is listed under the prompt before
// it, as the pull request page groups it; skills before every prompt lead the
// list. When the inline count or the budget runs out, the rest are counted with
// a link to the pull request's page.
func promptDetails(d schema.PromptDigest, rows map[schema.TranscriptID]Row, tier Tier, budget int) (string, error) {
	listed, err := Restrict(d, func(id schema.TranscriptID) bool { return rows[id].Listed })
	if err != nil {
		return "", err
	}

	// One block per prompt with the skills that follow it, after one leading
	// block for the skills that come before every prompt.
	type block struct {
		text   string
		prompt bool
	}
	var blocks []block
	indent := ""
	for _, item := range listed.Items {
		switch item.Kind {
		case schema.DigestItemPrompt:
			marker := fmt.Sprintf("%d. ", deref(item.Ordinal))
			indent = strings.Repeat(" ", len(marker))
			blocks = append(blocks, block{text: marker + inline(clip(item.Text, maxPromptRunes)) + "\n", prompt: true})
		case schema.DigestItemSkill:
			text := item.Text
			if item.Args != "" {
				text += " " + item.Args
			}
			entry := "- " + inline(clip(text, maxPromptRunes)) + "\n"
			switch {
			case len(blocks) == 0:
				blocks = append(blocks, block{text: entry})
			case blocks[len(blocks)-1].prompt:
				blocks[len(blocks)-1].text += indent + entry
			default:
				blocks[len(blocks)-1].text += entry
			}
		}
	}
	if len(blocks) == 0 {
		return "", nil
	}

	total := countPrompts(listed.Items)
	summary := plural(total, "prompt")
	if total == 0 {
		summary = "skills"
	}
	open := fmt.Sprintf("\n<details>\n<summary>%s</summary>\n\n", summary)
	const closing = "\n</details>\n"
	// The longest "more" line this block can end with, reserved up front so it
	// always fits.
	moreReserve := 0
	if total > 0 {
		moreReserve = len(moreLine(total))
	}

	var out strings.Builder
	inlined, emitted := 0, 0
	for _, b := range blocks {
		if b.prompt && tier.InlinePrompts > 0 && inlined >= tier.InlinePrompts {
			break
		}
		text := b.text
		if !b.prompt {
			// The leading skills are a bullet list; a blank line ends it before
			// the numbered prompts begin.
			text += "\n"
		}
		if tier.MaxBytes > 0 && len(open)+out.Len()+len(text)+moreReserve+len(closing) > budget {
			break
		}
		out.WriteString(text)
		emitted++
		if b.prompt {
			inlined++
		}
	}

	if emitted == 0 && tier.MaxBytes > 0 && len(open)+moreReserve+len(closing) > budget {
		// Not even the block's frame fits beside the table; the table's links
		// already say where the prompts are.
		return "", nil
	}
	result := open + out.String()
	if remaining := total - inlined; remaining > 0 {
		result += moreLine(remaining)
	}
	return result + closing, nil
}

func moreLine(remaining int) string {
	return fmt.Sprintf("\n[%d more on village][pr]\n", remaining)
}

func moreRow(remaining int) string {
	return fmt.Sprintf("| [%s on village][pr] | | |\n", plural(remaining, "more transcript"))
}

// boldLine is the first line: how many transcripts are attached and how many of
// the pull request's commits they trace.
func boldLine(transcripts, covered, total int) string {
	if transcripts == 0 {
		return "**peasant / prompts · no transcripts are attached**\n"
	}
	verb := "trace"
	if transcripts == 1 {
		verb = "traces"
	}
	return fmt.Sprintf("**peasant / prompts · %s %s %d of %d commits**\n", plural(transcripts, "transcript"), verb, covered, total)
}

// commitList renders a transcript's traced commits as short SHAs, which GitHub
// links to the commit. A SHA is validated hex, never user text.
func commitList(shas []string) string {
	if len(shas) == 0 {
		return "none"
	}
	return shortList(shas, maxRowCommits)
}

// uncoveredCommits is the pull request's commits no transcript traces, in the
// order GitHub listed them.
func uncoveredCommits(commitSet []string, covered map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, sha := range commitSet {
		lower := strings.ToLower(strings.TrimSpace(sha))
		if !commitSHAPattern.MatchString(lower) || seen[lower] || covered[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, lower)
	}
	return out
}

func uncoveredLine(shas []string) string {
	if len(shas) == 1 {
		return shortList(shas, maxUncoveredCommits) + " has no transcript"
	}
	return shortList(shas, maxUncoveredCommits) + " have no transcript"
}

func shortList(shas []string, limit int) string {
	shown := shas
	if len(shown) > limit {
		shown = shown[:limit]
	}
	parts := make([]string, 0, len(shown))
	for _, sha := range shown {
		if len(sha) > shortSHA {
			sha = sha[:shortSHA]
		}
		parts = append(parts, sha)
	}
	out := strings.Join(parts, ", ")
	if extra := len(shas) - len(shown); extra > 0 {
		out += fmt.Sprintf(" and %d more", extra)
	}
	return out
}

// RenderPreview is the comment a pull request carries while its author decides:
// how many of their transcripts match, and where to review them. It names no
// transcript and no prompt.
func RenderPreview(matches int, pullURL string) string {
	subject := fmt.Sprintf("%d of your transcripts match", matches)
	if matches == 1 {
		subject = "1 of your transcripts matches"
	}
	return fmt.Sprintf("%s this pull request: [review and attach them on village](%s).\n", subject, pullURL)
}

// handle renders a village handle as "@handle" that GitHub does not turn into a
// mention: a handle is chosen on village and can name a different GitHub
// account.
func handle(author string) string {
	if strings.TrimSpace(author) == "" {
		return "unknown"
	}
	return "@<!-- -->" + inline(author)
}

// inline escapes user text for one line of GitHub Markdown: a table cell, a list
// item, or a link label. Whitespace collapses to single spaces; every character
// that opens Markdown, HTML, or math is backslash-escaped, so a pipe cannot end a
// table cell, a backtick cannot open code, and a bracket or an angle cannot open
// a link or a tag; the text cannot open a list; and an @-mention or #-reference
// is split so GitHub neither notifies an account nor cross-references an issue.
func inline(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	var b strings.Builder
	for i, r := range runes {
		switch {
		case i == 0 && (r == '-' || r == '+'):
			b.WriteByte('\\')
		case (r == '.' || r == ')') && leadingDigits(runes[:i]):
			// "1. text" or "1) text" would open an ordered list.
			b.WriteByte('\\')
		}
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>', '&', '|', '~', '#', '$':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		if (r == '@' || r == '#') && i+1 < len(runes) && isWordRune(runes[i+1]) {
			// An empty HTML comment splits the text GitHub scans for mentions and
			// references, and renders as nothing.
			b.WriteString("<!-- -->")
		}
	}
	return b.String()
}

func leadingDigits(prefix []rune) bool {
	if len(prefix) == 0 {
		return false
	}
	for _, r := range prefix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// clip shortens text to at most limit characters, marking the cut. The complete
// text is on village.
func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimRight(string(runes[:limit-1]), " ") + "…"
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
