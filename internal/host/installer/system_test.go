package installer

import "testing"

// build.prop is read back, edited and written whole, so the edit has to leave every other line alone
// and must not turn a missing key into a duplicate.
func TestSetProp(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"replaces in place": {
			"ro.a=1\nro.build.version.number=7\nro.b=2\n",
			"ro.a=1\nro.build.version.number=9\nro.b=2\n",
		},
		"appends when missing": {
			"ro.a=1\nro.b=2\n",
			"ro.a=1\nro.b=2\nro.build.version.number=9\n",
		},
		"appends to a file with no trailing newline": {
			"ro.a=1",
			"ro.a=1\nro.build.version.number=9\n",
		},
		"leaves a key it only prefixes": {
			"ro.build.version.number.extra=1\n",
			"ro.build.version.number.extra=1\nro.build.version.number=9\n",
		},
	} {
		if got := string(setProp([]byte(tc.in), "ro.build.version.number", "9")); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}
