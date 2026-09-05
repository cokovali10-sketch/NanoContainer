//go:build linux

package api

import (
	"embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardFS embed.FS

func (s *Server) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	data, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
