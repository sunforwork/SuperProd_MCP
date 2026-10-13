package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

type ModelWriteInput struct {
	RequestID    string   `json:"requestId"`
	BaseRevision int64    `json:"baseRevision"`
	ID           string   `json:"id,omitempty"`
	Title        string   `json:"title,omitempty"`
	ContextType  string   `json:"contextType,omitempty"`
	ContextID    string   `json:"contextId,omitempty"`
	Changes      p.Object `json:"changes,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	Board        p.Object `json:"board,omitempty"`
	Metric       p.Object `json:"metric,omitempty"`
	PanelID      string   `json:"panelId,omitempty"`
	SectionID    string   `json:"sectionId,omitempty"`
	TaskID       string   `json:"taskId,omitempty"`
	Tree         []any    `json:"tree,omitempty"`
	TreeType     string   `json:"treeType,omitempty"`
	IsEnabled    *bool    `json:"isEnabled,omitempty"`
	DurationMs   *int64   `json:"durationMs,omitempty"`
}

type SectionAddTaskInput struct {
	ModelWriteInput
	AfterTaskID     *string `json:"afterTaskId"`
	SourceSectionID *string `json:"sourceSectionId"`
}
type SectionRemoveTaskInput struct {
	ModelWriteInput
	WorkContextAfterTaskID *string `json:"workContextAfterTaskId"`
}

func addModelTools(server *mcp.Server, s *service.Service) {
	for _, entry := range []struct{ Name, Description string }{
		{"section_create", "Create a client section in PROJECT or TODAY TAG context with original title normalization and defaults."},
		{"section_update", "Edit section title or expanded state."}, {"section_delete", "Delete a section without deleting its tasks."}, {"section_reorder", "Reorder sections within a context, retaining stale/concurrent membership."},
		{"board_create", "Add a complete board configuration using client panel normalization."}, {"board_update", "Update board fields or its complete panels."}, {"board_delete", "Delete a board configuration."}, {"board_reorder", "Reorder boards, retaining boards absent from a stale order."}, {"board_set_panel_tasks", "Set a board panel's manual task order."}, {"board_duplicate", "Duplicate a board with new board/panel IDs and empty panel task lists."},
		{"metric_create", "Create a client daily metric."}, {"metric_update", "Update a daily metric; missing metrics receive client defaults."}, {"metric_upsert", "Upsert a daily metric, retaining unspecified existing fields."}, {"metric_delete", "Delete a daily metric."}, {"metric_log_focus", "Record a focus session; client service ignores durations shorter than 1000 ms."},
		{"menu_set_project_tree", "Replace the client project menu tree."}, {"menu_set_tag_tree", "Replace the client tag menu tree."}, {"menu_rename_folder", "Rename a folder within project (p) or tag (t) menu tree."}, {"menu_delete_folder", "Delete a menu folder and its nested menu entries; underlying projects/tags remain."},
		{"plugin_set_enabled", "Set a plugin's synchronized enabled state."}, {"plugin_delete_metadata", "Delete synchronized plugin metadata."},
	} {
		addCommandTool[ModelWriteInput](server, s, entry.Name, entry.Description)
	}
	addCommandTool[SectionAddTaskInput](server, s, "section_add_task", "Place a task in a section with explicit source section and nullable anchor, in one native operation.")
	addCommandTool[SectionRemoveTaskInput](server, s, "section_remove_task", "Remove a task from a section and position it in the context's no-section list using a nullable anchor.")
}
