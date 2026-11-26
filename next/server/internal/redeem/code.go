package redeem

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// codeLen is the length of a normalized code: 16 random bytes in hex.
const codeLen = 32

// newCode returns a fresh code, normalized (32 upper-case hex digits).
func newCode() (string, error) {
	b := make([]byte, codeLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(b)), nil
}

// displayCode groups a normalized code by four: XXXX-XXXX-....
func displayCode(code string) string {
	var b strings.Builder
	for i := 0; i < len(code); i += 4 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(code[i:min(i+4, len(code))])
	}
	return b.String()
}

// normalize is what a user typed as the code: case, white space and
// hyphens do not matter.
func normalize(raw string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, raw)
}

// wellFormed reports whether a normalized code can exist at all, so that
// garbage never reaches the database.
func wellFormed(code string) bool {
	if len(code) != codeLen {
		return false
	}
	return strings.Trim(code, "0123456789ABCDEF") == ""
}

// hashCode is the stored code_hash: the lower-case hex SHA-256 of the
// normalized code, as the payment plugin hashed the codes 0126 copied.
func hashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// codeTail is the stored code_tail, the last four characters.
func codeTail(code string) string { return code[len(code)-4:] }
