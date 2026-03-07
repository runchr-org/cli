// Package trail provides types and helpers for managing trail metadata.
// Trails are branch-centric work tracking abstractions stored on the
// entire/trails/v1 orphan branch. They answer "why/what" (human intent)
// while checkpoints answer "how/when" (machine snapshots).
package trail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const idLength = 6 // 6 bytes = 12 hex chars

// ID is a 12-character hex identifier for trails.
type ID string

// EmptyID represents an unset or invalid trail ID.
const EmptyID ID = ""

// idRegex validates the format: exactly 12 lowercase hex characters.
var idRegex = regexp.MustCompile(`^[0-9a-f]{12}$`)

// GenerateID creates a new random 12-character hex trail ID.
func GenerateID() (ID, error) {
	bytes := make([]byte, idLength)
	if _, err := rand.Read(bytes); err != nil {
		return EmptyID, fmt.Errorf("failed to generate random trail ID: %w", err)
	}
	return ID(hex.EncodeToString(bytes)), nil
}

// ValidateID checks if a string is a valid trail ID format.
func ValidateID(s string) error {
	if !idRegex.MatchString(s) {
		return fmt.Errorf("invalid trail ID %q: must be 12 lowercase hex characters", s)
	}
	return nil
}

// String returns the trail ID as a string.
func (id ID) String() string {
	return string(id)
}

// IsEmpty returns true if the trail ID is empty or unset.
func (id ID) IsEmpty() bool {
	return id == EmptyID
}

// Path returns the sharded storage path for this trail ID.
// Uses first 2 characters as shard (256 buckets), remaining as folder name.
// Example: "a3b2c4d5e6f7" -> "a3/b2c4d5e6f7"
func (id ID) Path() string {
	if len(id) < 3 {
		return string(id)
	}
	return string(id[:2]) + "/" + string(id[2:])
}

// ShardParts returns the shard prefix and suffix separately.
// Example: "a3b2c4d5e6f7" -> ("a3", "b2c4d5e6f7")
func (id ID) ShardParts() (shard, suffix string) {
	if len(id) < 3 {
		return string(id), ""
	}
	return string(id[:2]), string(id[2:])
}

// Status represents the lifecycle status of a trail.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusActive     Status = "active"
	StatusValidating Status = "validating"
	StatusDone       Status = "done"
	StatusAbandoned  Status = "abandoned"
)

// ValidStatuses returns all valid trail statuses in lifecycle order.
func ValidStatuses() []Status {
	return []Status{
		StatusDraft,
		StatusActive,
		StatusValidating,
		StatusDone,
		StatusAbandoned,
	}
}

// IsValid returns true if the status is a recognized trail status.
func (s Status) IsValid() bool {
	for _, vs := range ValidStatuses() {
		if s == vs {
			return true
		}
	}
	return false
}

// Priority represents the priority level of a trail.
type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
	PriorityNone   Priority = "none"
)

// Type represents the type/category of a trail.
type Type string

const (
	TypeBug      Type = "bug"
	TypeFeature  Type = "feature"
	TypeChore    Type = "chore"
	TypeDocs     Type = "docs"
	TypeRefactor Type = "refactor"
)

// ReviewerStatus represents the review status for a reviewer.
type ReviewerStatus string

const (
	ReviewerPending          ReviewerStatus = "pending"
	ReviewerApproved         ReviewerStatus = "approved"
	ReviewerChangesRequested ReviewerStatus = "changes_requested"
)

// Reviewer represents a reviewer assigned to a trail.
type Reviewer struct {
	Login  string         `json:"login"`
	Status ReviewerStatus `json:"status"`
}

// BranchStatus represents the status of a branch within a trail.
type BranchStatus string

const (
	BranchOpen      BranchStatus = "open"
	BranchMerged    BranchStatus = "merged"
	BranchDiscarded BranchStatus = "discarded"
)

// IsValid returns true if the branch status is a recognized value.
func (s BranchStatus) IsValid() bool {
	switch s {
	case BranchOpen, BranchMerged, BranchDiscarded:
		return true
	}
	return false
}

// Intent describes the human intent behind a trail.
type Intent struct {
	Kind       string      `json:"kind"`
	Value      string      `json:"value"`
	Content    string      `json:"content,omitempty"`
	Amendments []Amendment `json:"amendments,omitempty"`
}

// Amendment records a change to the trail's intent.
type Amendment struct {
	Description string    `json:"description"`
	Reasoning   string    `json:"reasoning"`
	Timestamp   time.Time `json:"timestamp"`
}

// BranchEntry represents a branch associated with a trail.
type BranchEntry struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	BaseBranch string       `json:"base_branch"`
	BaseCommit string       `json:"base_commit,omitempty"`
	Status     BranchStatus `json:"status"`
	PR         *PRRef       `json:"pr,omitempty"`
	AddedAt    time.Time    `json:"added_at"`
}

// PRRef holds a reference to a pull request.
type PRRef struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

// VerificationEvent records a single verification event for a trail.
type VerificationEvent struct {
	Kind      string    `json:"kind"`
	BranchID  string    `json:"branch_id,omitempty"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Details   string    `json:"details,omitempty"`
}

// Verification holds the verification events for a trail.
type Verification struct {
	Events []VerificationEvent `json:"events"`
}

// Metadata represents the metadata for a trail, matching the web PR format.
type Metadata struct {
	TrailID   ID         `json:"trail_id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Status    Status     `json:"status"`
	Author    string     `json:"author"`
	Assignees []string   `json:"assignees"`
	Labels    []string   `json:"labels"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	Priority  Priority   `json:"priority,omitempty"`
	Type      Type       `json:"type,omitempty"`
	Reviewers []Reviewer `json:"reviewers,omitempty"`

	// Multi-branch fields
	Intent   *Intent       `json:"intent,omitempty"`
	Branches []BranchEntry `json:"branches,omitempty"`
	Summary  string        `json:"summary,omitempty"`

	// Legacy single-branch fields (kept for backward compat reads)
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
}

// FindBranch returns the BranchEntry matching the given branch name, or nil.
func (m *Metadata) FindBranch(branchName string) *BranchEntry {
	for i := range m.Branches {
		if m.Branches[i].Name == branchName {
			return &m.Branches[i]
		}
	}
	return nil
}

// ActiveBranchName returns the branch name for trail context.
// Prefers Branches[0].Name if available, falls back to legacy Branch field.
func (m *Metadata) ActiveBranchName() string {
	if len(m.Branches) > 0 {
		return m.Branches[0].Name
	}
	return m.Branch
}

// Discussion holds the discussion/comments for a trail.
type Discussion struct {
	Comments []Comment `json:"comments"`
}

// Comment represents a single comment on a trail.
type Comment struct {
	ID         string         `json:"id"`
	Author     string         `json:"author"`
	Body       string         `json:"body"`
	CreatedAt  time.Time      `json:"created_at"`
	Resolved   bool           `json:"resolved"`
	ResolvedBy *string        `json:"resolved_by"`
	ResolvedAt *time.Time     `json:"resolved_at"`
	Replies    []CommentReply `json:"replies,omitempty"`
}

// CommentReply represents a reply to a comment.
type CommentReply struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// commonBranchPrefixes are stripped from branch names when humanizing.
var commonBranchPrefixes = []string{
	"feature/",
	"fix/",
	"bugfix/",
	"chore/",
	"hotfix/",
	"release/",
}

// CheckpointRef links a checkpoint to a trail.
type CheckpointRef struct {
	CheckpointID string    `json:"checkpoint_id"`
	CommitSHA    string    `json:"commit_sha"`
	CreatedAt    time.Time `json:"created_at"`
	Summary      *string   `json:"summary"`
}

// Checkpoints holds the list of checkpoint references for a trail.
type Checkpoints struct {
	Checkpoints []CheckpointRef `json:"checkpoints"`
}

// HumanizeBranchName converts a branch name into a human-readable title.
// It strips common prefixes (feature/, fix/, etc.), replaces dashes/underscores
// with spaces, and capitalizes the first word.
func HumanizeBranchName(branch string) string {
	name := branch
	for _, prefix := range commonBranchPrefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}

	// Replace - and _ with spaces
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)

	// Trim spaces and capitalize first letter
	name = strings.TrimSpace(name)
	if name == "" {
		return branch
	}

	// Capitalize first character
	return strings.ToUpper(name[:1]) + name[1:]
}
