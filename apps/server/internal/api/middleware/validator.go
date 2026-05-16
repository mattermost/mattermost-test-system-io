// Package middleware holds HTTP middleware — notably the kin-openapi
// request validator that enforces the OpenAPI contract.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api"
)

// OpenAPIValidator validates every request against the supplied OpenAPI 3.1 spec
// and rejects violations as 400 with RFC 7807 problem+json.
type OpenAPIValidator struct {
	router routers.Router
}

// NewOpenAPIValidator loads and compiles the spec at specPath (absolute or
// relative to CWD) and returns a validator.
func NewOpenAPIValidator(specPath string) (*OpenAPIValidator, error) {
	loader := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("load openapi: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("invalid openapi: %w", err)
	}
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("build openapi router: %w", err)
	}
	return &OpenAPIValidator{router: r}, nil
}

// Middleware returns a chi-compatible middleware that validates incoming requests.
// Requests not matched by any spec operation (e.g. /swagger-ui/*) pass through.
func (v *OpenAPIValidator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, pathParams, err := v.router.FindRoute(r)
		if err != nil {
			// Expected misses (path not in the spec, or path matches with a
			// different method) fall through to chi, which will 404/405. Any
			// other error means the openapi router itself is broken — fail
			// fast so it isn't silently masked.
			if errors.Is(err, routers.ErrPathNotFound) || errors.Is(err, routers.ErrMethodNotAllowed) {
				next.ServeHTTP(w, r)
				return
			}
			api.WriteError(w, r, err)
			return
		}
		input := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: noopAuth,
			},
		}
		if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
			api.WriteError(w, r, fmt.Errorf("%w: %s", api.ErrBadRequest, err.Error()))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noopAuth lets request validation succeed regardless of security scheme — auth
// is enforced by dedicated middleware in internal/api/auth.
func noopAuth(_ context.Context, _ *openapi3filter.AuthenticationInput) error {
	return nil
}

// ErrNoRoute is returned when a request path is not in the spec. Exposed so
// handlers can choose to treat it as 404 if desired.
var ErrNoRoute = errors.New("no matching openapi route")
