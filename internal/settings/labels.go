package settings

// Labels is the list Home Assistant shows for a set of values, in the order given.
func Labels[T Labelled](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Label())
	}
	return out
}

// ByLabel resolves what Home Assistant sent back to the value it names. The mapping lives here so
// that no caller has to keep its own table: a select speaks labels, everything else speaks values,
// and this is the one place the two meet.
func ByLabel[T Labelled](values []T, label string) (T, bool) {
	for _, v := range values {
		if v.Label() == label {
			return v, true
		}
	}
	var zero T
	return zero, false
}
