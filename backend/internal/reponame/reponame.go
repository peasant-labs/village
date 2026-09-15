// Package reponame applies Village's single rule for reading a repository name
// out of the two places a published transcript can carry one: the stored git
// remote, and the transcript's project path.
//
// It is deliberately its own package. The publish path uses the rule to derive
// a project's display name, and the pull-request matcher uses it to decide
// whether a transcript belongs to a pull request's repository. Two copies of
// the rule would let those answers drift, so the rule is one pure function that
// both callers import.
package reponame

import "strings"

// Normalize derives the repository name a project refers to.
//
// It prefers the git remote URL (e.g. "github.com/example-org/sample-app.git" →
// "sample-app"), falling back to stripping known directory prefixes from the
// dash-delimited project name key
// (e.g. "-Users-developer-Documents-GitHub-sample-app" → "sample-app").
//
// An empty string means neither input named a repository; callers treat that as
// "no repository", never as a match.
func Normalize(projectName string, gitRemote string) string {
	// Try git remote first
	if gitRemote != "" {
		remote := strings.TrimSuffix(gitRemote, ".git")
		if idx := strings.LastIndex(remote, "/"); idx >= 0 {
			name := remote[idx+1:]
			if name != "" {
				return name
			}
		}
	}

	if projectName == "" {
		return ""
	}

	// Slash-separated path: take last segment
	if strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				return parts[i]
			}
		}
	}

	// Dash-delimited path key (e.g. "-Users-developer-Documents-GitHub-project-name")
	// Strip leading dash, split into segments, find the last known directory
	// marker and take everything after it as the project name.
	knownDirs := []string{"github", "documents", "projects", "repos", "src", "code", "dev", "home"}
	stripped := strings.TrimPrefix(projectName, "-")
	segments := strings.Split(stripped, "-")

	lastKnownIdx := -1
	for i, seg := range segments {
		lower := strings.ToLower(seg)
		for _, d := range knownDirs {
			if lower == d {
				lastKnownIdx = i
				break
			}
		}
	}

	if lastKnownIdx >= 0 && lastKnownIdx < len(segments)-1 {
		return strings.Join(segments[lastKnownIdx+1:], "-")
	}

	return projectName
}
