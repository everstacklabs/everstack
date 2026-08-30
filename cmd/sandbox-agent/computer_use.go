package main

// Computer Use: screenshot, mouse/keyboard control, screen recording (POR-91, POR-92, POR-93).
//
// When SANDBOX_COMPUTER_USE=1 the sandbox-agent starts Xvfb + XFCE4 at boot.
// The HTTP endpoints (/computer/*) are then available for vision-capable agents.
//
// In-guest HTTP endpoints:
//   POST /computer/screenshot     { format, quality, region, include_cursor }
//   GET  /computer/displays       -- list available displays
//   GET  /computer/windows        -- list open windows
//   POST /computer/mouse/click    { x, y, button, double }
//   POST /computer/mouse/move     { x, y }
//   POST /computer/mouse/scroll   { x, y, direction, amount }
//   POST /computer/mouse/drag     { from, to }
//   POST /computer/keyboard/type  { text }
//   POST /computer/keyboard/key   { key }
//   POST /computer/recording/start { label, fps }
//   POST /computer/recording/stop  { recording_id }
//   GET  /computer/recordings
//   GET  /computer/recordings/{id}/download
//   DELETE /computer/recordings/{id}

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	xvfbDisplay  = ":99"
	xvfbRes      = "1920x1080x24"
	recordingsDir = "/tmp/everstack-recordings"
)

var (
	// computerUseEnabled is set true once startComputerUse has brought up Xvfb.
	// Atomic because startComputerUse now runs in a background goroutine (so a
	// slow Xvfb start can't block the :8080 liveness bind), which means the
	// /computer/* handlers can read this flag concurrently with the write.
	computerUseEnabled atomic.Bool
	recordingsMu       sync.Mutex
	activeRecordings   = map[string]*exec.Cmd{}
)

// startComputerUse launches Xvfb + XFCE4 if SANDBOX_COMPUTER_USE=1.
func startComputerUse() {
	if os.Getenv("SANDBOX_COMPUTER_USE") != "1" {
		return
	}

	_ = os.MkdirAll(recordingsDir, 0755)

	// Start Xvfb.
	xvfb := exec.Command("Xvfb", xvfbDisplay, "-screen", "0", xvfbRes)
	xvfb.Stderr = os.Stderr
	if err := xvfb.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: Xvfb start failed: %v\n", err)
		return
	}
	time.Sleep(500 * time.Millisecond)

	// Start XFCE4 desktop.
	xfce := exec.Command("startxfce4")
	xfce.Env = append(os.Environ(), "DISPLAY="+xvfbDisplay)
	xfce.Stderr = os.Stderr
	if err := xfce.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-agent: startxfce4 failed (continuing without desktop): %v\n", err)
	}

	computerUseEnabled.Store(true)
	fmt.Fprintf(os.Stderr, "sandbox-agent: computer use enabled (display=%s, res=%s)\n", xvfbDisplay, xvfbRes)
}

func requireComputerUse(w http.ResponseWriter) bool {
	if !computerUseEnabled.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "computer use not enabled (set SANDBOX_COMPUTER_USE=1 on CreateSandboxRequest)",
		})
		return false
	}
	return true
}

// handleComputerUse routes /computer/* requests.
func handleComputerUse(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/computer/")
	switch {
	case path == "screenshot":
		handleScreenshot(w, r)
	case path == "displays":
		handleDisplays(w, r)
	case path == "windows":
		handleWindows(w, r)
	case path == "mouse/click":
		handleMouseClick(w, r)
	case path == "mouse/move":
		handleMouseMove(w, r)
	case path == "mouse/scroll":
		handleMouseScroll(w, r)
	case path == "mouse/drag":
		handleMouseDrag(w, r)
	case path == "keyboard/type":
		handleKeyboardType(w, r)
	case path == "keyboard/key":
		handleKeyboardKey(w, r)
	case path == "recording/start":
		handleRecordingStart(w, r)
	case path == "recording/stop":
		handleRecordingStop(w, r)
	case path == "recordings":
		handleRecordingsList(w, r)
	case strings.HasPrefix(path, "recordings/") && strings.HasSuffix(path, "/download"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "recordings/"), "/download")
		handleRecordingDownload(w, r, id)
	case strings.HasPrefix(path, "recordings/"):
		id := strings.TrimPrefix(path, "recordings/")
		if r.Method == http.MethodDelete {
			handleRecordingDelete(w, r, id)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func handleScreenshot(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "jpeg"
	}
	quality := r.URL.Query().Get("quality")
	if quality == "" {
		quality = "85"
	}

	// Take screenshot using scrot or import (ImageMagick).
	outFile := "/tmp/everstack-screenshot." + format
	var cmd *exec.Cmd
	if _, err := exec.LookPath("scrot"); err == nil {
		cmd = exec.Command("scrot", "-D", xvfbDisplay, outFile)
	} else {
		cmd = exec.Command("import", "-window", "root", "-display", xvfbDisplay,
			"-quality", quality, outFile)
	}
	cmd.Env = append(os.Environ(), "DISPLAY="+xvfbDisplay)
	if err := cmd.Run(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "screenshot failed: " + err.Error()})
		return
	}

	data, err := os.ReadFile(outFile)
	_ = os.Remove(outFile)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "read screenshot: " + err.Error()})
		return
	}

	w.Header().Set("Content-Type", "image/"+format)
	w.Header().Set("X-Image-Base64", base64.StdEncoding.EncodeToString(data))
	_, _ = w.Write(data)
}

func handleDisplays(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]map[string]interface{}{
		{"id": 0, "display": xvfbDisplay, "width": 1920, "height": 1080, "is_primary": true},
	})
}

func handleWindows(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	out, err := exec.Command("wmctrl", "-lG").Output()
	if err != nil {
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	var windows []map[string]interface{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 8 {
			x, _ := strconv.Atoi(fields[2])
			y, _ := strconv.Atoi(fields[3])
			ww, _ := strconv.Atoi(fields[4])
			wh, _ := strconv.Atoi(fields[5])
			windows = append(windows, map[string]interface{}{
				"id": fields[0], "title": strings.Join(fields[7:], " "),
				"x": x, "y": y, "w": ww, "h": wh,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if windows == nil {
		windows = []map[string]interface{}{}
	}
	_ = json.NewEncoder(w).Encode(windows)
}

func xdotool(args ...string) error {
	cmd := exec.Command("xdotool", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+xvfbDisplay)
	return cmd.Run()
}

func cuJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func cuError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func handleMouseClick(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct {
		X      int    `json:"x"`
		Y      int    `json:"y"`
		Button string `json:"button"`
		Double bool   `json:"double"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Button == "" {
		body.Button = "left"
	}
	btn := "1"
	switch body.Button {
	case "right":
		btn = "3"
	case "middle":
		btn = "2"
	}
	args := []string{"mousemove", strconv.Itoa(body.X), strconv.Itoa(body.Y), "click"}
	if body.Double {
		args = append(args, "--repeat", "2")
	}
	args = append(args, btn)
	if err := xdotool(args...); err != nil {
		cuError(w, "mouse click failed: "+err.Error())
		return
	}
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleMouseMove(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct{ X, Y int }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := xdotool("mousemove", strconv.Itoa(body.X), strconv.Itoa(body.Y)); err != nil {
		cuError(w, err.Error())
		return
	}
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleMouseScroll(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct {
		X, Y      int
		Direction string
		Amount    int
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	btn := "4"
	if body.Direction == "down" {
		btn = "5"
	}
	if body.Amount <= 0 {
		body.Amount = 3
	}
	_ = xdotool("mousemove", strconv.Itoa(body.X), strconv.Itoa(body.Y),
		"click", "--repeat", strconv.Itoa(body.Amount), btn)
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleMouseDrag(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct {
		From struct{ X, Y int } `json:"from"`
		To   struct{ X, Y int } `json:"to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	_ = xdotool("mousemove", strconv.Itoa(body.From.X), strconv.Itoa(body.From.Y),
		"mousedown", "1", "mousemove", strconv.Itoa(body.To.X), strconv.Itoa(body.To.Y),
		"mouseup", "1")
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleKeyboardType(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct{ Text string `json:"text"` }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := xdotool("type", "--clearmodifiers", body.Text); err != nil {
		cuError(w, err.Error())
		return
	}
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleKeyboardKey(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct{ Key string `json:"key"` }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := xdotool("key", "--clearmodifiers", body.Key); err != nil {
		cuError(w, err.Error())
		return
	}
	cuJSON(w, map[string]string{"status": "ok"})
}

func handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct {
		Label string `json:"label"`
		FPS   int    `json:"fps"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.FPS <= 0 {
		body.FPS = 15
	}
	id := fmt.Sprintf("rec_%d", time.Now().UnixNano())
	outFile := filepath.Join(recordingsDir, id+".mp4")
	cmd := exec.Command("ffmpeg",
		"-f", "x11grab", "-r", strconv.Itoa(body.FPS),
		"-s", "1920x1080", "-i", xvfbDisplay,
		"-codec:v", "libx264", "-preset", "ultrafast", outFile,
	)
	cmd.Env = append(os.Environ(), "DISPLAY="+xvfbDisplay)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cuError(w, "recording start failed: "+err.Error())
		return
	}
	recordingsMu.Lock()
	activeRecordings[id] = cmd
	recordingsMu.Unlock()
	w.WriteHeader(http.StatusCreated)
	cuJSON(w, map[string]interface{}{"recording_id": id, "label": body.Label, "file": outFile})
}

func handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	var body struct{ RecordingID string `json:"recording_id"` }
	_ = json.NewDecoder(r.Body).Decode(&body)
	recordingsMu.Lock()
	cmd, ok := activeRecordings[body.RecordingID]
	delete(activeRecordings, body.RecordingID)
	recordingsMu.Unlock()
	if !ok {
		cuError(w, "recording not found")
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}
	outFile := filepath.Join(recordingsDir, body.RecordingID+".mp4")
	fi, _ := os.Stat(outFile)
	size := int64(0)
	if fi != nil {
		size = fi.Size()
	}
	cuJSON(w, map[string]interface{}{"recording_id": body.RecordingID, "size_bytes": size, "status": "stopped"})
}

func handleRecordingsList(w http.ResponseWriter, r *http.Request) {
	if !requireComputerUse(w) {
		return
	}
	entries, _ := filepath.Glob(filepath.Join(recordingsDir, "*.mp4"))
	var recs []map[string]interface{}
	for _, e := range entries {
		fi, _ := os.Stat(e)
		if fi == nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(e), ".mp4")
		recs = append(recs, map[string]interface{}{
			"id": id, "file": e, "size_bytes": fi.Size(), "created_at": fi.ModTime(),
		})
	}
	if recs == nil {
		recs = []map[string]interface{}{}
	}
	cuJSON(w, recs)
}

func handleRecordingDownload(w http.ResponseWriter, r *http.Request, id string) {
	if !requireComputerUse(w) {
		return
	}
	outFile := filepath.Join(recordingsDir, id+".mp4")
	if _, err := os.Stat(outFile); err != nil {
		cuError(w, "recording not found")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.mp4"`, id))
	http.ServeFile(w, r, outFile)
}

func handleRecordingDelete(w http.ResponseWriter, r *http.Request, id string) {
	if !requireComputerUse(w) {
		return
	}
	outFile := filepath.Join(recordingsDir, id+".mp4")
	_ = os.Remove(outFile)
	w.WriteHeader(http.StatusNoContent)
}
