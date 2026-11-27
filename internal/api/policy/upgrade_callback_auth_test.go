package policy

import "testing"

// StoreUpgradeCallbackSecret registers the nonce ActivationCallback accepts
// before rebinding the gateway's license, so it must require authentication.
func TestStoreUpgradeCallbackSecretRequiresAuthentication(t *testing.T) {
	t.Parallel()

	p := NewDefaultPolicy()
	const proc = "/everstack.gateway.v1.GatewayService/StoreUpgradeCallbackSecret"
	if p.ShouldBypassProcedure(proc) || p.ShouldBypassPath(proc) {
		t.Fatalf("%s must not bypass authentication", proc)
	}
}
