// Package toolnames owns names reserved by the Agent Runtime. Project-local
// functions cannot use these names because synthetic handlers are registered
// into the same tool namespace at session startup.
package toolnames

import "strings"

var reserved = map[string]struct{}{
	"ask_user":              {},
	"call_external_agent":   {},
	"check_job":             {},
	"check_messages":        {},
	"create_trigger":        {},
	"create_workflow":       {},
	"delegate_job":          {},
	"delete_trigger":        {},
	"download_artifact":     {},
	"fork":                  {},
	"list_artifacts":        {},
	"list_triggers":         {},
	"memory_query":          {},
	"memory_store":          {},
	"parallel_tasks":        {},
	"platform_create_agent": {},
	"platform_delete_agent": {},
	"platform_get_agent":    {},
	"platform_list_agents":  {},
	"platform_update_agent": {},
	"publish_site":          {},
	"read_channel_history":  {},
	"repo_glob":             {},
	"repo_read_file":        {},
	"schedule_cron":         {},
	"send_message":          {},
	"spawn_agent":           {},
	"upload_artifact":       {},
	"use_skill":             {},
	"web_fetch":             {},
	"web_search":            {},
}

// IsReserved reports whether name is owned by a static runtime tool or a
// dynamic runtime namespace. Browser, sandbox, and MCP handlers are generated
// from configuration, so their complete name set is not known at compile time.
func IsReserved(name string) bool {
	if _, ok := reserved[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "browser_") ||
		strings.HasPrefix(name, "sandbox_") ||
		strings.HasPrefix(name, "mcp__")
}
