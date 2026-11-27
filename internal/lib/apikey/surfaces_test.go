package apikey

import "testing"

func TestSurfaceNullVersusEmpty(t *testing.T) {
	for _, s := range []Surface{SurfaceInference, SurfaceMCP, SurfaceTelemetry, SurfaceA2A} {
		if !ValidSurface(s) || !Allowed(nil, s) || Allowed([]string{}, s) {
			t.Fatalf("NULL/empty semantics for %s", s)
		}
		if !Allowed([]string{string(s)}, s) {
			t.Fatal("listed surface denied")
		}
	}
	if ValidSurface("admin") || Allowed([]string{"admin"}, "admin") || Allowed([]string{"mcp"}, SurfaceA2A) {
		t.Fatal("unknown or unlisted surface allowed")
	}
}
