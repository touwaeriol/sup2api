package manifest

// ValidModelID reports whether s is a complete model id usable as a price
// key: one or more characters from letters, digits and . _ : / @ + -, of any
// length. Prices are matched by exact model id, so wildcards (* ?) and
// spaces are rejected.
func ValidModelID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '@', c == '+', c == '-':
		default:
			return false
		}
	}
	return true
}
