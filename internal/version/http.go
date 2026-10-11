package version

import (
	"encoding/json"
	"net/http"
)

// Handler serves the running build's Info as JSON.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Resolve())
	})
}
