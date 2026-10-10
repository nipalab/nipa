package obs

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPprofHandler(t *testing.T) {
	h := PprofHandler()

	for _, target := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/goroutine?debug=1"} {
		t.Run(target, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
			require.Equal(t, http.StatusOK, rr.Code)
		})
	}
}
