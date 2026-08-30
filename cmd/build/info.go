package build

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	version  = ""
	commit   = ""
	date     = ""
	dateTime time.Time
)

func Version() string {
	if version != "" {
		return version
	}
	// Prefer env override for development runs
	if v := strings.TrimSpace(os.Getenv("EVS_VERSION")); v != "" {
		version = v
		return version
	}
	// Prefer exact tag at HEAD (released version)
	if v := strings.TrimSpace(gitDescribeExact()); v != "" {
		version = v
		return version
	}
	// Fallback to latest tag in repository
	if v := strings.TrimSpace(gitLatestTag()); v != "" {
		version = v
		return version
	}
	// Fallback to short commit if available
	if c := strings.TrimSpace(gitCommitShort()); c != "" {
		version = c
		return version
	}
	// Final fallback: RFC3339 timestamp
	version = Date().Format(time.RFC3339)
	return version
}

func Commit() string {
	if commit != "" {
		return commit
	}
	if c := strings.TrimSpace(gitCommit()); c != "" {
		commit = c
	}
	return commit
}

func Date() time.Time {
	if !dateTime.IsZero() {
		return dateTime
	}
	dateTime, _ = time.Parse(time.RFC3339, date)
	if dateTime.IsZero() {
		if ds := strings.TrimSpace(gitCommitDateISO()); ds != "" {
			if t, err := time.Parse(time.RFC3339, ds); err == nil {
				dateTime = t
			}
		}
		if dateTime.IsZero() {
			dateTime = time.Now()
		}
	}
	return dateTime
}

// --- git helpers used for development builds (when ldflags are not provided) ---

func gitDescribeExact() string {
	return execGit("describe", "--tags", "--exact-match")
}

func gitLatestTag() string {
	return execGit("describe", "--tags", "--abbrev=0")
}

func gitCommitShort() string {
	return execGit("rev-parse", "--short", "HEAD")
}

func gitCommit() string {
	return execGit("rev-parse", "HEAD")
}

func gitCommitDateISO() string {
	return execGit("show", "-s", "--format=%cI", "HEAD")
}

// readVersionFile reads the version from .github/version file if it exists
func readVersionFile() string {
	// Try current directory first
	data, err := os.ReadFile(".github/version")
	if err == nil {
		return strings.TrimSpace(string(data))
	}

	// Try finding git root and reading from there
	gitRoot := execGit("rev-parse", "--show-toplevel")
	if gitRoot != "" {
		data, err = os.ReadFile(gitRoot + "/.github/version")
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	return ""
}

func execGit(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
