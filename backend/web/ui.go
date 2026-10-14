package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// Embedded single-page UI built from frontend/ with `output: "export"`.
// The directory is populated at build time (see Dockerfile); a local
// `go build` without it serves an empty UI.
//
//go:embed all:ui-out
var uiOut embed.FS

// UI returns the embedded web UI file system.
func UI() (fs.FS, error) {
	subFS, err := fs.Sub(uiOut, "ui-out")
	if err != nil {
		return nil, fmt.Errorf("failed to get UI sub-filesystem: %w", err)
	}

	return subFS, nil
}
