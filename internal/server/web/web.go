// Package web contains the approval web pages.
package web

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed static/index.html static/app.js static/view.js static/machines.html static/machines.js static/machines-view.js
var staticFiles embed.FS

// Register registers web page routes.
func Register(mux *http.ServeMux) {
	mux.Handle("GET /pair/{approval_key}", pageHandler("static/index.html"))
	mux.Handle("GET /machines", pageHandler("static/machines.html"))
	mux.Handle("GET /static/", withPageHeaders(staticFileHandler()))
}

func pageHandler(name string) http.Handler {
	return withPageHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := staticFiles.ReadFile(name)
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}))
}

func staticFileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		contentType := ""
		switch name {
		case "index.html":
			contentType = "text/html; charset=utf-8"
		case "app.js", "view.js", "machines.js", "machines-view.js":
			contentType = "application/javascript; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}

		contents, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(contents)
	})
}

func withPageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
