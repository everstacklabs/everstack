package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	checkInterval = 24 * time.Hour
	// The releases repo lives under the everstacklabs org. This previously
	// pointed at "everstack", which 404s, so the check silently never fired.
	githubTagsURL  = "https://api.github.com/repos/everstacklabs/everstack/tags?per_page=30"
	upgradeHelpURL = "https://docs.everstack.ai/getting-started/installation"
)

type cacheFile struct {
	LastChecked time.Time `json:"lastChecked"`
	LatestTag   string    `json:"latestTag"`
}

// CheckForUpdate starts a non-blocking check for a newer Everstack version and
// prints a short message to stderr if one is available. It never fails the run.
func CheckForUpdate(currentVersion string) {
	// Allow opt-out. Return silently: announcing the opt-out on every single
	// invocation defeats the point of asking for quiet, and it polluted stderr
	// for scripted and CI callers that set this precisely to avoid noise.
	if strings.EqualFold(os.Getenv("EVS_NO_UPDATE_CHECK"), "1") || strings.EqualFold(os.Getenv("EVS_NO_UPDATE_CHECK"), "true") {
		return
	}

	go func() {
		defer func() { _ = recover() }()

		current := normalizeVersion(currentVersion)

		latest := readCachedLatest()
		if latest != "" && semver.IsValid(latest) && semver.Compare(latest, current) > 0 {
			printHint(currentVersion, latest)
		}

		if !shouldCheckNetwork() {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if v := fetchLatestTag(ctx); v != "" {
			writeCachedLatest(v)
			if semver.IsValid(v) && semver.Compare(v, current) > 0 {
				printHint(currentVersion, v)
			}
		}
	}()
}

func normalizeVersion(v string) string {
	// Accepts inputs like "v0.1.0", "0.1.0", "development-...", commit hashes, timestamps
	v = strings.TrimSpace(v)
	if v == "" {
		return "v0.0.0"
	}
	if strings.HasPrefix(v, "v") {
		if semver.IsValid(v) {
			return v
		}
	} else if semver.IsValid("v" + v) {
		return "v" + v
	}
	// Non-semver versions get treated as v0.0.0 for comparison so we only notify on stable/rc tags
	return "v0.0.0"
}

func shouldCheckNetwork() bool {
	cf, _ := cachePath()
	b, err := os.ReadFile(cf)
	if err != nil {
		return true
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil {
		return true
	}
	return time.Since(c.LastChecked) >= checkInterval
}

func readCachedLatest() string {
	cf, _ := cachePath()
	b, err := os.ReadFile(cf)
	if err != nil {
		return ""
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.LatestTag
}

func writeCachedLatest(tag string) {
	cf, dir := cachePath()
	_ = os.MkdirAll(dir, 0o755)
	b, _ := json.Marshal(cacheFile{LastChecked: time.Now(), LatestTag: tag})
	_ = os.WriteFile(cf, b, 0o644)
}

func cachePath() (filePath, dir string) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir = filepath.Join(base, "everstack")
	return filepath.Join(dir, "update-check.json"), dir
}

func fetchLatestTag(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubTagsURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "everstack-update-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &tags) != nil || len(tags) == 0 {
		return ""
	}
	max := ""
	for _, t := range tags {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "v") {
			name = "v" + name
		}
		if !semver.IsValid(name) {
			continue
		}
		if max == "" || semver.Compare(name, max) > 0 {
			max = name
		}
	}
	return max
}

func printHint(current, latest string) {
	// Keep output minimal and unobtrusive
	fmt.Fprintf(os.Stderr, "\nA new Everstack version is available: %s (you have %s)\n", latest, current)
	fmt.Fprintf(os.Stderr, "Upgrade: curl -fsSL https://get.everstack.ai/install.sh | bash  (or: brew upgrade evs)\n")
	fmt.Fprintf(os.Stderr, "Details: %s\n", upgradeHelpURL)
	fmt.Fprintln(os.Stderr, "Disable checks with --no-update-check or EVS_NO_UPDATE_CHECK=1.")
}
