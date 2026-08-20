package server

import (
	_ "embed"
	"net/http"
)

//go:embed ui.html
var indexHTML []byte

func (server *Server) index(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(indexHTML)
}
