package route

import "strings"

// authorizationToken: `Bearer <token>` (root service, dashboard) or bare token (as before)
func authorizationToken(header string) string {
	return strings.TrimPrefix(header, "Bearer ")
}
