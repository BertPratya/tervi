// Package web contains the approval web pages.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"time"
)

//go:embed static/index.html static/app.js static/view.js
var staticFiles embed.FS

// Register registers web page routes.
func Register(mux *http.ServeMux) {
	page := staticPageHandler()
	mux.Handle("GET /pair/{approval_key}", page)

	assets, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServer(http.FS(assets)))
	mux.Handle("GET /static/", withPageHeaders(files))
}

func staticPageHandler() http.Handler {
	return withPageHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
	}))
}

func withPageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
