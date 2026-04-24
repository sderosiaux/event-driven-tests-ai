package eval

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Prefix returns the first 16 hex chars (64 bits) of sha256(data). Used
// as a fingerprint in judge prompts — short enough for context, long enough to
// collide only by design.
func sha256Prefix(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}
