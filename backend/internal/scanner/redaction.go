package scanner

import (
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
		if p.pattern.MatchString(text) {
			// Findings are rule identifiers, never matched transcript data.
			issues = append(issues, p.name)
		}
	}

	return issues
}

// FormatScanErrors produces a user-friendly rejection message.
func FormatScanErrors(issues []string) string {
	// This is the outward diagnostic boundary. Even an unexpected collaborator
	// result cannot inject a credential, native key, path or payload preview.
	var safe []string
	for _, rule := range patterns {
		for _, issue := range issues {
			if issue == rule.name {
				safe = append(safe, rule.name)
				break
			}
		}
	}
	if len(safe) == 0 {
		safe = append(safe, "Sensitive content")
	}
	return "Redaction check failed. Potential secrets detected:\n- " + strings.Join(safe, "\n- ") +
		"\n\nPlease ensure the transcript is properly redacted before publishing."
}
