package webserver

import (
	"crypto/subtle"
	"net/http"
)

// authMiddleware protects the whole web server with HTTP Basic Auth when
// security.web_auth_required is enabled. Constant-time comparison.
// Enabled without password → 503 (fail-closed): never left silently open.
func (ws *WebServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sec := ws.config.Security
		if !sec.WebAuthRequired {
			next.ServeHTTP(w, r)
			return
		}
		if sec.WebPassword == "" {
			ws.logger.Error("web_auth_required active without web_password — serving 503 (fail-closed)")
			http.Error(w, "Web auth enabled but no password configured", http.StatusServiceUnavailable)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(sec.WebUsername)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(sec.WebPassword)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="BTicino Bridge"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
