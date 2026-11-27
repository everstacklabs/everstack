package secretlog

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestFingerprint(t *testing.T) {
	t.Parallel()

	if got := Fingerprint(""); got != "" {
		t.Fatalf("Fingerprint(\"\") = %q, want empty", got)
	}
	secret := "evs_act_aGVsbG8td29ybGQtdG9rZW4"
	got := Fingerprint(secret)
	if len(got) != fingerprintLength {
		t.Fatalf("Fingerprint length = %d, want %d", len(got), fingerprintLength)
	}
	if got != Fingerprint(secret) {
		t.Fatal("Fingerprint is not stable")
	}
	if strings.Contains(secret, got) {
		t.Fatalf("Fingerprint %q is a substring of the secret", got)
	}
	if got == Fingerprint(secret+"x") {
		t.Fatal("different secrets share a fingerprint")
	}
}

func TestRedactErrorRemovesURLTokens(t *testing.T) {
	t.Parallel()

	err := errors.New(`Get "https://app.everstack.ai/reset-password?token=s3cret&x=1": EOF; /verify?email=a%40b.c&code=123456`)
	got := RedactError(err).Error()
	for _, secret := range []string{"s3cret", "123456"} {
		if strings.Contains(got, secret) {
			t.Errorf("RedactError kept %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "x=1") {
		t.Errorf("RedactError removed an unrelated parameter: %q", got)
	}

	plain := errors.New("connection refused")
	if RedactError(plain) != plain {
		t.Error("RedactError replaced an error with nothing to redact")
	}
	if RedactError(nil) != nil {
		t.Error("RedactError(nil) != nil")
	}
}

// Log sinks: a call whose selector chain passes through one of these names
// (logger.Warnf, s.logger.WithFields, slog.Warn) and ends in one of the
// methods below, plus fmt.Print, fmt.Printf and fmt.Println, which some
// services use as a logger. fmt.Errorf is not a sink: its value goes back to
// a caller, which may be the credential's owner.
var (
	sinkRoots = map[string]bool{
		"logger": true, "Logger": true, "log": true, "slog": true,
	}
	fmtPrints   = map[string]bool{"Print": true, "Printf": true, "Println": true}
	sinkMethods = map[string]bool{
		"Debug": true, "Debugf": true, "Info": true, "Infof": true,
		"Warn": true, "Warnf": true, "Error": true, "Errorf": true,
		"Fatal": true, "Fatalf": true, "Print": true, "Printf": true,
		"Println": true, "WithField": true, "WithFields": true,
		"SetFields": true, "With": true, "DebugContext": true,
		"InfoContext": true, "WarnContext": true, "ErrorContext": true,
	}

	// Variables and fields that hold a bearer credential or a URL built
	// from one.
	secretIdentifiers = map[string]bool{
		"activationToken":            true,
		"inviteLink":                 true,
		"inviteURL":                  true,
		"magicLinkURL":               true,
		"pendingToken":               true,
		"PendingAuthenticationToken": true,
		"resetURL":                   true,
		"PasswordResetToken":         true,
		"tokenPrefix":                true,
		"tokenStr":                   true,
		"tokenString":                true,
		"verifyURL":                  true,
		"verificationURL":            true,
	}

	// Format strings and field keys that announce a credential.
	secretText = regexp.MustCompile(`(?i)(\btoken=|[?&]token=|token_prefix|verify_?url|reset_?url|invite_?(url|link)|magic_?link_?url)`)
)

// Directories whose log calls are checked, relative to the repository root.
var guardedDirs = []string{"cmd", "internal", "services"}

// Files allowed to log a "token_prefix" field, and why.
var tokenPrefixAllowed = map[string]string{
	// SSH tokens carry a stored display prefix (ssht_ plus a few characters,
	// shown in the console like an API key prefix) that identifies a token
	// without being enough to use it.
	"internal/ssh/proxy.go":                       "SSH token display prefix",
	"internal/sandbox/controlplane/ssh_tokens.go": "SSH token display prefix",
}

// TestLogsDoNotCarryCredentials fails when a log call names a one-time
// credential or a URL carrying one (log Fingerprint(token) instead), or when
// an http.Redirect puts err.Error() in the URL.
//
// What it cannot see: a credential that reaches a log under a name it does
// not know (a variable called link, a struct printed with %v), and a
// third-party error whose message embeds one (WorkOS errors carry the
// pending authentication token; log those through RedactError). TypeScript
// services have their own guard (services/identity/src/log-safety.test.ts).
func TestLogsDoNotCarryCredentials(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	var files []string
	for _, dir := range guardedDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "testdata", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(files) < 100 {
		t.Fatalf("found only %d Go files under %v; is the repository root right?", len(files), guardedDirs)
	}

	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, finding := range scanFile(t, path) {
			if finding.prefixKey && tokenPrefixAllowed[rel] != "" {
				continue
			}
			t.Errorf("%s:%d logs a credential (%s); log secretlog.Fingerprint(...) or nothing", rel, finding.line, finding.why)
		}
	}
}

func TestScannerFlagsCredentialLogs(t *testing.T) {
	t.Parallel()

	src := `package p
func f() {
	logger.Warnf("magic link created; token=%s", link.Token)
	logger.WithFields("token_prefix", activationToken[:10]).Debug("x")
	h.logger.Info("sent", "url", verifyURL)
	logger.Warnf("invite created; URL: %s", inviteURL)
	fmt.Printf("stored (prefix=%s)\n", tokenPrefix)
	logger.WithFields("token_sha256", secretlog.Fingerprint(link.Token)).Warn("ok")
	logger.WithFields("tokens", usage.Tokens).Info("ok")
	logger.Infof("billing tokens=%d", n)
	return fmt.Errorf("email_verification_required:%s", pendingToken)
}

var fields = map[string]any{"token_prefix": t[:10]}

func g(w http.ResponseWriter, r *http.Request, err error) {
	http.Redirect(w, r, "/login?error=auth_failed&details="+err.Error(), http.StatusFound)
	http.Redirect(w, r, "/login?error=auth_failed", http.StatusFound)
}
`
	findings := scanSource(t, "example.go", src)
	if got, want := len(findings), 7; got != want {
		t.Fatalf("got %d findings, want %d: %+v", got, want, findings)
	}
}

type finding struct {
	line      int
	why       string
	prefixKey bool
}

func scanFile(t *testing.T, path string) []finding {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return scanSource(t, path, string(src))
}

func scanSource(t *testing.T, name, src string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	var findings []finding
	ast.Inspect(file, func(node ast.Node) bool {
		// A prefix of a credential is part of the credential, wherever the
		// field is assembled.
		if lit, ok := node.(*ast.BasicLit); ok && lit.Value == `"token_prefix"` {
			findings = append(findings, finding{line: fset.Position(lit.Pos()).Line, why: "field key " + lit.Value, prefixKey: true})
			return true
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isRedirect(call.Fun) {
			// An internal error in a redirect URL lands in browser history,
			// analytics and Referer headers.
			for _, arg := range call.Args {
				if callsErrorMethod(arg) {
					findings = append(findings, finding{line: fset.Position(call.Pos()).Line, why: "err.Error() in a redirect URL"})
					break
				}
			}
			return true
		}
		if !isSink(call.Fun) {
			return true
		}
		for _, arg := range call.Args {
			if why := secretIn(arg); why != "" {
				findings = append(findings, finding{line: fset.Position(call.Pos()).Line, why: why})
				break
			}
		}
		return true
	})

	// One finding per line: a token_prefix key inside a log call matches
	// both rules.
	seen := map[int]bool{}
	unique := findings[:0]
	for _, f := range findings {
		if !seen[f.line] {
			seen[f.line] = true
			unique = append(unique, f)
		}
	}
	return unique
}

func isRedirect(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Redirect" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "http"
}

func callsErrorMethod(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && len(call.Args) == 0 {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
				found = true
			}
		}
		return !found
	})
	return found
}

// isSink reports whether fun is a logging call.
func isSink(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" {
		return fmtPrints[sel.Sel.Name]
	}
	if !sinkMethods[sel.Sel.Name] {
		return false
	}
	found := false
	ast.Inspect(sel.X, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Ident:
			if sinkRoots[n.Name] {
				found = true
			}
		case *ast.SelectorExpr:
			if sinkRoots[n.Sel.Name] {
				found = true
			}
		case *ast.CallExpr:
			// Walk the receiver chain (logger.WithError(err).Warn), not the
			// arguments of calls along it.
			ast.Inspect(n.Fun, func(inner ast.Node) bool {
				switch m := inner.(type) {
				case *ast.Ident:
					if sinkRoots[m.Name] {
						found = true
					}
				case *ast.SelectorExpr:
					if sinkRoots[m.Sel.Name] {
						found = true
					}
				}
				return !found
			})
			return false
		}
		return !found
	})
	return found
}

// secretIn returns why expr carries a credential, or "".
func secretIn(expr ast.Expr) string {
	why := ""
	ast.Inspect(expr, func(node ast.Node) bool {
		if why != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.CallExpr:
			// Fingerprint(token) and len(token) are safe to log.
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Fingerprint" {
				return false
			}
			if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "len" {
				return false
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(n.Value)
			if err != nil {
				text = n.Value
			}
			if text == "token" || secretText.MatchString(text) {
				why = "text " + n.Value
			}
		case *ast.Ident:
			if secretIdentifiers[n.Name] {
				why = "identifier " + n.Name
			}
		case *ast.SelectorExpr:
			if n.Sel.Name == "Token" || secretIdentifiers[n.Sel.Name] {
				why = "field ." + n.Sel.Name
				return false
			}
		}
		return true
	})
	return why
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
