package main

import (
	"embed"
	"io/fs"
)

//go:embed frontend/index.html frontend/style.css frontend/styles/*.css frontend/app.js frontend/app/*.js frontend/vendor/* frontend/favicon.ico
var webFS embed.FS

//go:embed frontend/admin.html frontend/admin.js frontend/admin/*.js
var adminFS embed.FS

func webRoot() fs.FS {
	root, err := fs.Sub(webFS, "frontend")
	if err != nil {
		panic(err)
	}
	return root
}

func adminRoot() fs.FS {
	root, err := fs.Sub(adminFS, "frontend")
	if err != nil {
		panic(err)
	}
	return root
}
