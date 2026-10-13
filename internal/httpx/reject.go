package httpx

import (
	"io"
	"net/http"
)

func Reject(w http.ResponseWriter, r *http.Request, message string, status int) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 2<<20))
	}
	http.Error(w, message, status)
}
