package scanner

import (
	"fmt"
	"regexp"
	"strings"
)

var patterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"AWS Access Key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"AWS Secret Key", regexp.MustCompile(`(?i)aws_secret_access_key\s*=\s*\S+`)},
	{"GitHub Token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`)},
	{"SSH Private Key", regexp.MustCompile(`-----BEGIN (RSA|DSA|EC|OPENSSH) PRIVATE KEY-----`)},
	{"Generic API Key", regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9_\-]{20,}`)},
	{"Home Directory Path", regexp.MustCompile(`/Users/[a-zA-Z0-9._-]+/`)},
	{"Windows User Path", regexp.MustCompile(`C:\\Users\\[a-zA-Z0-9._-]+\\`)},
	{"Slack Token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z\-]+`)},
	{"Private Key Value", regexp.MustCompile(`(?i)private[_-]?key\s*[:=]\s*["']?\S{20,}`)},
}

// ScanForSecrets checks the content for common secret patterns.
// Returns a list of detected issues. Empty list means content is clean.
func ScanForSecrets(content []byte) []string {
	text := string(content)
	var issues []string

	for _, p := range patterns {
		matches := p.pattern.FindAllString(text, 3)
		if len(matches) > 0 {
			preview := matches[0]
			if len(preview) > 40 {
				preview = preview[:40] + "..."
			}
			issues = append(issues, fmt.Sprintf("%s detected (e.g. %q)", p.name, preview))
		}
	}

	return issues
}

// FormatScanErrors produces a user-friendly rejection message.
func FormatScanErrors(issues []string) string {
	return "Redaction check failed. Potential secrets detected:\n- " + strings.Join(issues, "\n- ") +
		"\n\nPlease ensure the transcript is properly redacted before publishing."
}
