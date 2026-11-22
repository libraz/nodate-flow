package asana

import "time"

// Ref is a compact Asana resource reference.
type Ref struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

// Workspace is an Asana workspace or organization.
type Workspace struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

// User is an Asana user. Email is empty when the token cannot see it.
type User struct {
	GID   string `json:"gid"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Tag is a workspace tag.
type Tag struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

// Project is an Asana project.
type Project struct {
	GID      string `json:"gid"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// Section is a project section.
type Section struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

// Membership places a task in a project and, optionally, a section.
type Membership struct {
	Project Ref  `json:"project"`
	Section *Ref `json:"section"`
}

// Task is an Asana task or subtask. Date-only fields stay strings (YYYY-MM-DD).
type Task struct {
	GID             string       `json:"gid"`
	Name            string       `json:"name"`
	ResourceSubtype string       `json:"resource_subtype"`
	HTMLNotes       string       `json:"html_notes"`
	Assignee        *User        `json:"assignee"`
	Followers       []User       `json:"followers"`
	CreatedBy       *User        `json:"created_by"`
	CreatedAt       time.Time    `json:"created_at"`
	DueOn           string       `json:"due_on"`
	DueAt           *time.Time   `json:"due_at"`
	StartOn         string       `json:"start_on"`
	StartAt         *time.Time   `json:"start_at"`
	Completed       bool         `json:"completed"`
	Parent          *Ref         `json:"parent"`
	Memberships     []Membership `json:"memberships"`
	Tags            []Tag        `json:"tags"`
	Dependencies    []Ref        `json:"dependencies"`
	NumSubtasks     int          `json:"num_subtasks"`
}

// StoryCommentAdded is the resource_subtype of a user comment.
const StoryCommentAdded = "comment_added"

// Story is a task story (comment or system event).
type Story struct {
	GID             string    `json:"gid"`
	ResourceSubtype string    `json:"resource_subtype"`
	HTMLText        string    `json:"html_text"`
	CreatedBy       *User     `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	NumLikes        int       `json:"num_likes"`
}

// Attachment is a file attached to a task. DownloadURL is empty for external hosts.
type Attachment struct {
	GID         string `json:"gid"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	DownloadURL string `json:"download_url"`
	ViewURL     string `json:"view_url"`
	Size        int64  `json:"size"`
}

// Field lists requested through opt_fields; they mirror the structs above.
const (
	userFields = "gid,name,email"
	taskFields = "gid,name,resource_subtype,html_notes," +
		"assignee.gid,assignee.name,assignee.email," +
		"followers.gid,followers.name,followers.email," +
		"created_by.gid,created_by.name,created_by.email," +
		"created_at,due_on,due_at,start_on,start_at,completed,parent.gid,parent.name," +
		"memberships.project.gid,memberships.project.name,memberships.section.gid,memberships.section.name," +
		"tags.gid,tags.name,dependencies.gid,dependencies.name,num_subtasks"
	storyFields      = "gid,resource_subtype,html_text,created_by.gid,created_by.name,created_by.email,created_at,num_likes"
	attachmentFields = "gid,name,host,download_url,view_url,size"
	projectFields    = "gid,name,archived"
	nameFields       = "gid,name"
)
