package pipedrive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// recorded is one request as the fake server saw it.
type recorded struct {
	method, path string
	query        map[string][]string
	body         map[string]any
}

// recordingServer answers every request with reply and records it.
func recordingServer(t *testing.T, reply string, got *[]recorded) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &rec.body); err != nil {
				t.Errorf("body is not JSON: %s", raw)
			}
		}
		*got = append(*got, rec)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ListInstallments_SendsOneCommaJoinedDealIDs(t *testing.T) {
	var got []recorded
	srv := recordingServer(t, `{"success":true,"data":[
		{"id":12,"amount":150000,"billing_date":"2026-09-01","deal_id":7,"description":"Phase 1"}
	],"additional_data":{"next_cursor":"c2"}}`, &got)

	rows, next, err := newTestClient(srv).ListInstallments(context.Background(), []int64{7, 9}, 500, "c1")
	if err != nil {
		t.Fatalf("ListInstallments: %v", err)
	}
	want := []recorded{{method: "GET", path: "/api/v2/deals/installments", query: map[string][]string{
		"deal_ids": {"7,9"}, "sort_by": {"billing_date"}, "sort_direction": {"asc"}, "limit": {"500"}, "cursor": {"c1"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v\nwant       %+v", got, want)
	}
	wantRows := []Installment{{ID: 12, DealID: 7, Description: "Phase 1", Amount: 150000, BillingDate: "2026-09-01"}}
	if !reflect.DeepEqual(rows, wantRows) || next != "c2" {
		t.Errorf("rows = %+v, next = %q\nwant   %+v, next = c2", rows, next, wantRows)
	}
}

func TestClient_InstallmentWrites_HitTheDealsPathWithOnlyTheSetFields(t *testing.T) {
	amount := 75000.0
	date := "2027-09-01"
	desc := "Phase 2"
	reply := `{"success":true,"data":{"id":13,"amount":75000,"billing_date":"2027-09-01","deal_id":7,"description":"Phase 2"}}`

	cases := []struct {
		name string
		call func(*Client) error
		want recorded
	}{
		{"create", func(c *Client) error {
			_, err := c.CreateInstallment(context.Background(), 7, InstallmentRequest{Description: &desc, Amount: &amount, BillingDate: &date})
			return err
		}, recorded{method: "POST", path: "/api/v2/deals/7/installments", query: map[string][]string{},
			body: map[string]any{"description": "Phase 2", "amount": 75000.0, "billing_date": "2027-09-01"}}},
		{"update sends the amount alone", func(c *Client) error {
			_, err := c.UpdateInstallment(context.Background(), 7, 13, InstallmentRequest{Amount: &amount})
			return err
		}, recorded{method: "PATCH", path: "/api/v2/deals/7/installments/13", query: map[string][]string{},
			body: map[string]any{"amount": 75000.0}}},
		{"delete", func(c *Client) error {
			return c.DeleteInstallment(context.Background(), 7, 13)
		}, recorded{method: "DELETE", path: "/api/v2/deals/7/installments/13", query: map[string][]string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []recorded
			srv := recordingServer(t, reply, &got)
			if err := tc.call(newTestClient(srv)); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if want := []recorded{tc.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("requests = %+v\nwant       %+v", got, want)
			}
		})
	}
}

func TestClient_ListDealProducts_ReadsBillingFrequency(t *testing.T) {
	var got []recorded
	srv := recordingServer(t, `{"success":true,"data":[
		{"id":1,"billing_frequency":"one-time","is_enabled":true},
		{"id":2,"billing_frequency":"monthly","is_enabled":false}
	]}`, &got)

	products, err := newTestClient(srv).ListDealProducts(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListDealProducts: %v", err)
	}
	if len(got) != 1 || got[0].path != "/api/v2/deals/7/products" || got[0].query["limit"][0] != "500" {
		t.Errorf("request = %+v, want GET /api/v2/deals/7/products?limit=500", got)
	}
	want := []DealProduct{{ID: 1, BillingFrequency: "one-time", IsEnabled: true}, {ID: 2, BillingFrequency: "monthly"}}
	if !reflect.DeepEqual(products, want) {
		t.Errorf("products = %+v\nwant       %+v", products, want)
	}
}

func TestClient_ListDeals_IDsFilter(t *testing.T) {
	var got []recorded
	srv := recordingServer(t, `{"success":true,"data":[]}`, &got)

	if _, _, err := newTestClient(srv).ListDeals(context.Background(), ListDealsOptions{IDs: []int64{7, 9}}); err != nil {
		t.Fatalf("ListDeals: %v", err)
	}
	if len(got) != 1 || got[0].query["ids"][0] != "7,9" {
		t.Errorf("query = %v, want ids=7,9", got)
	}
}
