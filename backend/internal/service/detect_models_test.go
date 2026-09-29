package service

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDetectModel(t *testing.T) {
	for _, input := range []string{
		"claude-opus-5-5",
		"global.anthropic.claude-opus-5",
		"anthropic/claude-sonnet-5",
		"claude-opus-4-5@20251101",
	} {
		got, err := NormalizeDetectModel(" " + input + " ")
		if err != nil || got != input {
			t.Errorf("NormalizeDetectModel(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "bad model", "bad\nmodel", strings.Repeat("a", 129)} {
		if _, err := NormalizeDetectModel(input); !errors.Is(err, ErrInvalidDetectModel) {
			t.Errorf("NormalizeDetectModel(%q) should reject input, got %v", input, err)
		}
	}
}

func TestDetectModelListChanges(t *testing.T) {
	list, err := withModel([]string{"first"}, "claude-opus-5-5")
	if err != nil || len(list) != 2 || list[1] != "claude-opus-5-5" {
		t.Fatalf("add model: %v, %v", list, err)
	}
	list, err = withModel(list, "claude-opus-5-5")
	if err != nil || len(list) != 2 {
		t.Fatalf("duplicate model: %v, %v", list, err)
	}
	list = withoutModel(list, "claude-opus-5-5")
	if len(list) != 1 || list[0] != "first" {
		t.Fatalf("remove model: %v", list)
	}
}
