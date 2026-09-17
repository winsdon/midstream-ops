package modeldetect

import "testing"

func TestHelloEntropyThreshold(t *testing.T) {
	for _, tc := range []struct {
		unique int
		want   bool
	}{
		{unique: 3, want: false},
		{unique: 4, want: true},
		{unique: 10, want: true},
	} {
		if got := helloEntropyPassed(tc.unique); got != tc.want {
			t.Fatalf("unique=%d: got %v, want %v", tc.unique, got, tc.want)
		}
	}
}
