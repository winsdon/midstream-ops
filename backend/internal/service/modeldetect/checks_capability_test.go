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

func TestThinkingGradientRequiresMeaningfulGrowth(t *testing.T) {
	for _, tc := range []struct {
		chars []int
		want  bool
	}{
		{chars: []int{0, 0, 100}, want: false},
		{chars: []int{100, 110, 120}, want: false},
		{chars: []int{88, 505, 2699}, want: true},
		{chars: []int{100, 300, 600}, want: true},
	} {
		if got := meaningfulThinkingGradient(tc.chars); got != tc.want {
			t.Fatalf("chars=%v: got %v, want %v", tc.chars, got, tc.want)
		}
	}
}
