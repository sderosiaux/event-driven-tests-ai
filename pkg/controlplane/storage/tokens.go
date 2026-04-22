package storage

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashToken returns a hex-encoded sha256 digest of a bearer token. We store
// this digest rather than the plaintext so database snapshots cannot be
// replayed and a LIKE/= timing oracle cannot leak a prefix.
//
// The digest itself is compared via a map lookup in MemStore (no timing
// advantage) and an equality SQL predicate in PGStore (no advantage either,
// since both sides are a fixed-length hex string that an attacker cannot
// partially match without knowing the plaintext).
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
