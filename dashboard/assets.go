package dashboard

import (
	"embed"
	"io/fs"
)

// distDir is the frontend build output inside the embedded tree.
const distDir = "web/dist"

// embeddedAssets holds the built React app. A committed web/dist/.gitkeep
// keeps the embed valid before the frontend is built.
//
//go:embed all:web/dist
var embeddedAssets embed.FS

// DistFS returns the built frontend rooted at its dist directory.
func DistFS() (fs.FS, error) {
	result, err := fs.Sub(embeddedAssets, distDir)

	return result, err
}
