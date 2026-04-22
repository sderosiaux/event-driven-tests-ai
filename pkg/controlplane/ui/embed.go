// Package ui ships the control-plane web UI as an embedded filesystem so the
// `edt serve` binary stays single-file. Everything is plain HTML/CSS/JS; no
// build step.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// StaticFS returns the http.FileSystem rooted at /static.
func StaticFS() http.FileSystem {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Should never happen — the embed directive guarantees the path.
		panic("ui: static sub fs: " + err.Error())
	}
	return http.FS(sub)
}

// Index returns the root index.html bytes; handy when the server wants to
// inject runtime config in the future.
func Index() ([]byte, error) {
	return staticFS.ReadFile("static/index.html")
}
