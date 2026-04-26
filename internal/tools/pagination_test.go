package tools

import "testing"

func TestClampLimit(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{-1, defaultListLimit},
		{0, defaultListLimit},
		{1, 1},
		{25, 25},
		{100, 100},
		{101, maxListLimit},
		{99999, maxListLimit},
	}
	for _, tc := range tests {
		if got := clampLimit(tc.in); got != tc.want {
			t.Errorf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
