package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func webRequest(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	mux.ServeHTTP(recorder, request)
	return recorder
}

func TestPageServed(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	recorder := webRequest(t, mux, http.MethodGet, "/pair/anything")

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /pair/anything status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q, want text/html", recorder.Header().Get("Content-Type"))
	}
	if !strings.Contains(recorder.Body.String(), "<html") {
		t.Error("response does not contain the approval page HTML")
	}
	assertPageHeaders(t, recorder)
}

func TestStaticFilesServed(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	for _, path := range []string{"/static/app.js", "/static/view.js"} {
		t.Run(path, func(t *testing.T) {
			recorder := webRequest(t, mux, http.MethodGet, path)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
			}
			if !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
				t.Errorf("Content-Type = %q, want a JavaScript content type", recorder.Header().Get("Content-Type"))
			}
			assertPageHeaders(t, recorder)
		})
	}

	recorder := webRequest(t, mux, http.MethodGet, "/static/view.test.js")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("GET /static/view.test.js status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestPageLoadsNothingExternal(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	for _, path := range []string{"/pair/key", "/static/app.js", "/static/view.js"} {
		t.Run(path, func(t *testing.T) {
			recorder := webRequest(t, mux, http.MethodGet, path)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
			}
			body, err := io.ReadAll(recorder.Result().Body)
			if err != nil {
				t.Fatalf("read %s response: %v", path, err)
			}
			if strings.Contains(string(body), "http://") || strings.Contains(string(body), "https://") {
				t.Errorf("%s contains an external URL", path)
			}
		})
	}
}

func TestNoHTMLInsertion(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	for _, path := range []string{"/pair/key", "/static/app.js", "/static/view.js"} {
		t.Run(path, func(t *testing.T) {
			recorder := webRequest(t, mux, http.MethodGet, path)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
			}
			body, err := io.ReadAll(recorder.Result().Body)
			if err != nil {
				t.Fatalf("read %s response: %v", path, err)
			}
			for _, forbidden := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
				if strings.Contains(string(body), forbidden) {
					t.Errorf("%s contains forbidden HTML insertion API %q", path, forbidden)
				}
			}
		})
	}
}

func assertPageHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}
