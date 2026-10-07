package route

import "testing"

func TestAuthorizationToken(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer abc.def.ghi": "abc.def.ghi",
		"abc.def.ghi":        "abc.def.ghi",
		"":                   "",
		"bearer abc":         "bearer abc", // the scheme is case-sensitive here, as in the root service
	} {
		if got := authorizationToken(header); got != want {
			t.Errorf("authorizationToken(%q) = %q, want %q", header, got, want)
		}
	}
}
