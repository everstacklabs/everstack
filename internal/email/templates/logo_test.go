package templates

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/email/config"
)

// Every template's logo goes through logoBlock, and nothing inverts it any
// more: the asset is a dark wordmark, so the old default invert made it
// white on white in light mode and left it dark on dark in dark mode.
func TestEveryTemplateUsesTheAdaptiveLogo(t *testing.T) {
	if strings.Contains(darkModeStyleBlock, "invert") {
		t.Fatal("dark mode block still inverts the logo")
	}
	for typ, tmpl := range emailTemplates {
		if !strings.Contains(tmpl.HTMLTemplate, `alt="Everstack"`) {
			continue
		}
		if logoTag.MatchString(tmpl.HTMLTemplate) {
			t.Errorf("%s: bare logo tag left unwrapped", typ)
		}
		if !strings.Contains(tmpl.HTMLTemplate, "evs-logo-chip") {
			t.Errorf("%s: logo is not on the adaptive chip", typ)
		}
	}
}

func TestLogoBlockRendersBothVariants(t *testing.T) {
	tmpl, err := GetTemplate("card_removed")
	if err != nil {
		t.Fatal(err)
	}
	data := func(dark string) map[string]interface{} {
		return map[string]interface{}{
			"logo_url": config.DefaultLogoURL, "logo_dark_url": dark,
			"organization_name": "Acme", "method": "Visa ending in 4242", "noun_title": "Card",
			"billing_url": "https://app.everstack.ai/acme/settings/billing",
		}
	}
	_, html, err := tmpl.Render(data(config.DefaultLogoDarkURL))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="evs-logo-chip"`, `class="evs-logo-on-light" src="` + config.DefaultLogoURL + `"`,
		`class="evs-logo-on-dark" src="` + config.DefaultLogoDarkURL + `"`,
		"background-image: linear-gradient(#ffffff, #ffffff)", "prefers-color-scheme: dark",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A custom logo without a light variant keeps its chip in dark mode.
	_, html, err = tmpl.Render(data(""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `class="evs-logo-chip-fixed"`) || strings.Contains(html, "evs-logo-on-dark\" src") {
		t.Error("custom logo should keep a fixed chip and no dark variant")
	}
}

// The wordmark renders 22px tall inside a 142x30 email asset whose
// transparent frame is the chip's padding: the chip has no padding of its
// own, so the glyph sits within 3px of the headline, and the corner radius
// only reaches transparent pixels. Width and height are set as attributes
// (Outlook and Gmail honour only those) and as CSS.
func TestLogoIsSmallAndFlushLeft(t *testing.T) {
	block := logoBlock("28")
	for _, want := range []string{
		`width="142" height="30" style="display: block; width: 142px; height: 30px;`,
		`style="padding: 0; border-radius: 6px;`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("logo block missing %q", want)
		}
	}
	for _, stale := range []string{"40px", "137", "padding: 4px 8px 4px 0", "padding: 6px 10px"} {
		if strings.Contains(block, stale) {
			t.Errorf("logo block still has the old %q", stale)
		}
	}
	if !strings.Contains(darkModeStyleBlock, ".evs-logo-on-dark { display: block !important; width: 142px !important; height: 30px !important;") {
		t.Error("dark variant is not sized like the light one")
	}
}

// The img size above is only right for the default assets: 142x30 must be
// their exact ratio (no squash), and their outer frame must be transparent,
// or the chip's rounded corners clip the wordmark again.
func TestDefaultLogoAssetsMatchTheLogoBlock(t *testing.T) {
	for _, url := range []string{config.DefaultLogoURL, config.DefaultLogoDarkURL} {
		name := url[strings.LastIndex(url, "/")+1:]
		f, err := os.Open(filepath.Join("..", "..", "..", "apps", "landing", "public", name))
		if os.IsNotExist(err) {
			t.Skipf("%s: landing assets not in this checkout", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b := img.Bounds()
		if b.Dx()*30 != b.Dy()*142 {
			t.Errorf("%s is %dx%d, not the 142x30 ratio logoBlock draws it at", name, b.Dx(), b.Dy())
		}
		// Transparent frame: 2.5px (of the 142px display width) on every side.
		frame := b.Dx() * 5 / 284
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				inFrame := x < frame || x >= b.Max.X-frame || y < frame || y >= b.Max.Y-frame
				if _, _, _, a := img.At(x, y).RGBA(); inFrame && a != 0 {
					t.Fatalf("%s: opaque pixel at (%d,%d) inside the transparent frame", name, x, y)
				}
			}
		}
	}
}
