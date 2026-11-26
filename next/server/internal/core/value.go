package core

// ValueOr is *value, or def when value is nil (an absent optional field).
func ValueOr[T any](value *T, def T) T {
	if value != nil {
		return *value
	}
	return def
}

// SecretMask stands for a stored secret in a response (account credentials,
// plugin and top-up settings; sent back unchanged in a write, it keeps the
// stored value) and in a message that might echo one.
const SecretMask = "******"
