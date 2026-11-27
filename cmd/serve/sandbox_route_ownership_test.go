package serve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestDirectSandboxHandlersRequireOwnership(t *testing.T) {
	expected := map[string]bool{
		"sandboxLogsHandler":                false,
		"sandboxStatsHandler":               false,
		"sandboxEventsHandler":              false,
		"sandboxFilesHandler":               false,
		"sandboxFileSearchHandler":          false,
		"sandboxSSHInfoHandler":             false,
		"sandboxCommandHandler":             false,
		"sandboxExecuteCodeHandler":         false,
		"sandboxMetricsWatchHandler":        false,
		"sandboxPingHandler":                false,
		"sandboxCommandInterruptHandler":    false,
		"sandboxCommandStatusHandler":       false,
		"sandboxCommandLogsHandler":         false,
		"sandboxCreateCodeCtxHandler":       false,
		"sandboxListCodeCtxHandler":         false,
		"sandboxGetCodeCtxHandler":          false,
		"sandboxDeleteCodeCtxHandler":       false,
		"sandboxDeleteCodeCtxByLangHandler": false,
		"sandboxInterruptCodeHandler":       false,
		"sandboxFileInfoHandler":            false,
		"sandboxDeleteFilesHandler":         false,
		"sandboxFilePermissionsHandler":     false,
		"sandboxMoveFilesHandler":           false,
		"sandboxSearchFilesExecdHandler":    false,
		"sandboxBulkUploadHandler":          false,
		"sandboxReplaceFileHandler":         false,
		"sandboxUploadFileHandler":          false,
		"sandboxDownloadFileHandler":        false,
		"sandboxCreateDirsHandler":          false,
		"sandboxDeleteDirsHandler":          false,
		"sandboxMetricsHandler":             false,
		"sandboxRenewExpirationHandler":     false,
		"sandboxRestoreHandler":             false,
		"sandboxRecoverHandler":             false,
		"sandboxStartVerbHandler":           false,
		"sandboxStopVerbHandler":            false,
		"sandboxArchiveVerbHandler":         false,
		"sandboxDeleteVerbHandler":          false,
		"sandboxFSListHandler":              false,
		"sandboxFSUploadHandler":            false,
		"sandboxFSDownloadHandler":          false,
		"sandboxFSMkdirHandler":             false,
		"sandboxFSDeleteHandler":            false,
		"sandboxFSMoveHandler":              false,
		"sandboxFSPermissionsHandler":       false,
		"sandboxProcessSessionsHandler":     false,
		"sandboxProcessSessionHandler":      false,
		"sandboxProcessExecHandler":         false,
		"sandboxProcessCommandHandler":      false,
		"sandboxProcessLogsHandler":         false,
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "start_api.go", nil, 0)
	if err != nil {
		t.Fatalf("parse start_api.go: %v", err)
	}

	// Handlers may be wrapped with rawSandboxOwned, which adds device-token
	// authentication around RequireSandboxOwnership. It only counts as an
	// ownership wrapper if its own body calls RequireSandboxOwnership.
	ownershipWrappers := map[string]bool{}
	deviceAuthWrappers := map[string]bool{}
	tenantWrappers := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			identifier, ok := lhs.(*ast.Ident)
			if !ok || i >= len(assign.Rhs) {
				continue
			}
			if fn, ok := assign.Rhs[i].(*ast.FuncLit); ok {
				ownershipWrappers[identifier.Name] = bodyCallsNamed(fn.Body, "RequireSandboxOwnership")
				deviceAuthWrappers[identifier.Name] = bodyCallsNamed(fn.Body, "withRawDeviceTokenAuth")
				tenantWrappers[identifier.Name] = bodyCallsNamed(fn.Body, "RequireVerifiedTenant")
			}
		}
		return true
	})
	if !ownershipWrappers["rawSandboxOwned"] {
		t.Fatal("rawSandboxOwned no longer wraps RequireSandboxOwnership")
	}
	if !deviceAuthWrappers["rawSandboxOwned"] {
		t.Fatal("rawSandboxOwned no longer wraps withRawDeviceTokenAuth")
	}
	if !tenantWrappers["rawTenantVerified"] {
		t.Fatal("rawTenantVerified no longer wraps RequireVerifiedTenant")
	}
	if !deviceAuthWrappers["rawTenantVerified"] {
		t.Fatal("rawTenantVerified no longer wraps withRawDeviceTokenAuth")
	}

	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			identifier, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if _, tracked := expected[identifier.Name]; !tracked {
				continue
			}
			expected[identifier.Name] = true
			if i >= len(assign.Rhs) || !(callsRequireSandboxOwnership(assign.Rhs[i]) || callsWrapper(assign.Rhs[i], ownershipWrappers)) {
				t.Errorf("%s assignment at %s does not call RequireSandboxOwnership", identifier.Name, fset.Position(assign.Pos()))
			}
		}
		return true
	})

	for name, found := range expected {
		if !found {
			t.Errorf("%s assignment was not found", name)
		}
	}
	t.Logf("covered %d direct sandbox handler bindings", len(expected))
}

func callsRequireSandboxOwnership(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "RequireSandboxOwnership"
}

func callsWrapper(expr ast.Expr, wrappers map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && wrappers[ident.Name]
}

func bodyCallsNamed(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == name {
				found = true
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}
