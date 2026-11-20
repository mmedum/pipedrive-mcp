package pipedrive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"strconv"
)

// File is a file attached to a Pipedrive record (subset), read and written
// on v1, which v2 does not replace (D-105).
// https://developers.pipedrive.com/docs/api/v1/Files#getFile
type File struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FileName    string `json:"file_name"`
	Description string `json:"description"`
	FileSize    int64  `json:"file_size"`
	DealID      int64  `json:"deal_id"`
	PersonID    int64  `json:"person_id"`
	OrgID       int64  `json:"org_id"`
	ProductID   int64  `json:"product_id"`
	ActivityID  int64  `json:"activity_id"`
	LeadID      string `json:"lead_id"`
	ProjectID   int64  `json:"project_id"`
	UserID      int64  `json:"user_id"`
	ActiveFlag  bool   `json:"active_flag"`
	URL         string `json:"url"`
	AddTime     string `json:"add_time"`
	UpdateTime  string `json:"update_time"`
}

// FileAnchor is the record an upload attaches to; one is set.
type FileAnchor struct {
	DealID     int64
	PersonID   int64
	OrgID      int64
	ProductID  int64
	ActivityID int64
	LeadID     string
	ProjectID  int64
}

// GetFile reads one file's details. A deleted file still reads, with
// active_flag false, for the 30 days Pipedrive keeps it.
func (c *Client) GetFile(ctx context.Context, id int64) (*File, error) {
	var resp itemEnvelope[File]
	if err := c.doV1(ctx, "/files/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	f := resp.Data
	return &f, nil
}

// ListFiles lists the workspace's files a page at a time.
func (c *Client) ListFiles(ctx context.Context, sort string, start, limit int) ([]File, *V1Pagination, error) {
	q := url.Values{}
	if sort != "" {
		q.Set("sort", sort)
	}
	if start > 0 {
		q.Set("start", strconv.Itoa(start))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var resp listEnvelope[File]
	if err := c.doV1(ctx, buildPath("/files", q), &resp); err != nil {
		return nil, nil, err
	}
	return resp.Data, resp.AdditionalData.V1Pagination, nil
}

// UploadFile uploads content under name and attaches it to the anchor's
// record, as multipart/form-data, the one body the endpoint takes.
// https://developers.pipedrive.com/docs/api/v1/Files#addFile
func (c *Client) UploadFile(ctx context.Context, name string, content io.Reader, at FileAnchor) (*File, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		return nil, fmt.Errorf("pipedrive: encode upload: %w", err)
	}
	if _, err = io.Copy(part, content); err != nil {
		return nil, fmt.Errorf("pipedrive: read upload: %w", err)
	}
	for field, id := range map[string]int64{"deal_id": at.DealID, "person_id": at.PersonID, "org_id": at.OrgID,
		"product_id": at.ProductID, "activity_id": at.ActivityID, "project_id": at.ProjectID} {
		if id > 0 {
			if err = w.WriteField(field, strconv.FormatInt(id, 10)); err != nil {
				return nil, fmt.Errorf("pipedrive: encode upload: %w", err)
			}
		}
	}
	if at.LeadID != "" {
		if err = w.WriteField("lead_id", at.LeadID); err != nil {
			return nil, fmt.Errorf("pipedrive: encode upload: %w", err)
		}
	}
	if err = w.Close(); err != nil {
		return nil, fmt.Errorf("pipedrive: encode upload: %w", err)
	}
	var resp itemEnvelope[File]
	if err := c.postV1(ctx, "/files", rawBody{contentType: w.FormDataContentType(), data: buf.Bytes()}, &resp); err != nil {
		return nil, err
	}
	f := resp.Data
	return &f, nil
}

// UpdateFile changes a file's name and description, the two fields the
// endpoint takes, as a form. An empty value is left out.
func (c *Client) UpdateFile(ctx context.Context, id int64, name, description *string) (*File, error) {
	form := url.Values{}
	if name != nil {
		form.Set("name", *name)
	}
	if description != nil {
		form.Set("description", *description)
	}
	var resp itemEnvelope[File]
	body := rawBody{contentType: "application/x-www-form-urlencoded", data: []byte(form.Encode())}
	if err := c.putV1(ctx, "/files/"+strconv.FormatInt(id, 10), body, &resp); err != nil {
		return nil, err
	}
	f := resp.Data
	return &f, nil
}

// DeleteFile marks a file deleted, softly: "Marks a file as deleted.
// After 30 days, the file will be permanently deleted." Checked by the
// integration test TestWrite_FileRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Files#deleteFile
func (c *Client) DeleteFile(ctx context.Context, id int64) error {
	return c.deleteV1(ctx, "/files/"+strconv.FormatInt(id, 10))
}
