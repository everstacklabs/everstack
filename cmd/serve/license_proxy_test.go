package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticAuthTransport struct{ base http.RoundTripper }

func (t staticAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer portal-service-token")
	return t.base.RoundTrip(r)
}

// The gateway's license proxy attaches the portal service credentials to
// browser requests. It must only forward the admin-UI procedures and never
// turn into an anonymous route to GenerateActivationToken or license CRUD.
func TestLicenseProxyForwardsOnlyAllowedProcedures(t *testing.T) {
	var hits []string
	var auth []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		auth = append(auth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h := newLicenseProxyHandler(backend.URL, staticAuthTransport{base: http.DefaultTransport}, boundIdentity)

	call := func(proc string) int {
		body := "{}"
		if proc != "GetPlans" {
			body = `{"organizationId":"` + proxyTestOrg + `"}`
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/everstack.license.v1.LicenseService/"+proc, strings.NewReader(body))
		req = req.WithContext(proxySessionCtx("owner"))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, call("GetPlans"))
	require.Equal(t, http.StatusOK, call("GetSpendLimits"))
	require.Equal(t, http.StatusOK, call("SetSpendLimit"))
	require.Len(t, hits, 3)
	for _, a := range auth {
		require.Equal(t, "Bearer portal-service-token", a)
	}

	for _, proc := range []string{"GenerateActivationToken", "CreateLicense", "UpdateLicense", "DeleteLicense", "GetLicense", "ValidateLicense", "UpdateSpendUsage"} {
		require.Equal(t, http.StatusForbidden, call(proc), proc)
	}
	require.Len(t, hits, 3, "denied procedures must never reach the license service")

	// CORS preflight still answered locally.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/everstack.license.v1.LicenseService/GenerateActivationToken", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, hits, 3)
}
