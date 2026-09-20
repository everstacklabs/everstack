package authratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const authPrefix = "/everstack.auth.v1.AuthService/"

// On instance hosts the Connect surface is the only way to reach some of
// these. RequestMagicLink in particular has no HTTP route, so if it were
// missing here the endpoint that sends mail to a caller-supplied address
// would have no limit at all.
func TestSensitiveProcedureCoversTheCredentialSurface(t *testing.T) {
	for _, proc := range []string{
		"Login",
		"Register",
		"RequestMagicLink",
		"VerifyMagicLink",
		"RequestPasswordReset",
		"CompletePasswordReset",
		"CompleteEmailVerification",
		"InviteTeamMember",
		"AcceptInvitation",
		"ExchangeDeviceCode",
		"ApproveDeviceAuthorization",
	} {
		if !SensitiveProcedure(authPrefix + proc) {
			t.Errorf("%s must be limited: it mints, mails, or consumes a credential", proc)
		}
	}
}

func TestSensitiveProcedureLeavesReadPathsAlone(t *testing.T) {
	for _, proc := range []string{"GetSession", "GetAuthMode", "ListTeamMembers", "SignOut"} {
		if SensitiveProcedure(authPrefix + proc) {
			t.Errorf("%s should not be limited: throttling it slows the dashboard and closes no hole", proc)
		}
	}
	if SensitiveProcedure("nopath") {
		t.Error("a path with no separator should not match")
	}
}

// WrapSensitive must limit the named procedures and pass everything else
// through, so a burst spent on Login cannot lock the dashboard's session
// polling out.
func TestWrapSensitiveOnlyLimitsTheNamedProcedures(t *testing.T) {
	rl := NewAuthRateLimiter(60, 1)
	defer rl.Stop()

	handler := rl.WrapSensitive(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.RemoteAddr = "198.51.100.20:5555"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call(authPrefix + "RequestMagicLink"); code != http.StatusOK {
		t.Fatalf("first magic-link call got %d, want 200", code)
	}
	if code := call(authPrefix + "RequestMagicLink"); code != http.StatusTooManyRequests {
		t.Errorf("second magic-link call got %d, want 429", code)
	}
	// Same caller, same spent bucket, but a read path must still work.
	for i := range 5 {
		if code := call(authPrefix + "GetSession"); code != http.StatusOK {
			t.Errorf("GetSession call %d got %d, want 200", i+1, code)
		}
	}
}
