package trail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	apiurl "github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/entireio/cli/cmd/entire/cli/gitremote"
)

const (
	maxAPIResponseBytes = 1 << 20 // 1 MB
	apiTimeout          = 30 * time.Second
)

// ErrNotAuthenticated is returned when no auth token is available.
var ErrNotAuthenticated = errors.New("not authenticated: run 'entire login' first")

// APIStore provides trail CRUD operations via the Entire API.
type APIStore struct {
	httpClient *http.Client
	baseURL    string
	token      string
	owner      string
	repo       string
}

// NewAPIStore creates a new API-backed trail store.
// It resolves the org/repo from the git origin remote and retrieves the auth token.
func NewAPIStore(ctx context.Context) (*APIStore, error) {
	token, err := auth.LookupCurrentToken()
	if err != nil {
		return nil, fmt.Errorf("failed to look up auth token: %w", err)
	}
	if token == "" {
		return nil, ErrNotAuthenticated
	}

	owner, repo, err := gitremote.GetOriginOwnerRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve org/repo from git remote: %w", err)
	}

	return &APIStore{
		httpClient: &http.Client{},
		baseURL:    apiurl.BaseURL(),
		token:      token,
		owner:      owner,
		repo:       repo,
	}, nil
}

// NewAPIStoreWithConfig creates an APIStore with explicit configuration (for testing).
func NewAPIStoreWithConfig(httpClient *http.Client, baseURL, token, owner, repo string) *APIStore {
	return &APIStore{
		httpClient: httpClient,
		baseURL:    baseURL,
		token:      token,
		owner:      owner,
		repo:       repo,
	}
}

// apiTrailRequest is the JSON body for creating a trail via POST.
type apiTrailRequest struct {
	TrailID   string   `json:"trail_id"`
	Branch    string   `json:"branch"`
	Base      string   `json:"base"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Status    string   `json:"status,omitempty"`
	Author    string   `json:"author,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Priority  string   `json:"priority,omitempty"`
	Type      string   `json:"type,omitempty"`
}

// apiTrailUpdateRequest is the JSON body for updating a trail via PATCH.
type apiTrailUpdateRequest struct {
	Title     *string  `json:"title,omitempty"`
	Body      *string  `json:"body,omitempty"`
	Status    *string  `json:"status,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Priority  *string  `json:"priority,omitempty"`
	Type      *string  `json:"type,omitempty"`
}

// apiTrailResponse is the JSON response for a single trail from the API.
type apiTrailResponse struct {
	TrailID   string    `json:"trail_id"`
	Branch    string    `json:"branch"`
	Base      string    `json:"base"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	Author    string    `json:"author"`
	Assignees []string  `json:"assignees"`
	Labels    []string  `json:"labels"`
	Priority  string    `json:"priority"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	MergedAt  *jsonTime `json:"merged_at"`
	Reviewers []struct {
		Login  string `json:"login"`
		Status string `json:"status"`
	} `json:"reviewers"`
}

// jsonTime wraps time.Time for nullable JSON time parsing.
type jsonTime struct {
	time.Time
}

func (jt *jsonTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if err := json.Unmarshal(data, &jt.Time); err != nil {
		return fmt.Errorf("unmarshal time: %w", err)
	}
	return nil
}

func (r *apiTrailResponse) toMetadata() *Metadata {
	m := &Metadata{
		TrailID:   ID(r.TrailID),
		Branch:    r.Branch,
		Base:      r.Base,
		Title:     r.Title,
		Body:      r.Body,
		Status:    Status(r.Status),
		Author:    r.Author,
		Assignees: r.Assignees,
		Labels:    r.Labels,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		Priority:  Priority(r.Priority),
		Type:      Type(r.Type),
	}
	if r.MergedAt != nil && !r.MergedAt.IsZero() {
		t := r.MergedAt.Time
		m.MergedAt = &t
	}
	for _, rv := range r.Reviewers {
		m.Reviewers = append(m.Reviewers, Reviewer{
			Login:  rv.Login,
			Status: ReviewerStatus(rv.Status),
		})
	}
	if m.Assignees == nil {
		m.Assignees = []string{}
	}
	if m.Labels == nil {
		m.Labels = []string{}
	}
	return m
}

// trailsPath returns the API path prefix for trails in this repo.
func (s *APIStore) trailsPath() string {
	return fmt.Sprintf("/%s/%s/trails", s.owner, s.repo)
}

// List returns all trail metadata from the API.
func (s *APIStore) List() ([]*Metadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	resp, err := s.doRequest(ctx, http.MethodGet, s.trailsPath(), nil)
	if err != nil {
		return nil, fmt.Errorf("list trails: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readAPIError(resp, "list trails")
	}

	var items []apiTrailResponse
	if err := decodeJSON(resp.Body, &items); err != nil {
		return nil, fmt.Errorf("list trails: %w", err)
	}

	trails := make([]*Metadata, 0, len(items))
	for i := range items {
		trails = append(trails, items[i].toMetadata())
	}
	return trails, nil
}

// Read reads a trail by its ID from the API.
func (s *APIStore) Read(trailID ID) (*Metadata, *Discussion, *Checkpoints, error) {
	if err := ValidateID(string(trailID)); err != nil {
		return nil, nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	path := fmt.Sprintf("%s/%s", s.trailsPath(), trailID)
	resp, err := s.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read trail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, nil, ErrTrailNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, readAPIError(resp, "read trail")
	}

	var item apiTrailResponse
	if err := decodeJSON(resp.Body, &item); err != nil {
		return nil, nil, nil, fmt.Errorf("read trail: %w", err)
	}

	// The API does not return discussion/checkpoints inline.
	// Return empty defaults — callers that need them will use separate endpoints.
	return item.toMetadata(), &Discussion{Comments: []Comment{}}, &Checkpoints{Checkpoints: []CheckpointRef{}}, nil
}

// FindByBranch finds a trail for the given branch name.
// NOTE: This filters client-side from List(). A server-side ?branch= filter would be more efficient.
// See "Missing API Endpoints" in the implementation notes.
func (s *APIStore) FindByBranch(branchName string) (*Metadata, error) {
	trails, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, t := range trails {
		if t.Branch == branchName {
			return t, nil
		}
	}
	return nil, nil //nolint:nilnil // nil, nil means "not found" — callers check both
}

// Write creates a new trail via the API.
func (s *APIStore) Write(metadata *Metadata, _ *Discussion, _ *Checkpoints) error {
	if metadata.TrailID.IsEmpty() {
		return errors.New("trail ID is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	body := apiTrailRequest{
		TrailID:   string(metadata.TrailID),
		Branch:    metadata.Branch,
		Base:      metadata.Base,
		Title:     metadata.Title,
		Body:      metadata.Body,
		Status:    string(metadata.Status),
		Author:    metadata.Author,
		Assignees: metadata.Assignees,
		Labels:    metadata.Labels,
		Priority:  string(metadata.Priority),
		Type:      string(metadata.Type),
	}

	resp, err := s.doJSON(ctx, http.MethodPost, s.trailsPath(), body)
	if err != nil {
		return fmt.Errorf("create trail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return readAPIError(resp, "create trail")
	}

	return nil
}

// Update reads the current trail, applies the update function, and PATCHes the changes.
func (s *APIStore) Update(trailID ID, updateFn func(*Metadata)) error {
	metadata, _, _, err := s.Read(trailID)
	if err != nil {
		return fmt.Errorf("read trail for update: %w", err)
	}

	// Snapshot fields before update
	oldTitle := metadata.Title
	oldBody := metadata.Body
	oldStatus := metadata.Status
	oldAssignees := metadata.Assignees
	oldLabels := metadata.Labels
	oldPriority := metadata.Priority
	oldType := metadata.Type

	updateFn(metadata)
	metadata.UpdatedAt = time.Now()

	// Build PATCH body with only changed fields
	patch := apiTrailUpdateRequest{}
	hasChanges := false

	if metadata.Title != oldTitle {
		patch.Title = &metadata.Title
		hasChanges = true
	}
	if metadata.Body != oldBody {
		patch.Body = &metadata.Body
		hasChanges = true
	}
	if metadata.Status != oldStatus {
		s := string(metadata.Status)
		patch.Status = &s
		hasChanges = true
	}
	if !stringSliceEqual(metadata.Assignees, oldAssignees) {
		patch.Assignees = metadata.Assignees
		hasChanges = true
	}
	if !stringSliceEqual(metadata.Labels, oldLabels) {
		patch.Labels = metadata.Labels
		hasChanges = true
	}
	if metadata.Priority != oldPriority {
		p := string(metadata.Priority)
		patch.Priority = &p
		hasChanges = true
	}
	if metadata.Type != oldType {
		t := string(metadata.Type)
		patch.Type = &t
		hasChanges = true
	}

	if !hasChanges {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	path := fmt.Sprintf("%s/%s", s.trailsPath(), trailID)
	resp, err := s.doJSON(ctx, http.MethodPatch, path, patch)
	if err != nil {
		return fmt.Errorf("update trail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readAPIError(resp, "update trail")
	}

	return nil
}

// AddCheckpoint links a checkpoint to a trail.
// NOTE: The backend does not yet have a dedicated endpoint for this.
// This is a no-op that logs the intent. See "Missing API Endpoints".
func (s *APIStore) AddCheckpoint(_ ID, _ CheckpointRef) error {
	// TODO: Implement when POST /:org/:repo/trails/:trailId/checkpoints is available.
	// For now, checkpoint-to-trail linking is implicit via branch name in the DB.
	return nil
}

// Delete removes a trail via the API.
func (s *APIStore) Delete(trailID ID) error {
	if err := ValidateID(string(trailID)); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	path := fmt.Sprintf("%s/%s", s.trailsPath(), trailID)
	resp, err := s.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("delete trail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return readAPIError(resp, "delete trail")
	}

	return nil
}

// doRequest sends an HTTP request with auth headers.
func (s *APIStore) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	endpoint, err := apiurl.ResolveURLFromBase(s.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("resolve URL %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("User-Agent", "entire-cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}

	return resp, nil
}

// doJSON sends a JSON-encoded request body.
func (s *APIStore) doJSON(ctx context.Context, method, path string, body any) (*http.Response, error) {
	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}
	return s.doRequest(ctx, method, path, bytes.NewReader(jsonBytes))
}

// readAPIError reads an error response from the API.
func readAPIError(resp *http.Response, action string) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return fmt.Errorf("%s: status %d", action, resp.StatusCode)
	}

	var apiErr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
		return fmt.Errorf("%s: %s", action, apiErr.Error)
	}

	text := string(bytes.TrimSpace(body))
	if text != "" {
		return fmt.Errorf("%s: status %d: %s", action, resp.StatusCode, text)
	}
	return fmt.Errorf("%s: status %d", action, resp.StatusCode)
}

// decodeJSON reads and decodes a JSON response body.
func decodeJSON(r io.Reader, dest any) error {
	body, err := io.ReadAll(io.LimitReader(r, maxAPIResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// stringSliceEqual compares two string slices for equality.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
