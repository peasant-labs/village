package middleware

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// unmatchedRoute is what the request log records for a request no route
// matched. The raw path is never a fallback: it can carry transcript ids,
// usernames, refs and other values the log must not hold.
const unmatchedRoute = "unmatched"

// otherMethod is what the request log records for a method outside the
// standard set. A method token is client-chosen text, so only known tokens are
// echoed.
const otherMethod = "OTHER"

// loggedMethods is the closed set of method tokens the request log echoes.
var loggedMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
	http.MethodConnect: true, http.MethodTrace: true,
}

// Logger writes one line per request to the standard logger: the method, the
// matched route template, the status and the duration.
func Logger(next http.Handler) http.Handler {
	return RequestLogger(log.Default())(next)
}

// RequestLogger is Logger writing to out. The line names the matched route
// template (for example /api/v1/transcripts/{id}/segments/{index}), never the
// raw path, and never the query string, a body, a header or a credential. A
// request that matched no route is logged as "unmatched".
func RequestLogger(out *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			out.Printf("%s %s %d %s", loggedMethod(r.Method), routeTemplate(r), ww.status, time.Since(start))
		})
	}
}

// routeTemplate reads the pattern chi matched once routing has finished. It is
// built only from registered patterns, so it holds no request value.
func routeTemplate(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return unmatchedRoute
	}
	return pattern
}

func loggedMethod(method string) string {
	if loggedMethods[method] {
		return method
	}
	return otherMethod
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
