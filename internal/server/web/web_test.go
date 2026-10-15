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

func TestMachinesPageHasRevokeDialog(t *testing.T) {
	page, err := staticFiles.ReadFile("static/machines.html")
	if err != nil {
		t.Fatal(err)
	}
	pageText := string(page)
	dialogStart := strings.Index(pageText, `<dialog id="revoke-dialog"`)
	dialogEnd := strings.Index(pageText, `</dialog>`)
	appStart := strings.Index(pageText, `<div id="app">`)
	appEnd := strings.Index(pageText, `</div>`)
	if dialogStart < 0 || dialogEnd < dialogStart {
		t.Fatal("Machines page has no Revoke dialog")
	}
	if appStart < 0 || appEnd < appStart || (dialogStart >= appStart && dialogStart <= appEnd) {
		t.Fatal("Revoke dialog must be outside the part that refresh replaces")
	}
	dialog := pageText[dialogStart : dialogEnd+len(`</dialog>`)]
	if !strings.Contains(dialog, `id="revoke-question"`) || !strings.Contains(dialog, `>Revoke</button>`) || !strings.Contains(dialog, `>Cancel</button>`) {
		t.Fatal("Revoke dialog must contain its question, Revoke button, and Cancel button")
	}
}

func TestMachinesPageAnnouncesOnlyFailureLineAndPreservesRowSpacing(t *testing.T) {
	page, err := staticFiles.ReadFile("static/machines.html")
	if err != nil {
		t.Fatal(err)
	}
	pageText := string(page)
	const failureLine = `<p class="warning" aria-live="polite"></p>`
	if strings.Count(pageText, "aria-live=") != 1 || !strings.Contains(pageText, failureLine) {
		t.Fatal("Machines page must have one empty polite live region for the failure line")
	}
	mainStart := strings.Index(pageText, `<main>`)
	mainEnd := strings.Index(pageText, `</main>`)
	failureIndex := strings.Index(pageText, failureLine)
	headingIndex := strings.Index(pageText, `<h1>Machines</h1>`)
	appIndex := strings.Index(pageText, `<div id="app">`)
	if mainStart < 0 || mainEnd < 0 || headingIndex < mainStart || headingIndex > failureIndex || failureIndex > mainEnd {
		t.Fatal("failure line must be under the heading inside the centered main column")
	}
	if appIndex < 0 || failureIndex > appIndex {
		t.Fatal("failure line must be outside the part of the page that render replaces")
	}
	script, err := staticFiles.ReadFile("static/machines.js")
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(script)
	if !strings.Contains(scriptText, `failureLine.textContent !== page.failureMessage`) || !strings.Contains(scriptText, `failureLine.textContent = page.failureMessage;`) || strings.Contains(scriptText, `aria-live`) {
		t.Fatal("render must update the existing failure line only when its text changes")
	}
	if !strings.Contains(scriptText, `const app = document.querySelector('#app');`) || !strings.Contains(scriptText, `app.replaceChildren();`) {
		t.Fatal("render must replace only the inner app content, leaving the failure line in place")
	}
	if !strings.Contains(pageText, `white-space: pre-wrap;`) {
		t.Fatal("machine rows do not preserve their spacing")
	}
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
