package pipedrive

import "testing"

// A record with no id has no page, so it gets no link rather than one to
// a record that does not exist.
func TestWebURLNeedsAnID(t *testing.T) {
	if got := WebURL("acme", WebURLDeal, 0); got != "" {
		t.Errorf("id 0: %q", got)
	}
	if got := WebURL("acme", WebURLDeal, 42); got != "https://acme.pipedrive.com/deal/42" {
		t.Errorf("id 42: %q", got)
	}
}
