package service

import (
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/types"
)

// Every variant renders through its real data builder, with no missing key,
// no dash or legacy brand, the adaptive logo, the organization by display
// name, and (for the redesigned ones) the shared footer.
func TestEveryEmailVariantRendersClean(t *testing.T) {
	cfg := config.DefaultConfig()
	seen := map[types.EmailType]bool{}
	names := map[string]bool{}
	for _, p := range Previews() {
		if names[p.Name] {
			t.Fatalf("duplicate preview name %q", p.Name)
		}
		names[p.Name] = true
		r, err := RenderPreview(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		seen[p.Type] = true
		assertClean(t, r.Subject, r.HTML)
		if !strings.Contains(r.HTML, "evs-logo-chip") {
			t.Errorf("%s: logo is not the adaptive chip", p.Name)
		}
		if p.Note != "" {
			continue
		}
		text := visibleText(r.HTML)
		if strings.Contains(text+r.Subject, "acme organization") || strings.Contains(text, " - ") {
			t.Errorf("%s: slug as organization name, or a spaced hyphen as a dash:\n%s", p.Name, text)
		}
		if !strings.Contains(text, "You are receiving this because") {
			t.Errorf("%s: no footer saying why the email was sent", p.Name)
		}
		// One primary button; the verification code email has none (the
		// code is entered, not clicked).
		want := 1
		if p.Type == types.EmailTypeEmailVerification {
			want = 0
		}
		if got := strings.Count(r.HTML, "display: inline-block; background: #18181b;"); got != want {
			t.Errorf("%s: %d primary buttons, want %d", p.Name, got, want)
		}
	}
	// Every template that is sent today has at least one preview.
	for _, typ := range []types.EmailType{
		types.EmailTypeMagicLink, types.EmailTypePasswordReset, types.EmailTypeEmailVerification, types.EmailTypeInvitation, types.EmailTypeWelcome,
		types.EmailTypeSubscriptionStarted, types.EmailTypeSubscriptionChanged, types.EmailTypeCreditPurchased, types.EmailTypeCreditReversed,
		types.EmailTypeAutoTopUpCharged, types.EmailTypeAutoTopUpFailed, types.EmailTypeSpendLimit,
		types.EmailTypePaymentSuccess, types.EmailTypePaymentFailed, types.EmailTypeCreditLowBalance,
		types.EmailTypeInstanceOverLimit, types.EmailTypeInstanceOverLimitReminder, types.EmailTypeInstancesPaused,
		types.EmailTypeCardAdded, types.EmailTypeCardRemoved, types.EmailTypeCardDefaultChanged, types.EmailTypeCardExpiring,
	} {
		if !seen[typ] {
			t.Errorf("no preview for %s", typ)
		}
	}
}

const previewIndex = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Everstack email previews</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f4f4f5; color: #18181b; padding: 32px 16px; max-width: 860px; margin: 0 auto; }
  h1 { font-size: 22px; } p.lead { color: #52525b; }
  .item { background: #fff; border-radius: 8px; padding: 14px 18px; margin-bottom: 10px; box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
  .name { font-family: ui-monospace, monospace; font-size: 12px; color: #6366f1; }
  .subject { font-weight: 500; margin: 4px 0; } .subject a { color: #18181b; text-decoration: none; } .subject a:hover { text-decoration: underline; }
  .meta { color: #71717a; font-size: 13px; } .note { color: #b45309; font-size: 12px; }
</style></head><body>
<h1>Everstack email previews</h1>
<p class="lead">Every variant of every transactional email, rendered through the same data builders and templates production uses. {{len .}} files.</p>
{{range .}}<div class="item"><div class="name">{{.Name}} &middot; {{.Type}}</div>
<div class="subject"><a href="{{.File}}">{{.Subject}}</a></div>
<div class="meta">From: {{.From}}{{if .ReplyTo}} &middot; Reply-To: {{.ReplyTo}}{{end}}</div>{{if .Note}}<div class="note">{{.Note}}</div>{{end}}</div>
{{end}}</body></html>
`

type previewEntry struct{ Name, Type, Subject, From, ReplyTo, File, Note string }

// TestWriteAllEmailPreviews writes every variant to $EVS_EMAIL_PREVIEW_DIR,
// one file each, plus an index.html that also lists the identity service's
// previews (identity-*.html) found there. Skipped when unset.
func TestWriteAllEmailPreviews(t *testing.T) {
	dir := os.Getenv("EVS_EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("EVS_EMAIL_PREVIEW_DIR not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	var entries []previewEntry
	for _, p := range Previews() {
		r, err := RenderPreview(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		file := p.Name + ".html"
		html := strings.Replace(r.HTML, "<body", "<!-- Subject: "+r.Subject+" -->\n<body", 1)
		if err := os.WriteFile(filepath.Join(dir, file), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, previewEntry{p.Name, string(p.Type), r.Subject, r.From, r.ReplyTo, file, p.Note})
	}
	identity, _ := filepath.Glob(filepath.Join(dir, "identity-*.html"))
	sort.Strings(identity)
	for _, path := range identity {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		subject := ""
		if i := strings.Index(string(b), "<!-- Subject: "); i >= 0 {
			rest := string(b)[i+len("<!-- Subject: "):]
			subject = rest[:strings.Index(rest, " -->")]
		}
		name := strings.TrimSuffix(filepath.Base(path), ".html")
		entries = append(entries, previewEntry{name, "identity service (TypeScript)", subject, "EVS_IDENTITY_EMAIL_FROM", "", filepath.Base(path), ""})
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := template.Must(template.New("index").Parse(previewIndex)).Execute(f, entries); err != nil {
		t.Fatal(err)
	}
}

// internalDetail matches what a customer should never see: Stripe object ids,
// UUIDs, tier codes, snake_case status enums, and our vendor or plumbing
// words. Support finds anything else by organization and time.
var internalDetail = []struct {
	what string
	re   *regexp.Regexp
}{
	{"Stripe object id", regexp.MustCompile(`\b(pi|ch|in|cs|cus|pm|sub|si|re|dp|evt|seti|price|prod|txn|acct|ii|il|py|src|card|ba|po|tr|cn)_[A-Za-z0-9]{3,}`)},
	{"UUID", regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)},
	{"tier code", regexp.MustCompile(`\b(free|basic|pro|scale|enterprise|starter)\b`)},
	{"snake_case status or key", regexp.MustCompile(`\b[a-z0-9]+_[a-z0-9_]+\b`)},
	{"vendor or plumbing word", regexp.MustCompile(`(?i)\b(stripe|dedupe|ledger|webhook|idempotency|reconcil\w*|payload|outbox)\b|outside Everstack|Everstack, automatically`)},
}

func TestNoEmailShowsInternalDetails(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, p := range Previews() {
		r, err := RenderPreview(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		shown := r.Subject + "\n" + visibleText(r.HTML)
		for _, d := range internalDetail {
			if m := d.re.FindString(shown); m != "" {
				t.Errorf("%s shows a %s: %q", p.Name, d.what, m)
			}
		}
	}
	// The guard itself catches what it is meant to.
	for _, bad := range []string{"pi_3QxYz", "re_1Abc", "in_1Q2w3e", "4f6c2a7e-0b1d-4c8e-9a3f-2b7d6e1c9a05", "basic", "past_due", "Stripe reference", "Everstack, automatically"} {
		hit := false
		for _, d := range internalDetail {
			hit = hit || d.re.MatchString(bad)
		}
		if !hit {
			t.Errorf("guard misses %q", bad)
		}
	}
}
