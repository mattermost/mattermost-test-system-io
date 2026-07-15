package api

import (
	"net/http"
	"os"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// SwaggerHandlers returns handlers for /swagger-ui/* (the Swagger UI page)
// and /api/v1/openapi.yaml (the raw spec file).
func SwaggerHandlers(specPath string) (ui, rawSpec http.HandlerFunc) {
	ui = httpSwagger.Handler(
		httpSwagger.URL("/api/v1/openapi.yaml"),
		httpSwagger.DeepLinking(true),
	)
	rawSpec = func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(specPath) //nolint:gosec // operator-controlled path
		if err != nil {
			http.Error(w, "spec unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(b)
	}
	return
}
