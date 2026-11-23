package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
)

const specPath = "../../api/openapi.yaml"

// exemptRoutes are registered in Build but intentionally absent from the spec.
var exemptRoutes = map[string]bool{
	"GET /api/v1/openapi.yaml": true, // serves the spec itself
}

// TestRoutesMatchOpenAPI fails when a real API route is missing from
// openapi.yaml or a documented operation is not registered by Build.
func TestRoutesMatchOpenAPI(t *testing.T) {
	loader := &openapi3.Loader{Context: context.Background()}
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load %s: %v", specPath, err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("validate %s: %v", specPath, err)
	}

	spec := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			spec[method+" "+path] = true
		}
	}

	// Zero-value deps are enough: Build only wires handlers, nothing runs.
	routes := map[string]bool{}
	err = chi.Walk(Build(Deps{}), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if key := routeKey(method, route); key != "" && !exemptRoutes[key] {
			routes[key] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	for _, k := range missing(routes, spec) {
		t.Errorf("route registered in server.Build but missing from openapi.yaml: %s", k)
	}
	for _, k := range missing(spec, routes) {
		t.Errorf("operation in openapi.yaml but not registered in server.Build: %s", k)
	}
}

// routeKey returns "METHOD path" for an API route, or "" for routes outside
// the contract (swagger UI, HTML pages, SPA fallbacks).
func routeKey(method, route string) string {
	switch {
	case route == "/health", route == "/ready", strings.HasPrefix(route, "/api/v1/"):
	case route == "/files/*":
		route = "/files/{key}"
	default:
		return ""
	}
	return method + " " + route
}

// missing returns the sorted keys in a that are not in b.
func missing(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
