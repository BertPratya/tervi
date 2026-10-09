package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/api"
	"github.com/bertpratya/tervi/internal/server"
)

const (
	swaggerCSSIntegrity = `integrity="sha384-Ov4/wv3j2bmct8cDc5X4ngJZohVPzEmc6uDPH8WeljUxO5vtoykvMEfbu9Vh6RaW"`
	swaggerJSIntegrity  = `integrity="sha384-YDALVcy8kj8yltLBVi1vBiBAUqdxvus673gM8XKwiy6aDUJFXivF/KCufekjYbVf"`
)

func TestDocsOffByDefault(t *testing.T) {
	handler := server.New(server.Config{PublicURL: "http://localhost:8080"}, nil, nil)
	for _, path := range []string{"/docs", "/docs/openapi.yaml"} {
		t.Run(path, func(t *testing.T) {
			response := requestServer(t, handler, path)
			if response.Code != http.StatusNotFound {
				t.Fatalf("GET %s status = %d, want %d", path, response.Code, http.StatusNotFound)
			}
		})
	}
}

func TestDocsOn(t *testing.T) {
	handler := server.New(server.Config{PublicURL: "http://localhost:8080", APIDocs: true}, nil, nil)
	page := requestServer(t, handler, "/docs")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /docs status = %d, want %d; body = %q", page.Code, http.StatusOK, page.Body.String())
	}
	if got := page.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("GET /docs Content-Type = %q, want %q", got, "text/html; charset=utf-8")
	}
	for _, required := range []string{"swagger-ui-dist@5.33.0", swaggerCSSIntegrity, swaggerJSIntegrity, "/docs/openapi.yaml"} {
		if !strings.Contains(page.Body.String(), required) {
			t.Errorf("GET /docs page does not contain %q", required)
		}
	}

	contract := requestServer(t, handler, "/docs/openapi.yaml")
	if contract.Code != http.StatusOK {
		t.Fatalf("GET /docs/openapi.yaml status = %d, want %d", contract.Code, http.StatusOK)
	}
	if got := contract.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("GET /docs/openapi.yaml Content-Type = %q, want %q", got, "application/yaml")
	}
	if !bytes.Equal(contract.Body.Bytes(), api.OpenAPI) {
		t.Errorf("GET /docs/openapi.yaml body differs from api.OpenAPI")
	}
}

func TestAPIDocsSetting(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		unset   bool
		want    bool
		wantErr string
	}{
		{name: "unset", unset: true, want: false},
		{name: "empty", value: "", want: false},
		{name: "off", value: "off", want: false},
		{name: "on", value: "on", want: true},
		{name: "invalid", value: "yes", wantErr: `TERVI_API_DOCS must be "on" or "off"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			setConfigEnv(t, "127.0.0.1:8080", "http://localhost:8080")
			if test.unset {
				unsetEnvironmentValue(t, "TERVI_API_DOCS")
			} else {
				t.Setenv("TERVI_API_DOCS", test.value)
			}

			cfg, err := server.LoadConfig()
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("LoadConfig() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.APIDocs != test.want {
				t.Errorf("APIDocs = %t, want %t", cfg.APIDocs, test.want)
			}
		})
	}
}

func requestServer(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = "localhost"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func unsetEnvironmentValue(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
