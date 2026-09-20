package http

import (
	"context"
	"net/http"

	"github.com/nipalab/nipa/internal/domain"
)

type AppContext interface {
	Context() context.Context
	Claims() *domain.Claims
	ReadJson(v any) error
	WriteJson(statusCode int, v any) error
	WriteBytes(statusCode int, contentType string, data []byte)
	SetCookie(cookie *http.Cookie)
	Cookie(name string) (*http.Cookie, error)
	PathParameter(name string) string
	QueryParameter(name string) string
	HandleError(err error)
}
