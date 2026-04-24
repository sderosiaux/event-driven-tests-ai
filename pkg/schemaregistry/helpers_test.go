package schemaregistry_test

import "net/http"

// wrapCount increments *n on every HTTP request hitting the wrapped handler.
// Used by codec tests to assert the registry is hit only once per subject.
func wrapCount(h http.Handler, n *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		h.ServeHTTP(w, r)
	})
}
