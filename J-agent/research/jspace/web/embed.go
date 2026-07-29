package web

import "embed"

// Files contains the dependency-free viewer shell and its illustrative sample.
//
//go:embed index.html styles.css app.js demo.json
var Files embed.FS
