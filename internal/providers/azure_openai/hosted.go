package azure_openai

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/everstacklabs/everstack/internal/providers/anthropic"
	"github.com/everstacklabs/everstack/internal/providers/openai"
)

// Azure serves three wire formats from one resource:
//
//   - classic deployments: /openai/deployments/{deployment}/chat/completions
//     with a dated api-version (the original path in this package);
//   - the v1 Responses API: /openai/v1/responses with no api-version, which
//     is the only way to call the GPT-6 family and the Codex models;
//   - Claude models: /anthropic/v1/messages on the Foundry host.
//
// The second and third are the same formats OpenAI and Anthropic serve
// first-party, so they reuse those clients with Azure's base URL and
// credential header rather than a third implementation.

type route int

const (
	routeDeployments route = iota
	routeResponses
	routeClaude
)

const (
	apiTypeResponses = "responses"
	apiTypeAnthropic = "anthropic"

	openAIHostSuffix  = ".openai.azure.com"
	foundryHostSuffix = ".services.ai.azure.com"
)

// routeFor picks the wire format for a model. The catalog's api_type decides
// when the model has an entry; a "claude-" id is treated as Claude even
// without one, since Azure defaults a Claude deployment's name to the model
// id and no Claude model answers on the OpenAI paths.
func (p *Provider) routeFor(model string) route {
	apiType := ""
	if p.cfg.APITypeForModel != nil {
		apiType = strings.ToLower(strings.TrimSpace(p.cfg.APITypeForModel(model)))
	}
	switch {
	case apiType == apiTypeAnthropic, isClaudeModel(model):
		return routeClaude
	case apiType == apiTypeResponses:
		return routeResponses
	}
	return routeDeployments
}

func isClaudeModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude-")
}

// resourceURL parses the configured base URL and rejects the unfilled
// "{resource}" placeholder from the default spec.
func (p *Provider) resourceURL() (*url.URL, error) {
	raw := strings.TrimSpace(strings.TrimRight(p.cfg.BaseURL, "/"))
	if raw == "" || strings.Contains(raw, "{resource}") {
		return nil, fmt.Errorf("azure-openai base_url must point to an Azure OpenAI resource, got %q", p.cfg.BaseURL)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("azure-openai base_url is not a valid URL: %q", p.cfg.BaseURL)
	}
	return u, nil
}

// responsesBaseURL is the v1 base the Responses API lives under:
// https://{resource}.openai.azure.com/openai/v1.
func (p *Provider) responsesBaseURL() (string, error) {
	u, err := p.resourceURL()
	if err != nil {
		return "", err
	}
	return u.Scheme + "://" + u.Host + "/openai/v1", nil
}

// claudeBaseURL is the base the Messages API lives under:
// https://{resource}.services.ai.azure.com/anthropic. Microsoft documents
// Claude only on the Foundry host, so a resource configured with its
// openai.azure.com name is rewritten to the Foundry name of the same
// resource. Only the hostname suffix is rewritten; any other host is used as
// given so a private endpoint or proxy keeps working.
func (p *Provider) claudeBaseURL() (string, error) {
	if override := strings.TrimSpace(strings.TrimRight(p.cfg.AnthropicBaseURL, "/")); override != "" {
		return override, nil
	}
	u, err := p.resourceURL()
	if err != nil {
		return "", err
	}
	host := u.Host
	if lower := strings.ToLower(host); strings.HasSuffix(lower, openAIHostSuffix) {
		host = host[:len(host)-len(openAIHostSuffix)] + foundryHostSuffix
	}
	return u.Scheme + "://" + host + "/anthropic", nil
}

// responsesClient returns the OpenAI client pointed at this resource's v1
// API. Every model it is asked for goes to /responses.
func (p *Provider) responsesClient() (*openai.Provider, error) {
	p.hostedOnce.Do(p.buildHosted)
	if p.responsesErr != nil {
		return nil, p.responsesErr
	}
	return p.responses, nil
}

// claudeClient returns the Anthropic client pointed at this resource's
// Messages API.
func (p *Provider) claudeClient() (*anthropic.Provider, error) {
	p.hostedOnce.Do(p.buildHosted)
	if p.claudeErr != nil {
		return nil, p.claudeErr
	}
	return p.claude, nil
}

func (p *Provider) buildHosted() {
	key := strings.TrimSpace(p.cfg.APIKey)

	if base, err := p.responsesBaseURL(); err != nil {
		p.responsesErr = err
	} else {
		var httpClient *http.Client
		if p.cfg.HTTPClient != nil {
			// The OpenAI client wraps the transport it is given; hand it a copy
			// so the caller's client is not wrapped a second time.
			copied := *p.cfg.HTTPClient
			httpClient = &copied
		}
		p.responses = openai.NewProvider(openai.Config{
			APIKey:          key,
			BaseURL:         base,
			HTTPClient:      httpClient,
			ProviderName:    p.Name(),
			APITypeForModel: func(string) string { return apiTypeResponses },
			SetAuth:         func(h http.Header) { h.Set("api-key", key) },
		})
	}

	if base, err := p.claudeBaseURL(); err != nil {
		p.claudeErr = err
	} else {
		// Foundry accepts the resource key in x-api-key, which is what the
		// Anthropic client already sends.
		p.claude = anthropic.NewProvider(anthropic.Config{
			APIKey:       key,
			BaseURL:      base,
			ProviderName: p.Name(),
		})
	}
}
