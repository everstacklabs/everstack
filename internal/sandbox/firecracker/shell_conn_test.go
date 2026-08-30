package firecracker

import "testing"

// TestStripControl guards the shell→log-buffer sanitizer against the
// regression where variable-length CSI/OSC sequences leaked their body
// into the Logs tab (ESC[?25l → "?25l", ESC[1;24r → "1;24r", etc.).
func TestStripControl(t *testing.T) {
	esc := "\x1b"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"csi_hide_cursor", esc + "[?25l", ""},
		{"csi_scroll_region", esc + "[1;24r", ""},
		{"csi_sgr_pair", esc + "[30m" + esc + "[42m", ""},
		{"csi_erase_line_keeps_text", esc + "[Khello", "hello"},
		{"csi_altscreen_and_clear", esc + "[?1049h" + esc + "[2J", ""},
		{"bracketed_paste", esc + "[?2004h" + esc + "[?2004l", ""},
		{"osc_title_bel", esc + "]0;my title\x07after", "after"},
		{"osc_title_st", esc + "]0;t" + esc + "\\after", "after"},
		{"charset_designation", esc + "(Bplain", "plain"},
		{"simple_two_byte", esc + "=" + esc + ">text", "text"},
		{"keeps_newlines_tabs", "a\tb\r\nc", "a\tb\r\nc"},
		{"drops_bare_c0", "x\x00\x07y", "xy"},
		{"prompt_survives", "sandbox:/workspace$ ", "sandbox:/workspace$ "},
		{
			"realistic_prompt_line",
			esc + "[?25l" + esc + "[1;1H" + "sandbox:/workspace$ ls" + esc + "[K\r\n",
			"sandbox:/workspace$ ls\r\n",
		},
		{"truncated_csi_no_final", esc + "[1;2", ""},
		{"lone_trailing_esc", "done" + esc, "done"},
	}
	for _, c := range cases {
		if got := stripControl([]byte(c.in)); got != c.want {
			t.Errorf("%s: stripControl(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
