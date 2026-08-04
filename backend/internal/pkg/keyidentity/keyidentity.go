package keyidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Fingerprint returns a stable identity for API keys. new-api accepts and
// displays the sk- prefix independently from the key value it stores.
func Fingerprint(key string) string {
	canonical := strings.TrimSpace(key)
	canonical = strings.TrimPrefix(canonical, "sk-")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
