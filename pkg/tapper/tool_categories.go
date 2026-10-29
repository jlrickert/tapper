package tapper

// ToolCategory sorts every agent tool by the job it does in the KEG system,
// so an agent can tell everyday memory work from browsing the public
// directory and from administering the memory system itself. Each tool's
// description opens with its category label, orientation prints the intent
// of each category, and the Hub's tool groups use the same split.
type ToolCategory string

const (
	// ToolCategorySession tools report who the session is and what it may do.
	ToolCategorySession ToolCategory = "Session"
	// ToolCategoryMemory tools read and write notes in the covered KEGs.
	ToolCategoryMemory ToolCategory = "Memory"
	// ToolCategoryExplore tools search the hub's public directory.
	ToolCategoryExplore ToolCategory = "Explore"
	// ToolCategoryManage tools change the structure of the memory system.
	ToolCategoryManage ToolCategory = "Manage"
)

// ToolCategoryInfo is one category and the one-line intent an agent reads.
type ToolCategoryInfo struct {
	Category ToolCategory
	Intent   string
}

// ToolCategories lists the categories in the order agents see them.
var ToolCategories = []ToolCategoryInfo{
	{ToolCategorySession, "who you are and what this session may do; always available."},
	{ToolCategoryMemory, "read and write notes in the KEGs this session covers. Your own KEGs, flights, schemas, and tags are listed, never searched; node_search finds notes."},
	{ToolCategoryExplore, "search the hub's public directory of KEGs, flights, and people. Read-only, and a result never grants access."},
	{ToolCategoryManage, "change the memory system itself: create and delete KEGs, edit settings and schemas, repair indexes and locks, and manage flights and agents. Writing a note never needs these."},
}

var toolCategoryOf = map[string]ToolCategory{
	"orient": ToolCategorySession, "guide": ToolCategorySession,
	"session_info": ToolCategorySession, "session_refresh": ToolCategorySession,
	"namespace_list": ToolCategorySession, "invitation_list": ToolCategorySession,

	"keg_list": ToolCategoryMemory, "keg_info": ToolCategoryMemory, "keg_settings_read": ToolCategoryMemory,
	"node_read": ToolCategoryMemory, "node_list": ToolCategoryMemory, "node_search": ToolCategoryMemory,
	"node_grep": ToolCategoryMemory, "node_links": ToolCategoryMemory, "node_backlinks": ToolCategoryMemory,
	"node_stats": ToolCategoryMemory, "node_create": ToolCategoryMemory, "node_edit": ToolCategoryMemory,
	"node_move": ToolCategoryMemory, "node_delete": ToolCategoryMemory, "node_merge": ToolCategoryMemory,
	"tag_list": ToolCategoryMemory, "schema_list": ToolCategoryMemory, "schema_read": ToolCategoryMemory,
	"schema_validate": ToolCategoryMemory, "index_list": ToolCategoryMemory, "index_read": ToolCategoryMemory,
	"file_list": ToolCategoryMemory, "file_download": ToolCategoryMemory, "file_upload": ToolCategoryMemory,
	"file_delete": ToolCategoryMemory, "image_list": ToolCategoryMemory, "image_download": ToolCategoryMemory,
	"image_upload": ToolCategoryMemory, "image_delete": ToolCategoryMemory, "video_list": ToolCategoryMemory,
	"video_download": ToolCategoryMemory, "video_upload": ToolCategoryMemory, "video_delete": ToolCategoryMemory,
	"snapshot_list": ToolCategoryMemory,
	"snapshot_read": ToolCategoryMemory, "snapshot_create": ToolCategoryMemory, "snapshot_restore": ToolCategoryMemory,
	"trash_list": ToolCategoryMemory, "trash_restore": ToolCategoryMemory, "lock_acquire": ToolCategoryMemory,
	"lock_release": ToolCategoryMemory, "lock_status": ToolCategoryMemory,
	"flight_list": ToolCategoryMemory, "flight_read": ToolCategoryMemory, "url": ToolCategoryMemory,

	"explore_kegs": ToolCategoryExplore, "explore_flights": ToolCategoryExplore, "explore_people": ToolCategoryExplore,

	"keg_create": ToolCategoryManage, "keg_delete": ToolCategoryManage, "keg_settings_edit": ToolCategoryManage,
	"keg_replace": ToolCategoryManage, "schema_create": ToolCategoryManage, "schema_edit": ToolCategoryManage,
	"schema_delete": ToolCategoryManage, "index_rebuild": ToolCategoryManage, "keg_check": ToolCategoryManage,
	"lock_force_release": ToolCategoryManage, "flight_create": ToolCategoryManage, "flight_edit": ToolCategoryManage,
	"flight_delete": ToolCategoryManage, "agent_list": ToolCategoryManage, "agent_read": ToolCategoryManage,
	"agent_create": ToolCategoryManage, "agent_edit": ToolCategoryManage, "agent_delete": ToolCategoryManage,
}

// ToolCategoryOf returns the category of an agent tool.
func ToolCategoryOf(tool string) (ToolCategory, bool) {
	c, ok := toolCategoryOf[tool]
	return c, ok
}
