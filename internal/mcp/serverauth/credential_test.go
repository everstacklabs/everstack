package serverauth

import (
	"net/http"
	"testing"

	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestPresentedKeyParsingIsUnambiguous(t *testing.T) {
	for _, tc := range []struct {
		name               string
		headers            map[string][]string
		key                string
		present, malformed bool
	}{
		{"absent", nil, "", false, false},
		{"canonical", map[string][]string{"x-evs-api-key": {" key "}}, "key", true, false},
		{"alias", map[string][]string{"x-mf-api-key": {"key"}}, "key", true, false},
		{"second alias", map[string][]string{"x-everstack-api-key": {"key"}}, "key", true, false},
		{"bearer precedence", map[string][]string{"Authorization": {"Bearer bearer-key"}, "x-evs-api-key": {"header-key"}}, "bearer-key", true, false},
		{"canonical precedence", map[string][]string{"x-evs-api-key": {"canonical"}, "x-mf-api-key": {"legacy"}}, "canonical", true, false},
		{"nbsp", map[string][]string{"Authorization": {"\u00a0Bearer key\u00a0"}}, "key", true, false},
		{"tabs", map[string][]string{"Authorization": {"\tBearer\tkey\t"}}, "key", true, false},
		{"lower case", map[string][]string{"Authorization": {"bearer key"}}, "key", true, false},
		{"empty bearer", map[string][]string{"Authorization": {"Bearer \t\u00a0"}}, "", true, true},
		{"empty canonical", map[string][]string{"x-evs-api-key": {" \t\u00a0"}}, "", true, true},
		{"blank alias falls through to a valid one", map[string][]string{"x-mf-api-key": {" \t"}, "x-everstack-api-key": {"key"}}, "key", true, false},
		{"lower empty alias does not shadow canonical", map[string][]string{"x-evs-api-key": {"key"}, "x-mf-api-key": {" "}}, "key", true, false},
		{"duplicate authorization", map[string][]string{"Authorization": {"Bearer key", "Bearer other"}}, "", true, true},
		{"repeated identical key", map[string][]string{"x-evs-api-key": {"key", "key"}}, "key", true, false},
		{"case duplicate", map[string][]string{"X-Evs-Api-Key": {"key"}, "x-evs-api-key": {"other"}}, "", true, true},
		{"merged duplicate", map[string][]string{"Authorization": {"Bearer key,other"}}, "", true, true},
		{"another scheme is not an Everstack credential", map[string][]string{"Authorization": {"Basic x"}}, "", false, false},
		{"another scheme does not shadow the key header", map[string][]string{"Authorization": {"Basic x"}, "x-evs-api-key": {"key"}}, "key", true, false},
		{"empty bearer falls through to the key header", map[string][]string{"Authorization": {"Bearer "}, "x-evs-api-key": {"key"}}, "key", true, false},
		{"blank authorization", map[string][]string{"Authorization": {"\t"}}, "", true, true},
		{"whitespace within key", map[string][]string{"x-evs-api-key": {"key\tother"}}, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req(t, "", "")
			r.Header = http.Header(tc.headers)
			key, present, malformed := ExtractPresentedKey(r)
			if key != tc.key || present != tc.present || malformed != tc.malformed {
				t.Fatalf("got key-match=%v presented=%v malformed=%v", key == tc.key, present, malformed)
			}
			auth := newWithLookup(fakeLookup{result: apikeyq.APIKeyReadModel{ID: "id", OrgID: strptr("tenant")}}, nil)
			if auth.PresentsCredential(r) != tc.present {
				t.Fatal("presence disagrees with parser")
			}
			if _, ok := auth.Authenticate(r); ok != (tc.present && !tc.malformed) {
				t.Fatal("authentication disagrees with parser")
			}
		})
	}
}
