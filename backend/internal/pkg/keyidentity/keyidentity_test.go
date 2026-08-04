package keyidentity

import "testing"

func TestFingerprintTreatsSkPrefixAsDisplayOnly(t *testing.T) {
	withPrefix := Fingerprint("  sk-example-key  ")
	withoutPrefix := Fingerprint("example-key")
	if withPrefix != withoutPrefix {
		t.Fatalf("display prefix changed key identity: %q != %q", withPrefix, withoutPrefix)
	}
}
