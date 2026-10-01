package main

import (
	"strings"
	"testing"
)

// Each rule fires on a real-looking value and stays silent on an
// invented one. Real-looking values are built by concatenation, so the
// gate never reads one whole in this file.
func TestFindLeaks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // a substring of the one finding, or "" for none
	}{
		{"address at a real domain", "mail " + "kim@" + "nordic-freight.dk", "address at a real domain"},
		{"address at a reserved domain", "mail kim@example.com", ""},
		{"address with a marker word", "mail fixture@" + "nordic-freight.dk", ""},
		{"company subdomain", "see https://" + "nordicfreight" + ".pipedrive.com/deal/1", "Pipedrive subdomain"},
		{"invented subdomain", "see https://acme.pipedrive.com/deal/1", ""},
		{"vendor subdomain", "see https://developers.pipedrive.com/docs", ""},
		{"format verb is not a subdomain", "fmt.Sprintf(`https://%s.pipedrive.com/`, d)", ""},
		{"api token", "api_token=" + strings.Repeat("3f9a", 10), "API token"},
		{"phone number", `"value":"+45 ` + `31 72 08 64"`, "phone number"},
		{"phone number with dashes", "call +1-" + "646-381-2297", "phone number"},
		{"fictional phone number", `"value":"+1 555 0100"`, ""},
		{"fictional phone number with area code", "call +1-202-555-0142", ""},
		{"a sum is not a phone number", "attempt+1 < max", ""},
		{"a version is not a phone number", "go 1.26 +1.27 builds", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findLeaks(tc.text)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("findLeaks(%q) = %q, want no finding", tc.text, got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Errorf("findLeaks(%q) = %q, want one finding naming %q", tc.text, got, tc.want)
			}
		})
	}
}
