package pipedrive

import (
	"encoding/json"
	"testing"
)

func TestOneLine_IsSentAsAnObjectAndLeftOutWhenEmpty(t *testing.T) {
	for _, tc := range []struct {
		req  any
		want string
	}{
		{CreateActivityRequest{Subject: "Call", Location: "Rådhuspladsen 1"}, `{"subject":"Call","location":{"value":"Rådhuspladsen 1"}}`},
		{CreateActivityRequest{Subject: "Call"}, `{"subject":"Call"}`},
		{CreateOrganizationRequest{Name: "Acme"}, `{"name":"Acme"}`},
	} {
		got, err := json.Marshal(tc.req)
		if err != nil || string(got) != tc.want {
			t.Errorf("json.Marshal(%+v) = %s, %v; want %s", tc.req, got, err, tc.want)
		}
	}
}
