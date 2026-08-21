package server

import (
	"embed"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// inspectorAssets contains the prebuilt UI so the Farfield binary remains
// self-contained and does not require a JavaScript runtime in production.
//
//go:embed ui/dist/*
var inspectorAssets embed.FS

func (server *Server) index(writer http.ResponseWriter, request *http.Request) {
	asset := strings.TrimPrefix(request.URL.Path, "/")
	if asset == "" {
		asset = "index.html"
	}
	if strings.Contains(asset, "..") {
		http.NotFound(writer, request)
		return
	}
	data, err := inspectorAssets.ReadFile("ui/dist/" + asset)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(asset))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; style-src 'self'; script-src 'self'; connect-src 'self'; font-src 'self'")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if asset == "index.html" {
		writer.Header().Set("Cache-Control", "no-store")
	} else {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	_, _ = writer.Write(data)
}
