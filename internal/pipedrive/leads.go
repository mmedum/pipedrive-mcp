package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Lead is a Pipedrive lead (subset), read from v1: v2 has lead search
// and conversion only (D-105). Its id is a UUID. Custom fields arrive at
// the record's root under their hash keys, as v1 deals carry them, and
// are not decoded here.
// https://developers.pipedrive.com/docs/api/v1/Leads#getLead
type Lead struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	OwnerID           int64      `json:"owner_id"`
	CreatorID         int64      `json:"creator_id"`
	PersonID          *int64     `json:"person_id"`
	OrganizationID    *int64     `json:"organization_id"`
	Value             *LeadValue `json:"value"`
	ExpectedCloseDate string     `json:"expected_close_date"`
	LabelIDs          []string   `json:"label_ids"`
	IsArchived        bool       `json:"is_archived"`
	SourceName        string     `json:"source_name"`
	AddTime           string     `json:"add_time"`
	UpdateTime        string     `json:"update_time"`
}

// LeadValue is a lead's potential value.
type LeadValue struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// LeadLabel is one of the workspace's lead labels.
type LeadLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// ListLeadsOptions filters a /v1/leads list. Archived reads
// /v1/leads/archived, which /v1/leads leaves out and which takes no
// updated_since.
type ListLeadsOptions struct {
	OwnerID        int64
	PersonID       int64
	OrganizationID int64
	UpdatedSince   string // RFC3339
	Sort           string // "<field> ASC|DESC"
	Start          int
	Limit          int
	Archived       bool
}

// LeadRequest is the body for creating or editing a lead. Only the
// fields set are sent; v1's PATCH keeps what a body leaves out.
type LeadRequest struct {
	Title             *string    `json:"title,omitempty"`
	OwnerID           *int64     `json:"owner_id,omitempty"`
	PersonID          *int64     `json:"person_id,omitempty"`
	OrganizationID    *int64     `json:"organization_id,omitempty"`
	Value             *LeadValue `json:"value,omitempty"`
	ExpectedCloseDate *string    `json:"expected_close_date,omitempty"`
	LabelIDs          []string   `json:"label_ids,omitempty"`
	IsArchived        *bool      `json:"is_archived,omitempty"`
}

// GetLead fetches one lead by its UUID.
func (c *Client) GetLead(ctx context.Context, id string) (*Lead, error) {
	var resp itemEnvelope[Lead]
	if err := c.doV1(ctx, "/leads/"+url.PathEscape(id), &resp); err != nil {
		return nil, err
	}
	l := resp.Data
	return &l, nil
}

// ListLeads lists leads, or archived leads, a page at a time. v1 answers
// with the pagination block notes use, next_start included, though the
// reference lists start, limit and more_items_in_collection alone.
// Checked by the integration test TestV1CarveOutStillAnswers/list_leads.
func (c *Client) ListLeads(ctx context.Context, opts ListLeadsOptions) ([]Lead, *V1Pagination, error) {
	q := url.Values{}
	for k, v := range map[string]int64{"owner_id": opts.OwnerID, "person_id": opts.PersonID, "organization_id": opts.OrganizationID} {
		if v > 0 {
			q.Set(k, strconv.FormatInt(v, 10))
		}
	}
	if opts.UpdatedSince != "" && !opts.Archived {
		q.Set("updated_since", opts.UpdatedSince)
	}
	if opts.Sort != "" {
		q.Set("sort", opts.Sort)
	}
	if opts.Start > 0 {
		q.Set("start", strconv.Itoa(opts.Start))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	path := "/leads"
	if opts.Archived {
		path = "/leads/archived"
	}
	var resp listEnvelope[Lead]
	if err := c.doV1(ctx, buildPath(path, q), &resp); err != nil {
		return nil, nil, err
	}
	return resp.Data, resp.AdditionalData.V1Pagination, nil
}

// CreateLead adds a lead. Pipedrive requires a title and a person or an
// organization, and marks the lead's source as API.
// https://developers.pipedrive.com/docs/api/v1/Leads#addLead
func (c *Client) CreateLead(ctx context.Context, req LeadRequest) (*Lead, error) {
	if req.Title == nil || *req.Title == "" {
		return nil, fmt.Errorf("%w: title must not be empty", ErrValidation)
	}
	var resp itemEnvelope[Lead]
	if err := c.postV1(ctx, "/leads", req, &resp); err != nil {
		return nil, err
	}
	l := resp.Data
	return &l, nil
}

// UpdateLead edits a lead, archive and unarchive included, and returns it
// as stored.
func (c *Client) UpdateLead(ctx context.Context, id string, req LeadRequest) (*Lead, error) {
	var resp itemEnvelope[Lead]
	if err := c.patchV1(ctx, "/leads/"+url.PathEscape(id), req, &resp); err != nil {
		return nil, err
	}
	l := resp.Data
	return &l, nil
}

// DeleteLead deletes a lead for good: the reference says only "Deletes a
// specific lead", and the lead answers 404 straight after. Checked by
// the integration test TestWrite_LeadRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Leads#deleteLead
func (c *Client) DeleteLead(ctx context.Context, id string) error {
	return c.deleteV1(ctx, "/leads/"+url.PathEscape(id), nil)
}

// ListLeadLabels returns every lead label; the endpoint does not page.
func (c *Client) ListLeadLabels(ctx context.Context) ([]LeadLabel, error) {
	var resp listEnvelope[LeadLabel]
	if err := c.doV1(ctx, "/leadLabels", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
