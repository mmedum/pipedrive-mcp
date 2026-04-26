package pipedrive

import "fmt"

// WebURLKind identifies which Pipedrive web-UI route to build a URL
// for. Activities use a fragment-style URL (`activities/list#dialog/...`)
// because Pipedrive's UI opens activity detail in a modal over the
// activities list rather than at a dedicated path.
type WebURLKind string

// WebURL kinds covering the resource types the LLM-facing tools surface.
const (
	WebURLPipeline     WebURLKind = "pipeline"
	WebURLDeal         WebURLKind = "deal"
	WebURLPerson       WebURLKind = "person"
	WebURLOrganization WebURLKind = "organization"
	WebURLActivity     WebURLKind = "activities/list#dialog/activity"
)

// WebURL returns the Pipedrive web-UI URL for a resource by id. Tool
// outputs include this so a human reading the LLM's transcript can jump
// to the entity in their browser.
func WebURL(domain string, kind WebURLKind, id int64) string {
	return fmt.Sprintf("https://%s.pipedrive.com/%s/%d", domain, kind, id)
}
