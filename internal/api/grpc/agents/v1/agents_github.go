package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/everstacklabs/everstack/internal/github"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// SetGitHubApp sets the GitHub App client on the server.
// Called during startup if GitHub App env vars are configured.
func (s *Server) SetGitHubApp(app *github.App, store *github.Store) {
	s.githubApp = app
	s.githubStore = store
}

// ListGitHubInstallations lists active GitHub App installations for the tenant.
func (s *Server) ListGitHubInstallations(ctx context.Context, req *connect.Request[agentspb.ListGitHubInstallationsRequest]) (*connect.Response[agentspb.ListGitHubInstallationsResponse], error) {
	if s.githubStore == nil {
		logger.Warn("github: store not configured, returning empty installations list")
		return connect.NewResponse(&agentspb.ListGitHubInstallationsResponse{
			Installations: []*agentspb.GitHubInstallation{},
		}), nil
	}

	tenantID := req.Msg.TenantId
	if strings.TrimSpace(tenantID) == "" {
		tenantID = req.Header().Get("x-tenant-id")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id is required"))
	}
	installations, err := s.githubStore.ListActiveInstallations(ctx, tenantID)
	if err != nil {
		// Graceful degradation for local/dev environments where migrations are not
		// applied yet or DB-backed agents are not enabled.
		if errors.Is(err, github.ErrStoreNotConfigured) || errors.Is(err, github.ErrSchemaNotReady) {
			logger.WithFields("tenant_id", tenantID, "error", err.Error()).
				Warn("github: integration store unavailable, returning empty installations list")
			return connect.NewResponse(&agentspb.ListGitHubInstallationsResponse{
				Installations: []*agentspb.GitHubInstallation{},
			}), nil
		}
		logger.WithFields("tenant_id", tenantID, "error", err.Error()).
			Error("github: failed to list installations")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbInstalls := make([]*agentspb.GitHubInstallation, len(installations))
	for i, inst := range installations {
		pbInstalls[i] = installationToProto(&inst)
	}

	return connect.NewResponse(&agentspb.ListGitHubInstallationsResponse{
		Installations: pbInstalls,
	}), nil
}

// RemoveGitHubInstallation unlinks a GitHub App installation from the tenant.
func (s *Server) RemoveGitHubInstallation(ctx context.Context, req *connect.Request[agentspb.RemoveGitHubInstallationRequest]) (*connect.Response[agentspb.RemoveGitHubInstallationResponse], error) {
	if s.githubStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrGitHubNotConfigured)
	}

	tenantID := req.Msg.TenantId
	if strings.TrimSpace(tenantID) == "" {
		tenantID = req.Header().Get("x-tenant-id")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id is required"))
	}
	installationID := req.Msg.InstallationId

	if err := s.githubStore.RemoveInstallation(ctx, tenantID, installationID); err != nil {
		if err == github.ErrInstallationNotFound {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		logger.WithFields("tenant_id", tenantID, "installation_id", installationID, "error", err.Error()).
			Error("github: failed to remove installation")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Invalidate cached token for legacy/static app mode.
	if appClient, err := s.resolveGitHubAppForTenant(ctx, tenantID); err == nil && appClient != nil {
		appClient.InvalidateToken(installationID)
	}

	return connect.NewResponse(&agentspb.RemoveGitHubInstallationResponse{
		Success: true,
		Message: "Installation removed",
	}), nil
}

// LinkGitHubInstallation links a pending GitHub App installation to the current tenant.
// Called from the frontend after the GitHub App install redirect completes.
func (s *Server) LinkGitHubInstallation(ctx context.Context, req *connect.Request[agentspb.LinkGitHubInstallationRequest]) (*connect.Response[agentspb.LinkGitHubInstallationResponse], error) {
	if s.githubStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrGitHubNotConfigured)
	}

	tenantID := req.Msg.TenantId
	if strings.TrimSpace(tenantID) == "" {
		tenantID = req.Header().Get("x-tenant-id")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id is required"))
	}
	installationID := req.Msg.InstallationId

	// Fetch the installation (may be in 'pending' state from webhook)
	inst, err := s.githubStore.GetInstallation(ctx, installationID)
	if err != nil {
		logger.WithFields("installation_id", installationID, "error", err.Error()).
			Error("github: failed to get installation for linking")
		return nil, connect.NewError(connect.CodeNotFound, github.ErrInstallationNotFound)
	}

	// Link to tenant and activate
	inst.TenantID = tenantID
	inst.Status = "active"
	if err := s.githubStore.UpsertInstallation(ctx, inst); err != nil {
		logger.WithFields("installation_id", installationID, "error", err.Error()).
			Error("github: failed to link installation")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.LinkGitHubInstallationResponse{
		Installation: installationToProto(inst),
	}), nil
}

// ListGitHubRepositories lists repositories accessible to a GitHub installation.
func (s *Server) ListGitHubRepositories(ctx context.Context, req *connect.Request[agentspb.ListGitHubRepositoriesRequest]) (*connect.Response[agentspb.ListGitHubRepositoriesResponse], error) {
	if s.githubStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrGitHubNotConfigured)
	}

	tenantID := req.Msg.TenantId
	if strings.TrimSpace(tenantID) == "" {
		tenantID = req.Header().Get("x-tenant-id")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id is required"))
	}
	installationID := req.Msg.InstallationId

	// Verify installation belongs to tenant
	if _, err := s.githubStore.GetInstallationForTenant(ctx, tenantID, installationID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, github.ErrInstallationNotFound)
	}

	appClient, err := s.resolveGitHubAppForTenant(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnimplemented, err)
	}

	page := int(req.Msg.GetPage())
	perPage := int(req.Msg.GetPerPage())
	query := req.Msg.GetQuery()

	repos, total, err := appClient.ListRepositories(ctx, installationID, page, perPage, query)
	if err != nil {
		logger.WithFields("installation_id", installationID, "error", err.Error()).
			Error("github: failed to list repositories")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbRepos := make([]*agentspb.GitHubRepository, len(repos))
	for i, repo := range repos {
		pbRepos[i] = &agentspb.GitHubRepository{
			Id:            repo.ID,
			Name:          repo.Name,
			FullName:      repo.FullName,
			Description:   repo.Description,
			Private:       repo.Private,
			DefaultBranch: repo.DefaultBranch,
			Language:      repo.Language,
			SizeKb:        int32(repo.Size),
			HtmlUrl:       repo.HTMLURL,
		}
	}

	return connect.NewResponse(&agentspb.ListGitHubRepositoriesResponse{
		Repositories: pbRepos,
		Total:        int32(total),
	}), nil
}

// ListGitHubBranches lists branches for a specific repo accessible via an installation.
func (s *Server) ListGitHubBranches(ctx context.Context, req *connect.Request[agentspb.ListGitHubBranchesRequest]) (*connect.Response[agentspb.ListGitHubBranchesResponse], error) {
	if s.githubStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, ErrGitHubNotConfigured)
	}

	tenantID := req.Msg.TenantId
	if strings.TrimSpace(tenantID) == "" {
		tenantID = req.Header().Get("x-tenant-id")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id is required"))
	}
	installationID := req.Msg.InstallationId

	// Verify installation belongs to tenant
	if _, err := s.githubStore.GetInstallationForTenant(ctx, tenantID, installationID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, github.ErrInstallationNotFound)
	}

	appClient, err := s.resolveGitHubAppForTenant(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnimplemented, err)
	}

	page := int(req.Msg.GetPage())
	perPage := int(req.Msg.GetPerPage())

	branches, err := appClient.ListBranches(ctx, installationID, req.Msg.Owner, req.Msg.Repo, page, perPage)
	if err != nil {
		logger.WithFields("installation_id", installationID, "error", err.Error()).
			Error("github: failed to list branches")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbBranches := make([]*agentspb.GitHubBranch, len(branches))
	for i, b := range branches {
		pbBranches[i] = &agentspb.GitHubBranch{
			Name:      b.Name,
			Protected: b.Protected,
			CommitSha: b.Commit.SHA,
		}
	}

	return connect.NewResponse(&agentspb.ListGitHubBranchesResponse{
		Branches: pbBranches,
	}), nil
}

func (s *Server) resolveGitHubAppForTenant(ctx context.Context, tenantID string) (*github.App, error) {
	if s.githubStore != nil {
		appClient, _, err := s.githubStore.LoadAppClientForTenant(ctx, tenantID)
		if err == nil {
			return appClient, nil
		}
		if !errors.Is(err, github.ErrGitHubAppNotFound) &&
			!errors.Is(err, github.ErrSchemaNotReady) &&
			!errors.Is(err, github.ErrStoreNotConfigured) {
			logger.WithFields("tenant_id", tenantID, "error", err.Error()).
				Warn("github: failed to load tenant app credentials; falling back to legacy app")
		}
	}
	if s.githubApp != nil {
		return s.githubApp, nil
	}
	return nil, ErrGitHubNotConfigured
}

// handleListGitHubRepoTree handles GET /v1/integrations/github/tree
// Query params: installation_id, owner, repo, ref (branch/SHA), path (optional dir filter),
// search (optional substring search across all file paths).
// Returns the same format as the sandbox file listing for frontend compatibility.
func (s *Server) handleListGitHubRepoTree(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	tenantID := r.Header.Get("x-tenant-id")
	if tenantID == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}

	installationIDStr := r.URL.Query().Get("installation_id")
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	ref := r.URL.Query().Get("ref")
	filterPath := r.URL.Query().Get("path")
	searchQuery := r.URL.Query().Get("search")

	if installationIDStr == "" || owner == "" || repo == "" {
		http.Error(w, `{"error":"installation_id, owner, and repo are required"}`, http.StatusBadRequest)
		return
	}

	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid installation_id"}`, http.StatusBadRequest)
		return
	}

	if ref == "" {
		ref = "HEAD"
	}

	// Verify installation belongs to tenant
	if s.githubStore != nil {
		if _, err := s.githubStore.GetInstallationForTenant(r.Context(), tenantID, installationID); err != nil {
			http.Error(w, `{"error":"installation not found"}`, http.StatusNotFound)
			return
		}
	}

	appClient, err := s.resolveGitHubAppForTenant(r.Context(), tenantID)
	if err != nil {
		http.Error(w, `{"error":"github not configured"}`, http.StatusServiceUnavailable)
		return
	}

	tree, err := appClient.ListTree(r.Context(), installationID, owner, repo, ref)
	if err != nil {
		logger.WithFields("installation_id", installationID, "owner", owner, "repo", repo, "error", err.Error()).
			Error("github: failed to list tree")
		http.Error(w, `{"error":"failed to list repository tree"}`, http.StatusInternalServerError)
		return
	}

	// Convert to sandbox-compatible file format.
	type fileEntry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}

	var files []fileEntry

	if searchQuery != "" {
		// Search mode: case-insensitive substring match on full path, return flat list.
		needle := strings.ToLower(searchQuery)
		const maxResults = 50
		for _, entry := range tree {
			if len(files) >= maxResults {
				break
			}
			if !strings.Contains(strings.ToLower(entry.Path), needle) {
				continue
			}
			files = append(files, fileEntry{
				Name:  path.Base(entry.Path),
				Path:  "/" + entry.Path,
				Size:  entry.Size,
				IsDir: entry.Type == "tree",
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"search": searchQuery,
			"files":  files,
		})
		return
	}

	// Browse mode: optionally filtered by path prefix.
	// If filterPath is set, only return direct children of that directory.
	filterPath = strings.TrimPrefix(filterPath, "/")
	filterPath = strings.TrimSuffix(filterPath, "/")

	seen := make(map[string]bool)

	for _, entry := range tree {
		entryPath := entry.Path

		if filterPath != "" {
			// Only include direct children of the filter path
			if !strings.HasPrefix(entryPath, filterPath+"/") {
				continue
			}
			rel := strings.TrimPrefix(entryPath, filterPath+"/")
			// Direct child = no more slashes
			if strings.Contains(rel, "/") {
				// This is a nested entry. Extract the immediate child directory name.
				dirName := strings.SplitN(rel, "/", 2)[0]
				dirPath := filterPath + "/" + dirName
				if !seen[dirPath] {
					seen[dirPath] = true
					files = append(files, fileEntry{
						Name:  dirName,
						Path:  "/" + dirPath,
						IsDir: true,
					})
				}
				continue
			}
			// Direct child file or explicit tree node — deduplicate
			if seen[entryPath] {
				continue
			}
			seen[entryPath] = true
		} else {
			// Top-level: only direct children (no slashes in path)
			if strings.Contains(entryPath, "/") {
				dirName := strings.SplitN(entryPath, "/", 2)[0]
				if !seen[dirName] {
					seen[dirName] = true
					files = append(files, fileEntry{
						Name:  dirName,
						Path:  "/" + dirName,
						IsDir: true,
					})
				}
				continue
			}
			// Direct child at root — deduplicate
			if seen[entryPath] {
				continue
			}
			seen[entryPath] = true
		}

		isDir := entry.Type == "tree"
		files = append(files, fileEntry{
			Name:  path.Base(entryPath),
			Path:  "/" + entryPath,
			Size:  entry.Size,
			IsDir: isDir,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":  "/" + filterPath,
		"files": files,
	})
}

// installationToProto converts a store Installation to a proto GitHubInstallation.
func installationToProto(inst *github.Installation) *agentspb.GitHubInstallation {
	pb := &agentspb.GitHubInstallation{
		Id:                  inst.ID,
		TenantId:            inst.TenantID,
		InstallationId:      inst.InstallationID,
		AccountLogin:        inst.AccountLogin,
		AccountType:         inst.AccountType,
		AppId:               inst.AppID,
		RepositorySelection: inst.RepositorySelection,
		Status:              inst.Status,
		CreatedAt:           timestamppb.New(inst.CreatedAt),
		UpdatedAt:           timestamppb.New(inst.UpdatedAt),
	}
	if inst.InstalledBy != nil {
		pb.InstalledBy = *inst.InstalledBy
	}
	return pb
}
