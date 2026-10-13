package web

import (
	"io/fs"
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
	assertPageHeaders(t, recorder)
}

func TestMachinesPageServed(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	page := webRequest(t, mux, http.MethodGet, "/machines")
	if page.Code != http.StatusOK {
		t.Fatalf("GET /machines status = %d, want %d", page.Code, http.StatusOK)
	}
	if !strings.Contains(page.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q, want text/html", page.Header().Get("Content-Type"))
	}
	if !strings.Contains(page.Body.String(), "Machines") {
		t.Error("page does not contain its Machines heading")
	}
	assertPageHeaders(t, page)

	for _, path := range []string{"/static/machines.js", "/static/machines-view.js"} {
		t.Run(path, func(t *testing.T) {
			recorder := webRequest(t, mux, http.MethodGet, path)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
			}
			if !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
				t.Errorf("Content-Type = %q, want JavaScript", recorder.Header().Get("Content-Type"))
			}
			assertPageHeaders(t, recorder)
		})
	}

	testFile := webRequest(t, mux, http.MethodGet, "/static/machines-view.test.js")
	if testFile.Code != http.StatusNotFound {
		t.Errorf("GET /static/machines-view.test.js status = %d, want %d", testFile.Code, http.StatusNotFound)
	}
	assertPageHeaders(t, testFile)
}

func TestPageLoadsNothingExternal(t *testing.T) {
	checkEmbeddedFiles(t, func(path string, contents []byte) {
		if strings.Contains(string(contents), "http://") || strings.Contains(string(contents), "https://") {
			t.Errorf("%s contains an external URL", path)
		}
	})
}

func TestNoHTMLInsertion(t *testing.T) {
	checkEmbeddedFiles(t, func(path string, contents []byte) {
		for _, forbidden := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
			if strings.Contains(string(contents), forbidden) {
				t.Errorf("%s contains forbidden HTML insertion API %q", path, forbidden)
			}
		}
	})
}

func checkEmbeddedFiles(t *testing.T, check func(path string, contents []byte)) {
	t.Helper()
	err := fs.WalkDir(staticFiles, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := staticFiles.ReadFile(path)
		if err != nil {
			return err
		}
		check(path, contents)
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded files: %v", err)
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
