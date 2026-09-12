package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// httpError lets validation helpers return a status and message without needing
// the echo context, keeping the rules readable and testable.
type httpError struct {
	code    int
	message string
}

func (e *httpError) render(c echo.Context) error {
	return c.JSON(e.code, apiError{Error: e.message})
}

func errBadRequest(format string, args ...any) *httpError {
	return &httpError{code: http.StatusBadRequest, message: sprintf(format, args...)}
}

func errForbidden(format string, args ...any) *httpError {
	return &httpError{code: http.StatusForbidden, message: sprintf(format, args...)}
}

func errNotFound(format string, args ...any) *httpError {
	return &httpError{code: http.StatusNotFound, message: sprintf(format, args...)}
}

func errInternal(format string, args ...any) *httpError {
	return &httpError{code: http.StatusInternalServerError, message: sprintf(format, args...)}
}
