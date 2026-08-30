package moderation_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/hosting/moderation"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCloudflareKVEnforcerAppliesTakedownAndRestore(t *testing.T) {
	var methods []string
	var values []map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method)
		if got, want := r.URL.Path, "/client/v4/accounts/account-1/storage/kv/namespaces/namespace-1/values/release-notes"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			t.Errorf("decode value: %v", err)
		}
		values = append(values, value)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
		}, nil
	})}

	edge, err := moderation.NewCloudflareKVEnforcer(moderation.CloudflareKVConfig{
		AccountID:   "account-1",
		NamespaceID: "namespace-1",
		APIToken:    "secret-token",
		BaseURL:     "https://api.cloudflare.test/client/v4",
		HTTPClient:  client,
	})
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}

	if err := edge.Apply(context.Background(), moderation.Action{
		ID: "action-1", SiteID: "site-1", Slug: "release-notes", Generation: 1,
		Kind: moderation.ActionKindTakedown, Reason: moderation.ReasonPhishing,
	}); err != nil {
		t.Fatalf("takedown: %v", err)
	}
	if err := edge.Apply(context.Background(), moderation.Action{
		ID: "action-2", SiteID: "site-1", Slug: "release-notes", Generation: 2,
		Kind: moderation.ActionKindRestore,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if len(methods) != 2 || methods[0] != http.MethodPut || methods[1] != http.MethodPut {
		t.Fatalf("methods = %v, want [PUT PUT]", methods)
	}
	if values[0]["action_id"] != "action-1" || values[0]["site_id"] != "site-1" ||
		values[0]["generation"] != float64(1) || values[0]["disabled"] != true || values[0]["reason"] != "phishing" {
		t.Fatalf("takedown value = %#v", values[0])
	}
	if values[1]["action_id"] != "action-2" || values[1]["generation"] != float64(2) || values[1]["disabled"] != false {
		t.Fatalf("restore value = %#v", values[1])
	}
}

func TestCloudflareKVEnforcerRejectsAnUnsuccessfulResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("forbidden")),
		}, nil
	})}

	edge, err := moderation.NewCloudflareKVEnforcer(moderation.CloudflareKVConfig{
		AccountID: "account-1", NamespaceID: "namespace-1", APIToken: "secret-token",
		BaseURL: "https://api.cloudflare.test/client/v4", HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := edge.Apply(context.Background(), moderation.Action{
		ID: "action-1", SiteID: "site-1", Slug: "release-notes", Generation: 1,
		Kind: moderation.ActionKindTakedown,
	}); err == nil {
		t.Fatal("403 response was accepted")
	}
}

func TestCloudflareKVEnforcerRejectsAnUnsuccessfulEnvelope(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"success":false,"errors":[{"code":10000}]}`)),
		}, nil
	})}

	edge, err := moderation.NewCloudflareKVEnforcer(moderation.CloudflareKVConfig{
		AccountID: "account-1", NamespaceID: "namespace-1", APIToken: "secret-token",
		BaseURL: "https://api.cloudflare.test/client/v4", HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := edge.Apply(context.Background(), moderation.Action{
		ID: "action-1", SiteID: "site-1", Slug: "release-notes", Generation: 1,
		Kind: moderation.ActionKindTakedown,
	}); err == nil {
		t.Fatal("success=false response was accepted")
	}
}
