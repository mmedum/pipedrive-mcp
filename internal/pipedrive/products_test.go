package pipedrive

import (
	"context"
	"reflect"
	"testing"
)

func TestClient_ProductWrites_HitTheirPathsWithOnlyTheSetFields(t *testing.T) {
	name := "Support hours"
	qty := 3.0
	reply := `{"success":true,"data":{"id":5}}`
	cases := []struct {
		name string
		call func(*Client) error
		want recorded
	}{
		{"create product", func(c *Client) error {
			_, err := c.CreateProduct(context.Background(), ProductRequest{Name: &name, Prices: []ProductPrice{{Price: 90, Currency: "EUR"}}})
			return err
		}, recorded{method: "POST", path: "/api/v2/products", query: map[string][]string{},
			body: map[string]any{"name": "Support hours", "prices": []any{map[string]any{"price": 90.0, "currency": "EUR"}}}}},
		{"rename product", func(c *Client) error {
			_, err := c.UpdateProduct(context.Background(), 5, ProductRequest{Name: &name})
			return err
		}, recorded{method: "PATCH", path: "/api/v2/products/5", query: map[string][]string{}, body: map[string]any{"name": "Support hours"}}},
		{"delete product", func(c *Client) error { return c.DeleteProduct(context.Background(), 5) },
			recorded{method: "DELETE", path: "/api/v2/products/5", query: map[string][]string{}}},
		{"change a deal line's quantity", func(c *Client) error {
			_, err := c.UpdateDealProduct(context.Background(), 7, 11, DealProductRequest{Quantity: &qty})
			return err
		}, recorded{method: "PATCH", path: "/api/v2/deals/7/products/11", query: map[string][]string{}, body: map[string]any{"quantity": 3.0}}},
		{"remove a deal line", func(c *Client) error { return c.DeleteDealProduct(context.Background(), 7, 11) },
			recorded{method: "DELETE", path: "/api/v2/deals/7/products/11", query: map[string][]string{}}},
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
