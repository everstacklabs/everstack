package policy

import "testing"

// Shadow-metered paths are observed log-only. Promoting a path to
// MeteredPathPrefixes changes trial/spend behavior, so the two lists must
// stay disjoint and the shadow predicate must never match production paths.
func TestShouldShadowMeterRequest(t *testing.T) {
	p := NewDefaultPolicy()

	shadow := []string{
		"/v1/responses",
		"/v1/rerank",
		"/everstack.gateway.v1.GatewayService/Speech",
		"/everstack.gateway.v1.GatewayService/Transcription",
		"/everstack.gateway.v1.GatewayService/Translation",
		"/everstack.gateway.v1.GatewayService/ImageGeneration",
		"/everstack.gateway.v1.GatewayService/ImageEdit",
		"/everstack.gateway.v1.GatewayService/ImageVariation",
		"/everstack.gateway.v1.GatewayService/Moderation",
		"/everstack.gateway.v1.GatewayService/Rerank",
		"/everstack.gateway.v1.GatewayService/CreateResponse",
	}
	for _, path := range shadow {
		if !p.ShouldShadowMeterRequest(path) {
			t.Errorf("ShouldShadowMeterRequest(%q) = false, want true", path)
		}
		if p.ShouldMeterRequest(path) {
			t.Errorf("ShouldMeterRequest(%q) = true, shadow path must stay out of production prefixes", path)
		}
	}

	production := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/audio/speech",
		"/v1/moderations",
		"/everstack.gateway.v1.GatewayService/ChatCompletion",
		"/everstack.gateway.v1.GatewayService/Embeddings",
	}
	for _, path := range production {
		if p.ShouldShadowMeterRequest(path) {
			t.Errorf("ShouldShadowMeterRequest(%q) = true, production path must never be shadow", path)
		}
	}

	// Metadata-only response ops are deliberately excluded.
	for _, path := range []string{
		"/everstack.gateway.v1.GatewayService/GetResponse",
		"/everstack.gateway.v1.GatewayService/CancelResponse",
		"/everstack.gateway.v1.GatewayService/DeleteResponse",
		"/everstack.gateway.v1.GatewayService/ListResponses",
		"/v1/models",
		"",
	} {
		if p.ShouldShadowMeterRequest(path) {
			t.Errorf("ShouldShadowMeterRequest(%q) = true, want false", path)
		}
	}

	var nilP *Policy
	if nilP.ShouldShadowMeterRequest("/v1/responses") {
		t.Error("nil policy must not shadow-match")
	}
}
