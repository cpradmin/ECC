package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sync/atomic"
)

// MaxRequestBodyBytes caps the size of any request body the service will read.
// B3 FIX: without this an attacker can stream an unbounded body into
// json.Decoder and exhaust process memory (DoS).
const MaxRequestBodyBytes int64 = 10 << 20 // 10 MiB

// Observability counters. Exported through accessor funcs so tests and the
// metrics endpoint can assert on them without racing on package state.
var (
	encodeFailures  atomic.Int64
	panicsRecovered atomic.Int64
	oversizeBodies  atomic.Int64
)

// EncodeFailures returns the number of response encode/write failures observed.
func EncodeFailures() int64 { return encodeFailures.Load() }

// PanicsRecovered returns the number of handler panics caught by RecoverMiddleware.
func PanicsRecovered() int64 { return panicsRecovered.Load() }

// OversizeBodies returns the number of requests rejected for exceeding MaxRequestBodyBytes.
func OversizeBodies() int64 { return oversizeBodies.Load() }

// middlewareLogger is the package-level structured logger used by middleware and
// the response helpers. Handlers such as RegistryHandler have no logger of their
// own, so a package default keeps error reporting uniform. It is installed once
// by NewHandler; until then everything falls back to stderr.
var middlewareLogger atomic.Pointer[Logger]

// SetPackageLogger installs the process-wide logger used by middleware helpers.
func SetPackageLogger(l *Logger) { middlewareLogger.Store(l) }

func logf(level slog.Level, msg string, args ...any) {
	if l := middlewareLogger.Load(); l != nil && l.logger != nil {
		l.logger.Log(nil, level, msg, args...) //nolint:staticcheck // nil ctx is accepted by slog
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s %v\n", level, msg, args)
}

// ---------------------------------------------------------------------------
// B3: request body limiting
// ---------------------------------------------------------------------------

// MaxBytes wraps a handler so every request body is capped at
// MaxRequestBodyBytes. http.MaxBytesReader also closes the connection after the
// limit is hit, so a malicious client cannot keep streaming.
//
// Applied at the mux level in main.go: it is a no-op for bodyless GETs and
// covers every current and future POST handler, so no endpoint can be added
// without the protection.
func MaxBytes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// DecodeJSONBody decodes the request body into dst, translating failures into
// the correct HTTP status. It returns false once a response has been written.
//
// A body over the limit surfaces as *http.MaxBytesError and must be answered
// with 413, not 400: the client's JSON may have been perfectly valid.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			oversizeBodies.Add(1)
			logf(slog.LevelWarn, "request_body_too_large",
				slog.String("path", r.URL.Path),
				slog.Int64("limit_bytes", maxErr.Limit),
			)
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// B7: JSON encode / write error handling
// ---------------------------------------------------------------------------

// WriteJSON serialises payload and writes it with the given status code.
//
// Encoding happens into a buffer *before* any header is written. That split is
// the whole point: a marshalling error (unsupported type, NaN float, cyclic
// value) is still recoverable at that stage, so we can emit a clean 500 instead
// of the half-object-then-EOF that json.NewEncoder(w).Encode produces.
//
// Once the header is out the only honest response to a write error is to log it
// and abort the connection — the client is already receiving a truncated body
// and there is no status code left to correct it with. panic(http.ErrAbortHandler)
// is the idiomatic way to tell net/http to drop the connection without a stack
// dump; RecoverMiddleware deliberately re-panics it.
func WriteJSON(w http.ResponseWriter, status int, payload any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		encodeFailures.Add(1)
		logf(slog.LevelError, "response_encode_failed", slog.String("error", err.Error()))
		http.Error(w, "Internal server error: response encoding failed", http.StatusInternalServerError)
		return err
	}

	h := w.Header()
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/json")
	}
	h.Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(status)

	if _, err := w.Write(buf.Bytes()); err != nil {
		encodeFailures.Add(1)
		logf(slog.LevelError, "response_write_failed",
			slog.Int("status", status),
			slog.Int("bytes", buf.Len()),
			slog.String("error", err.Error()),
		)
		// Headers are already on the wire: close the connection.
		panic(http.ErrAbortHandler)
	}
	return nil
}

// WriteJSONOK is the common case: 200 with a JSON body.
func WriteJSONOK(w http.ResponseWriter, payload any) error {
	return WriteJSON(w, http.StatusOK, payload)
}

// ---------------------------------------------------------------------------
// Panic recovery
// ---------------------------------------------------------------------------

// Recover converts a panicking handler into a 500 instead of taking the whole
// process down. net/http already recovers per-connection, but it kills the
// connection silently and logs an unstructured stack to stderr — the client
// sees a broken pipe rather than an error, and nothing lands in our log
// pipeline. This middleware gives the caller a real 500 JSON body and an
// auditable structured log line.
//
// http.ErrAbortHandler is re-panicked unchanged: it is a deliberate signal
// (used by WriteJSON above) and must reach net/http to close the connection.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec) // intentional connection abort, not a bug
			}

			panicsRecovered.Add(1)
			stack := debug.Stack()
			logf(slog.LevelError, "handler_panic",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("panic", rec),
				slog.String("stack", string(stack)),
			)

			// Best effort: if the handler already wrote a header this is a
			// no-op with a "superfluous WriteHeader" log line, which is
			// strictly better than dropping the connection.
			defer func() { _ = recover() }() // never panic from the recovery path
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","error":"internal server error"}` + "\n"))
		}()
		next.ServeHTTP(w, r)
	})
}

// Chain applies middleware to h, outermost first.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
