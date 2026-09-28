package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed dist
var dist embed.FS

// newStaticHandler serves the embedded UI build. Static files carry no data;
// everything that does goes through the UIService RPC.
func newStaticHandler() (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return http.FileServerFS(sub), nil
}
