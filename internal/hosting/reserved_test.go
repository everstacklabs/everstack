package hosting

import "testing"

func TestValidSlug(t *testing.T) {
	valid := []string{"my-site", "a1", "swift-heron-x4t9", "docs2", "x0"}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []string{
		"",            // empty
		"a",           // too short
		"-abc",        // leading hyphen
		"abc-",        // trailing hyphen
		"UPPER",       // uppercase
		"has_under",   // underscore
		"has.dot",     // dot
		"api",         // reserved
		"eu-gra-1",    // reserved region
		"ssh",         // reserved infra
		"everstack",   // reserved brand
		string(make([]byte, 70)), // too long
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestGenerateSlugShape(t *testing.T) {
	for i := 0; i < 50; i++ {
		s, err := GenerateSlug()
		if err != nil {
			t.Fatalf("GenerateSlug: %v", err)
		}
		if !ValidSlug(s) {
			t.Fatalf("generated slug %q is not valid", s)
		}
	}
}

func TestObjectKeys(t *testing.T) {
	if got := ObjectKey("demo", 3, "assets/app.js"); got != "sites/demo/v3/assets/app.js" {
		t.Errorf("ObjectKey = %q", got)
	}
	if got := ManifestKey("demo"); got != "sites/demo/manifest.json" {
		t.Errorf("ManifestKey = %q", got)
	}
}
