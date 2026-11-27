package serve

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/everstacklabs/everstack/internal/enterprise"
	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
)

// Even holding a license, a shared gateway must not let any tenant's admin read
// or change that license's spend limits. GetPlans is public and keeps working.
func TestLicenseProxy_SharedGatewayNeverSpeaksForItsLicense(t *testing.T) {
	enterprise.SetManagedGateway(true)
	t.Cleanup(func() { enterprise.SetManagedGateway(false) })

	svc, url := proxyFixture(t, func() context.Context { return proxySessionCtx("owner") })
	c := grpcWebClient(url)

	_, err := c.GetSpendLimits(context.Background(), connect.NewRequest(&licv1.GetSpendLimitsRequest{OrganizationId: proxyTestOrg}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "GetSpendLimits: %v", err)
	_, err = c.SetSpendLimit(context.Background(), connect.NewRequest(ownSet()))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "SetSpendLimit: %v", err)
	_, err = c.DeleteSpendLimit(context.Background(), connect.NewRequest(&licv1.DeleteSpendLimitRequest{Id: proxyOwnLimitID}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "DeleteSpendLimit: %v", err)

	sets, deletes := svc.counts()
	require.Zero(t, sets+deletes, "writes reached the license service")
	require.Empty(t, svc.gets, "reads reached the license service")

	_, err = c.GetPlans(context.Background(), connect.NewRequest(&licv1.GetPlansRequest{}))
	require.NoError(t, err)
}
