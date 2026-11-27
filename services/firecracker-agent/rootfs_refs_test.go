package main

import (
	"context"
	"strings"
	"testing"
	"time"

	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReportRootfsReferencesFeedsTheGC(t *testing.T) {
	s := NewAgentServer(nil, "node-a")
	s.SetRootfsReporters([]string{"everstack-gateway"})
	if _, fresh := s.RootfsReferences().Tags(); fresh {
		t.Fatal("fresh before any report")
	}

	_, err := s.ReportRootfsReferences(context.Background(), &fcpb.ReportRootfsReferencesRequest{
		Reporter: "everstack-gateway/pod-abc",
		Images:   []string{"everstack/openai-codex-firecracker-rootfs:openai-codex-0.154.0-r3"},
	})
	if err != nil {
		t.Fatalf("ReportRootfsReferences: %v", err)
	}
	tags, fresh := s.RootfsReferences().Tags()
	if !fresh {
		t.Fatal("report not recorded")
	}
	if _, ok := tags["openai-codex-0.154.0-r3"]; !ok {
		t.Fatalf("tags = %v", tags)
	}
}

func TestReportRootfsReferencesRejectsOversizedReports(t *testing.T) {
	s := NewAgentServer(nil, "node-a")
	s.SetRootfsReporters([]string{"gw"})
	images := make([]string, maxRootfsReferenceImages+1)
	for i := range images {
		images[i] = "x:" + strings.Repeat("a", 1+i%5)
	}
	_, err := s.ReportRootfsReferences(context.Background(), &fcpb.ReportRootfsReferencesRequest{
		Reporter: "gw", Images: images,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if _, fresh := s.RootfsReferences().Tags(); fresh {
		t.Fatal("rejected report was recorded")
	}
}

func TestReportRootfsReferencesRefusesUnlistedReporters(t *testing.T) {
	s := NewAgentServer(nil, "node-a")
	s.SetRootfsReporters([]string{"everstack-gateway"})
	_, err := s.ReportRootfsReferences(context.Background(), &fcpb.ReportRootfsReferencesRequest{
		Reporter: "someone-else/pod", Images: []string{"x:y"},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	if _, fresh := s.RootfsReferences().Tags(); fresh {
		t.Fatal("unlisted report was recorded")
	}
}

func TestReportRootfsReferencesUnknownKeepsEverything(t *testing.T) {
	s := NewAgentServer(nil, "node-a")
	s.SetRootfsReporters([]string{"everstack-gateway"})
	_, err := s.ReportRootfsReferences(context.Background(), &fcpb.ReportRootfsReferencesRequest{
		Reporter: "everstack-gateway/pod", Images: []string{"x:y"}, Unknown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, fresh := s.RootfsReferences().Tags(); fresh {
		t.Fatal("unknown report made the set fresh")
	}
}

func TestNewAgentServerAcceptsNoReporterByDefault(t *testing.T) {
	s := NewAgentServer(nil, "node-a")
	_, err := s.ReportRootfsReferences(context.Background(), &fcpb.ReportRootfsReferencesRequest{
		Reporter: "everstack-gateway/pod", Images: []string{"x:y"},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied with no allowlist", err)
	}
}

func TestRootfsGCConfigFromEnv(t *testing.T) {
	t.Run("defaults are dry run", func(t *testing.T) {
		cfg := rootfsGCConfigFromEnv()
		if cfg.Enforce {
			t.Fatal("enforce on by default")
		}
		if cfg.GracePeriod != 24*time.Hour || cfg.Interval != time.Hour {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		t.Setenv("FC_ROOTFS_GC_DIR", "/var/lib/everstack/rootfs-gc")
		t.Setenv("FC_ROOTFS_GC_ENFORCE", "true")
		t.Setenv("FC_ROOTFS_GC_GRACE", "48h")
		t.Setenv("FC_ROOTFS_GC_INTERVAL", "15m")
		t.Setenv("FC_ROOTFS_GC_KEEP", " node, python ,,")
		cfg := rootfsGCConfigFromEnv()
		if !cfg.Enforce || cfg.GracePeriod != 48*time.Hour || cfg.Interval != 15*time.Minute {
			t.Fatalf("cfg = %+v", cfg)
		}
		if strings.Join(cfg.Keep, ",") != "node,python" {
			t.Fatalf("keep = %q", cfg.Keep)
		}
		if cfg.Dir != "/var/lib/everstack/rootfs-gc" {
			t.Fatalf("dir = %q", cfg.Dir)
		}
	})
	t.Run("garbage durations fall back, never shrink the grace", func(t *testing.T) {
		t.Setenv("FC_ROOTFS_GC_GRACE", "soon")
		t.Setenv("FC_ROOTFS_GC_INTERVAL", "-1h")
		cfg := rootfsGCConfigFromEnv()
		if cfg.GracePeriod != 24*time.Hour || cfg.Interval != time.Hour {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
}

func TestRootfsGCReportersFromEnv(t *testing.T) {
	t.Setenv("FC_ROOTFS_GC_REPORTERS", " everstack-gateway, ,other ")
	if got := strings.Join(rootfsGCReportersFromEnv(), ","); got != "everstack-gateway,other" {
		t.Fatalf("reporters = %q", got)
	}
}
