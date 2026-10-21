package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The built frontend (web/dist, committed): `go build` needs no Node.
//
//go:embed all:web/dist
var webDist embed.FS

// static serves the frontend. Font files carry their version in their name
// (web/public/fonts/README.md), so they may be cached for good; everything
// else is revalidated, so a new build shows on the next load.
func static() http.Handler {
	dist, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic(err) // the embed pattern above guarantees the directory
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fonts/") && strings.HasSuffix(r.URL.Path, ".woff2") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
