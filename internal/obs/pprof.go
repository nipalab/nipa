package obs

import (
	"net/http"
	"net/http/pprof"
)

// PprofHandler serves the net/http/pprof profiles on /debug/pprof/. It is only
// mounted when PPROF_ENABLED is set.
func PprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
