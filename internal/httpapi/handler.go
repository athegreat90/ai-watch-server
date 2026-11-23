// Package httpapi exposes the monitor state over HTTP with the exact wire behavior of the
// original Python bridge: endpoints, authentication, body limits, error bodies and ASCII JSON.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/athegreat90/ai-watch-server/internal/monitor"
)

// MaxBody is the largest accepted request body in bytes.
const MaxBody = 4096

// AuthHeader is the request header that carries the shared token.
const AuthHeader = "X-Monitor-Key"

// send writes obj as compact ASCII JSON with the headers the firmware expects, then closes the
// connection. Encoding failures fall back to a plain 500 because the inputs are all internal types.
func send(w http.ResponseWriter, code int, obj any) {
	payload, err := encodeJSON(obj)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(payload)))
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "close")
	w.WriteHeader(code)
	_, _ = w.Write(payload) // the client may already be gone; there is nothing useful to do
}

// sendError writes a {"error": message} body with the given status code.
func sendError(w http.ResponseWriter, code int, message string) {
	send(w, code, map[string]string{"error": message})
}

// NewHandler returns the HTTP handler that serves store. Requests must carry token in the
// X-Monitor-Key header, except GET /health. The token is compared in constant time.
func NewHandler(store *monitor.Store, token string) http.Handler {
	authorized := func(r *http.Request) bool {
		return subtle.ConstantTimeCompare([]byte(r.Header.Get(AuthHeader)), []byte(token)) == 1
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RequestURI keeps the query string, so "/health?x" is not the health endpoint (as before).
		path := r.RequestURI
		switch r.Method {
		case http.MethodGet:
			serveGet(w, path, authorized(r), store)
		case http.MethodPost:
			servePost(w, r, path, authorized(r), store)
		default:
			// The Python server answered methods without a handler with 501.
			http.Error(w, "Unsupported method", http.StatusNotImplemented)
		}
	})
}

// serveGet handles GET requests: the public health check and the authenticated snapshots.
func serveGet(w http.ResponseWriter, path string, ok bool, store *monitor.Store) {
	if path == "/health" {
		send(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !ok {
		sendError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch path {
	case "/api/sessions":
		send(w, http.StatusOK, store.Snapshot(monitor.MaxSessions))
	case "/api/status":
		send(w, http.StatusOK, store.Snapshot(monitor.StatusSessions))
	default:
		sendError(w, http.StatusNotFound, "not found")
	}
}

// servePost handles POST requests: event and limits submissions. Authentication is checked
// first, then the path, then the body size, then the payload, matching the original order.
func servePost(w http.ResponseWriter, r *http.Request, path string, ok bool, store *monitor.Store) {
	if !ok {
		sendError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	post := store.PostEvent
	switch path {
	case "/api/event", "/api/events":
	case "/api/limits":
		post = store.PostLimits
	default:
		sendError(w, http.StatusNotFound, "not found")
		return
	}
	if r.ContentLength <= 0 || r.ContentLength > MaxBody {
		sendError(w, http.StatusRequestEntityTooLarge, "body must be 1-4096 bytes")
		return
	}
	body := make([]byte, r.ContentLength)
	var data map[string]any
	if _, err := io.ReadFull(r.Body, body); err != nil || json.Unmarshal(body, &data) != nil || data == nil {
		sendError(w, http.StatusBadRequest, "invalid event")
		return
	}
	if post(data) != nil {
		sendError(w, http.StatusBadRequest, "invalid event")
		return
	}
	send(w, http.StatusOK, map[string]bool{"ok": true})
}
