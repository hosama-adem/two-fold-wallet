package idempotency

import (
	"bytes"

	"github.com/gin-gonic/gin"
)

// capturingWriter mirrors the response body so we can store it for replay.
// Status is read from the underlying gin writer after the handler returns.
type capturingWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func newCapturingWriter(w gin.ResponseWriter) *capturingWriter {
	return &capturingWriter{ResponseWriter: w}
}

func (w *capturingWriter) Write(b []byte) (int, error) {
	_, _ = w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *capturingWriter) WriteString(s string) (int, error) {
	_, _ = w.body.WriteString(s)
	if ws, ok := w.ResponseWriter.(interface{ WriteString(string) (int, error) }); ok {
		return ws.WriteString(s)
	}
	return w.ResponseWriter.Write([]byte(s))
}

func (w *capturingWriter) Bytes() []byte {
	return append([]byte(nil), w.body.Bytes()...)
}
