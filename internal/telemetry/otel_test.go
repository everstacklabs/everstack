package telemetry

import "testing"

func TestCreateResourceIncludesDeploymentEnvironment(t *testing.T) {
	t.Parallel()

	resource, err := createResource(Config{
		ServiceName:           "everstack-gateway",
		ServiceVersion:        "test",
		TenantType:            "cloud",
		InstanceOwner:         "everstack",
		DeploymentEnvironment: "production",
	})
	if err != nil {
		t.Fatalf("createResource() error = %v", err)
	}

	attributes := map[string]string{}
	for _, attr := range resource.Attributes() {
		attributes[string(attr.Key)] = attr.Value.AsString()
	}
	if got := attributes["deployment.environment"]; got != "production" {
		t.Fatalf("deployment.environment = %q, want production", got)
	}
}
