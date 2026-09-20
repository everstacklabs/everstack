package plans

import (
	"encoding/json"
	"testing"

	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestCatalogLicenseLimitsRoundTrip(t *testing.T) {
	catalog, err := Embedded()
	require.NoError(t, err)
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		t.Run(tier, func(t *testing.T) {
			plan, ok := catalog.GetPlan(tier)
			require.True(t, ok)
			limits, err := plan.ToProtoUsageLimits()
			require.NoError(t, err)
			require.Len(t, limits, len(plan.UsageLimits))
			state := &licv1.LicenseState{UsageLimits: limits}
			// The service stores encoding/json; Connect transmits protobuf or
			// protobuf JSON. Verify each boundary preserves type and int64 value.
			for name, codec := range map[string]struct {
				marshal   func() ([]byte, error)
				unmarshal func([]byte, *licv1.LicenseState) error
			}{
				"storage":      {func() ([]byte, error) { return json.Marshal(state) }, func(b []byte, s *licv1.LicenseState) error { return json.Unmarshal(b, s) }},
				"binary":       {func() ([]byte, error) { return proto.Marshal(state) }, func(b []byte, s *licv1.LicenseState) error { return proto.Unmarshal(b, s) }},
				"connect-json": {func() ([]byte, error) { return protojson.Marshal(state) }, func(b []byte, s *licv1.LicenseState) error { return protojson.Unmarshal(b, s) }},
			} {
				t.Run(name, func(t *testing.T) {
					payload, err := codec.marshal()
					require.NoError(t, err)
					var decoded licv1.LicenseState
					require.NoError(t, codec.unmarshal(payload, &decoded))
					for i, expected := range plan.UsageLimits {
						actual := decoded.UsageLimits[i]
						key := expected.Type
						if key == "STORAGE_BYTES" {
							key = "STORAGE"
						}
						require.Equal(t, "USAGE_LIMITS_TYPE_"+key, actual.Type.String())
						require.Equal(t, expected.Value, actual.Value)
					}
				})
			}
		})
	}
}

func TestLicenseLimitsRejectInvalidCatalogEntries(t *testing.T) {
	for _, limits := range [][]UsageLimit{
		{{Type: "UNKNOWN", Value: 10}},
		{{Type: "UNSPECIFIED", Value: 10}},
		{{Type: "STORAGE_BYTES", Value: -2}},
		{{Type: "STORAGE", Value: 1}, {Type: "STORAGE_BYTES", Value: 2}},
	} {
		got, err := (&Plan{UsageLimits: limits}).ToProtoUsageLimits()
		require.Error(t, err)
		require.Nil(t, got, "never issue a partial set of entitlements")
	}
	for _, value := range []int64{-1, 0, 53687091200} {
		got, err := (&Plan{UsageLimits: []UsageLimit{{Type: "STORAGE_BYTES", Value: value}}}).ToProtoUsageLimits()
		require.NoError(t, err)
		require.Equal(t, value, got[0].Value)
		require.Equal(t, int32(3), int32(got[0].Type), "preserve the legacy object-storage wire value")
	}
}

func TestLicenseLimitsOlderClientCompatibility(t *testing.T) {
	// Recreate the previous enum descriptor, without any appended values.
	fd := protodesc.ToFileDescriptorProto(licv1.File_everstack_license_v1_license_proto)
	for _, enum := range fd.EnumType {
		if enum.GetName() == "UsageLimitsType" {
			enum.Value = enum.Value[:16]
		}
	}
	older, err := protodesc.NewFile(fd, protoregistry.GlobalFiles)
	require.NoError(t, err)
	descriptor := older.Messages().ByName("UsageLimits")
	for _, kind := range []licv1.UsageLimitsType{licv1.UsageLimitsType_USAGE_LIMITS_TYPE_STORAGE, licv1.UsageLimitsType_USAGE_LIMITS_TYPE_HOSTING_STORAGE_BYTES} {
		original := &licv1.UsageLimits{Type: kind, Value: 53687091200}
		payload, err := proto.Marshal(original)
		require.NoError(t, err)
		legacy := dynamicpb.NewMessage(descriptor)
		require.NoError(t, proto.Unmarshal(payload, legacy))
		require.Equal(t, protoreflect.EnumNumber(kind), legacy.Get(descriptor.Fields().ByName("type")).Enum())
		payload, err = protojson.Marshal(original)
		require.NoError(t, err)
		// Connect's JSON codec discards unknown symbolic enum values. Old
		// clients still read object storage, but must upgrade to use hosting.
		require.NoError(t, (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, legacy))
		want := protoreflect.EnumNumber(kind)
		if kind != licv1.UsageLimitsType_USAGE_LIMITS_TYPE_STORAGE {
			want = 0
		}
		require.Equal(t, want, legacy.Get(descriptor.Fields().ByName("type")).Enum())
		require.Equal(t, int64(53687091200), legacy.Get(descriptor.Fields().ByName("value")).Int())
	}
}
