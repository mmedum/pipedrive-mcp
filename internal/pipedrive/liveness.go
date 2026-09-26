package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"slices"
)

// idsPerFetch is Pipedrive's documented cap on the `ids` parameter:
// "up to 100 entity ids to fetch".
const idsPerFetch = 100

// livenessPaths are the v2 collections that can answer "does this
// record still exist" for a search hit, by item type.
//
// Deliberately two entries, not six:
//
//   - deal is NOT here, and must not be. /deals stopped returning
//     archived deals on 2025-07-15 — they live at /deals/archived now
//     (see Deal.IsArchived) — so an archived deal is absent from
//     /deals while being perfectly alive. Reading that absence as
//     deletion would drop live deals out of search, which is worse
//     than the bug this file exists to fix. Pipedrive drops deleted
//     deals from its own search index anyway, and a live probe holds
//     that claim.
//   - product, file and lead are not modeled by this server at all,
//     so there is no collection here to ask.
var livenessPaths = map[ItemType]string{
	ItemTypeOrganization: "/organizations",
	ItemTypePerson:       "/persons",
}

// CanCheckLiveness reports whether LiveIDs can answer for this item
// type. Callers use it to decide what to check rather than hard-coding
// the list a second time.
func CanCheckLiveness(itemType ItemType) bool {
	_, ok := livenessPaths[itemType]
	return ok
}

// LiveIDs reports which of the given ids Pipedrive still lists, as a
// set. An id absent from the result has been deleted — a v2 collection
// omits a soft-deleted record rather than returning it with a marker.
//
// This exists because /itemSearch does not do the same. A deleted
// record can keep its place in the search index, and the item it
// returns carries no is_deleted, no active_flag and no status, so it
// cannot be told from a live one by looking at it. Confirmed against
// the live API: a deleted organization stays indexed indefinitely, and
// a deleted person stays for a while after the delete.
//
// It does not go through the resource's own List function, which would
// send include_fields — an aggregate per row, computed for up to a
// hundred records, to answer a question that needs one field. The
// reply is decoded to the id and nothing else for the same reason.
func (c *Client) LiveIDs(ctx context.Context, itemType ItemType, ids []int64) (map[int64]bool, error) {
	path, ok := livenessPaths[itemType]
	if !ok {
		return nil, fmt.Errorf("%w: no collection can answer whether a %s still exists", ErrValidation, itemType)
	}
	live := make(map[int64]bool, len(ids))
	// Chunked although today's only caller cannot exceed one chunk: a
	// search page is clamped to 100 hits. Sending 101 ids would let
	// Pipedrive answer about the first 100 and say nothing about the
	// rest, and silence is read here as deleted — which would drop
	// live records from a search, the failure this whole check exists
	// to prevent.
	for chunk := range slices.Chunk(ids, idsPerFetch) {
		q := url.Values{}
		q.Set("ids", joinInt64s(chunk))
		setLimitCursor(q, idsPerFetch, "")

		var resp listEnvelope[struct {
			ID int64 `json:"id"`
		}]
		if err := c.do(ctx, buildPath(path, q), &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Data {
			live[r.ID] = true
		}
	}
	return live, nil
}
