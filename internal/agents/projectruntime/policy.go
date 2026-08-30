package projectruntime

import "fmt"

// ValidateFunctionSandboxPolicy enforces the raw agent configuration required
// before revision-local code can be activated or registered. It intentionally
// validates presence instead of parsed defaults: ordinary sandboxes default to
// open egress, while project code must make its egress policy explicit.
func ValidateFunctionSandboxPolicy(config map[string]interface{}) error {
	raw, ok := config["sandbox"]
	if !ok {
		return fmt.Errorf("project functions require explicit sandbox configuration")
	}
	sandboxConfig, ok := raw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("project functions require sandbox to be an object")
	}
	enabled, ok := sandboxConfig["enabled"].(bool)
	if !ok || !enabled {
		return fmt.Errorf("project functions require sandbox.enabled must be true")
	}
	mode, ok := sandboxConfig["network_mode"].(string)
	if !ok || (mode != "deny" && mode != "whitelist" && mode != "allow") {
		return fmt.Errorf("project functions require sandbox.network_mode must be deny, whitelist or allow")
	}
	return nil
}
