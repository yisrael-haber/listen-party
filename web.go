package main

import (
	"embed"
	"io/fs"
)

//go:embed index.html style.css app.js
var webFS embed.FS

//go:embed admin.html admin.js
var adminFS embed.FS

func webRoot() fs.FS {
	return webFS
}

func adminRoot() fs.FS {
	return adminFS
}
