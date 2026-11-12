package api

import (
	"github.com/emicklei/go-restful/v3"
)

// corsFilter answers CORS for the listed origins only. It runs on the
// container so preflights reach it even though no OPTIONS routes exist. Other
// origins get no CORS headers (the browser then blocks the response); state
// changing cross-origin requests are rejected by sameOriginFilter.
func corsFilter(origins []string) restful.FilterFunction {
	cors := restful.CrossOriginResourceSharing{
		AllowedDomains:    origins,
		AllowedDomainFunc: func(string) bool { return false },
		AllowedMethods:    []string{"POST", "GET", "PUT", "PATCH", "DELETE"},
		AllowedHeaders:    []string{"Content-Type", "Accept", "Authorization"},
		MaxAge:            600,
	}
	return cors.Filter
}
