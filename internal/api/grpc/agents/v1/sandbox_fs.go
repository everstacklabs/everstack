package v1

// Daytona-style file system API for sandboxes.
//
//	GET    /v1/sandbox/instances/{sandbox_id}/fs/list?path=/dir
//	POST   /v1/sandbox/instances/{sandbox_id}/fs/upload        multipart (file+path) or raw body (?path=)
//	GET    /v1/sandbox/instances/{sandbox_id}/fs/download?path=/file
//	POST   /v1/sandbox/instances/{sandbox_id}/fs/mkdir         {"path", "mode"?}
//	POST   /v1/sandbox/instances/{sandbox_id}/fs/delete        {"path", "recursive"?}
//	POST   /v1/sandbox/instances/{sandbox_id}/fs/move          {"source", "destination"}
//	POST   /v1/sandbox/instances/{sandbox_id}/fs/permissions   {"path", "mode", "owner"?, "group"?}
//
// Upload/download go through the backend's WriteFile/ReadFile (vsock
// file channel on firecracker); the metadata operations run as argv
// execs in the guest (no shell interpolation, so paths cannot inject).
// All handlers enforce tenant ownership inline and fail closed.

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// maxFSUploadBytes caps a single upload. Matches the snapshot path's
// 1GB guard: ReadFile/WriteFile buffer the full content in gateway
// memory, so unbounded uploads could OOM the pod.
const maxFSUploadBytes = 1 << 30

var fsModeRe = regexp.MustCompile(`^[0-7]{3,4}$`)

// cleanSandboxPath validates and normalizes an in-guest path. Absolute
// paths only; traversal is normalized away by path.Clean (the guest is
// the security boundary, but a clean path keeps argv execs and error
// messages sane).
func cleanSandboxPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path must be absolute")
	}
	// '/' itself is allowed: the file browser navigates to and lists
	// the root. Destructive operations (delete, move source) guard
	// against '/' explicitly via refuseRootPath — refusing it here too
	// broke listing the root with "refusing to operate on /".
	return path.Clean(p), nil
}

// refuseRootPath rejects the filesystem root for destructive
// operations. Listing and downloading '/' are fine; deleting or moving
// it is not.
func refuseRootPath(cleaned string) error {
	if cleaned == "/" {
		return fmt.Errorf("refusing to operate on /")
	}
	return nil
}

// fsRequestContext resolves the sandbox id and enforces ownership.
// Returns ("", false) after writing the error response on failure.
func (s *Server) fsRequestContext(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.sandboxMgr == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sandbox feature is not enabled")
		return "", false
	}
	sandboxID := mux.Vars(r)["sandbox_id"]
	if sandboxID == "" {
		writeJSONError(w, http.StatusBadRequest, "sandbox_id is required")
		return "", false
	}
	if !s.requireSandboxOwnershipHTTP(w, r, sandboxID) {
		return "", false
	}
	return sandboxID, true
}

// fsExec runs an argv command in the guest and maps a non-zero exit to
// a 422 with the stderr surfaced. Used by the metadata operations.
func (s *Server) fsExec(w http.ResponseWriter, r *http.Request, sandboxID string, argv ...string) bool {
	res, err := s.sandboxMgr.ExecBySandboxID(r.Context(), sandboxID, sandbox.ExecRequest{
		Command:   argv,
		SilentLog: true, // file-browser plumbing; keep it out of the Logs tab
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return false
	}
	if res.ExitCode != 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, strings.TrimSpace(res.Stderr))
		return false
	}
	return true
}

func writeFSOK(w http.ResponseWriter, fields map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fields)
}

// HandleSandboxFSList lists a directory.
// GET /v1/sandbox/instances/{sandbox_id}/fs/list?path=/dir
func (s *Server) HandleSandboxFSList(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/workspace"
	}
	cleaned, err := cleanSandboxPath(p)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := s.sandboxMgr.ListFilesBySandboxID(r.Context(), sandboxID, cleaned)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeFSOK(w, map[string]interface{}{"path": cleaned, "files": files})
}

// HandleSandboxFSUpload writes a file into the sandbox. Two body
// shapes are accepted:
//   - multipart/form-data with a "file" part and a "path" field
//   - any other content type: the raw body, destination in ?path=
//
// POST /v1/sandbox/instances/{sandbox_id}/fs/upload
func (s *Server) HandleSandboxFSUpload(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFSUploadBytes)

	var (
		destPath string
		content  []byte
		err      error
	)
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSONError(w, http.StatusBadRequest, "parse multipart: "+err.Error())
			return
		}
		destPath = r.FormValue("path")
		file, header, ferr := r.FormFile("file")
		if ferr != nil {
			writeJSONError(w, http.StatusBadRequest, "file part is required")
			return
		}
		defer file.Close()
		if destPath == "" && header != nil {
			writeJSONError(w, http.StatusBadRequest, "path field is required")
			return
		}
		content, err = io.ReadAll(file)
	} else {
		destPath = r.URL.Query().Get("path")
		content, err = io.ReadAll(r.Body)
	}
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read upload: "+err.Error())
		return
	}
	cleaned, perr := cleanSandboxPath(destPath)
	if perr != nil {
		writeJSONError(w, http.StatusBadRequest, perr.Error())
		return
	}

	// Ensure the parent directory exists; WriteFile does not mkdir.
	if parent := path.Dir(cleaned); parent != "/" {
		if !s.fsExec(w, r, sandboxID, "mkdir", "-p", parent) {
			return
		}
	}
	if err := s.sandboxMgr.WriteFileBySandboxID(r.Context(), sandboxID, cleaned, content); err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeFSOK(w, map[string]interface{}{"path": cleaned, "size_bytes": len(content)})
}

// HandleSandboxFSDownload streams a file out of the sandbox.
// GET /v1/sandbox/instances/{sandbox_id}/fs/download?path=/file
func (s *Server) HandleSandboxFSDownload(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	cleaned, err := cleanSandboxPath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.sandboxMgr.ReadFileBySandboxID(r.Context(), sandboxID, cleaned)
	if err != nil {
		if strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not found") {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(cleaned)}))
	_, _ = w.Write(data)
}

// HandleSandboxFSMkdir creates a directory (parents included).
// POST /v1/sandbox/instances/{sandbox_id}/fs/mkdir {"path","mode"?}
func (s *Server) HandleSandboxFSMkdir(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cleaned, err := cleanSandboxPath(body.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	argv := []string{"mkdir", "-p"}
	if body.Mode != "" {
		if !fsModeRe.MatchString(body.Mode) {
			writeJSONError(w, http.StatusBadRequest, "mode must be octal (e.g. 755)")
			return
		}
		argv = append(argv, "-m", body.Mode)
	}
	argv = append(argv, cleaned)
	if !s.fsExec(w, r, sandboxID, argv...) {
		return
	}
	writeFSOK(w, map[string]interface{}{"path": cleaned})
}

// HandleSandboxFSDelete removes a file or directory.
// POST /v1/sandbox/instances/{sandbox_id}/fs/delete {"path","recursive"?}
func (s *Server) HandleSandboxFSDelete(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cleaned, err := cleanSandboxPath(body.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := refuseRootPath(cleaned); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	argv := []string{"rm", "-f"}
	if body.Recursive {
		argv = []string{"rm", "-rf"}
	}
	argv = append(argv, "--", cleaned)
	if !s.fsExec(w, r, sandboxID, argv...) {
		return
	}
	writeFSOK(w, map[string]interface{}{"path": cleaned, "deleted": true})
}

// HandleSandboxFSMove renames or relocates a file/directory.
// POST /v1/sandbox/instances/{sandbox_id}/fs/move {"source","destination"}
func (s *Server) HandleSandboxFSMove(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	var body struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src, err := cleanSandboxPath(body.Source)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "source: "+err.Error())
		return
	}
	if err := refuseRootPath(src); err != nil {
		writeJSONError(w, http.StatusBadRequest, "source: "+err.Error())
		return
	}
	dst, err := cleanSandboxPath(body.Destination)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "destination: "+err.Error())
		return
	}
	if err := refuseRootPath(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "destination: "+err.Error())
		return
	}
	if parent := path.Dir(dst); parent != "/" {
		if !s.fsExec(w, r, sandboxID, "mkdir", "-p", parent) {
			return
		}
	}
	if !s.fsExec(w, r, sandboxID, "mv", "--", src, dst) {
		return
	}
	writeFSOK(w, map[string]interface{}{"source": src, "destination": dst})
}

// HandleSandboxFSPermissions sets mode/owner/group on a path.
// POST /v1/sandbox/instances/{sandbox_id}/fs/permissions
// {"path","mode"?,"owner"?,"group"?}
func (s *Server) HandleSandboxFSPermissions(w http.ResponseWriter, r *http.Request) {
	sandboxID, ok := s.fsRequestContext(w, r)
	if !ok {
		return
	}
	var body struct {
		Path  string `json:"path"`
		Mode  string `json:"mode"`
		Owner string `json:"owner"`
		Group string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cleaned, err := cleanSandboxPath(body.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Mode == "" && body.Owner == "" && body.Group == "" {
		writeJSONError(w, http.StatusBadRequest, "one of mode, owner, group is required")
		return
	}
	if body.Mode != "" {
		if !fsModeRe.MatchString(body.Mode) {
			writeJSONError(w, http.StatusBadRequest, "mode must be octal (e.g. 644)")
			return
		}
		if !s.fsExec(w, r, sandboxID, "chmod", body.Mode, "--", cleaned) {
			return
		}
	}
	if body.Owner != "" || body.Group != "" {
		ownerRe := regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`)
		spec := body.Owner
		if body.Group != "" {
			if body.Group != "" && !ownerRe.MatchString(body.Group) {
				writeJSONError(w, http.StatusBadRequest, "invalid group")
				return
			}
			spec += ":" + body.Group
		}
		if body.Owner != "" && !ownerRe.MatchString(body.Owner) {
			writeJSONError(w, http.StatusBadRequest, "invalid owner")
			return
		}
		if !s.fsExec(w, r, sandboxID, "chown", spec, "--", cleaned) {
			return
		}
	}
	writeFSOK(w, map[string]interface{}{"path": cleaned})
}
