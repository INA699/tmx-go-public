// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/blackcode-ch/tmx-go/ui"
)

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>tmx</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:3rem auto;padding:0 1rem;color:#222;background:#fff}
@media (prefers-color-scheme: dark){body{color:#ddd;background:#111}}code{font-size:.95em}</style></head>
<body><h1>tmx daemon is running</h1>
<p>The web UI isn't built into this binary. Build it with <code>pnpm build</code> in <code>ui/</code>, then rebuild tmx.</p>
<p>The API is available under <code>/api/</code>.</p></body></html>
`

// uiHandler serves the embedded UI with an SPA fallback to index.html.
func uiHandler() http.Handler {
	if !ui.Built() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuiltPage))
		})
	}
	root := ui.FS()
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(root, name); err != nil || st.IsDir() {
			// Client-side route: serve the app shell.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
