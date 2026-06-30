package daemon

import (
	"errors"
	"net/http"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

type daemonAPIErrorCategory string

const (
	daemonAPIErrorBadRequest         daemonAPIErrorCategory = "bad_request"
	daemonAPIErrorConflict           daemonAPIErrorCategory = "conflict"
	daemonAPIErrorAccessDenied       daemonAPIErrorCategory = "access_denied"
	daemonAPIErrorServiceUnavailable daemonAPIErrorCategory = "service_unavailable"
	daemonAPIErrorInternal           daemonAPIErrorCategory = "internal"
)

var errConnectionAlreadyActive = errors.New("connection already active; run podlaz disconnect before connecting another profile")

type daemonAPIError struct {
	category daemonAPIErrorCategory
	err      error
}

func (e *daemonAPIError) Error() string {
	if e == nil || e.err == nil {
		return "daemon API error"
	}
	return e.err.Error()
}

func (e *daemonAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func categorizeDaemonAPIError(category daemonAPIErrorCategory, err error) error {
	if err == nil {
		return nil
	}
	return &daemonAPIError{category: category, err: err}
}

func daemonAPIBadRequest(err error) error {
	return categorizeDaemonAPIError(daemonAPIErrorBadRequest, err)
}

func daemonAPIConflict(err error) error {
	return categorizeDaemonAPIError(daemonAPIErrorConflict, err)
}

func daemonAPIAccessDenied(err error) error {
	return categorizeDaemonAPIError(daemonAPIErrorAccessDenied, err)
}

func daemonAPIServiceUnavailable(err error) error {
	return categorizeDaemonAPIError(daemonAPIErrorServiceUnavailable, err)
}

func daemonAPIInternal(err error) error {
	return categorizeDaemonAPIError(daemonAPIErrorInternal, err)
}

func daemonAPIHTTPStatusCode(err error) int {
	if profile.IsValidationError(err) {
		return http.StatusBadRequest
	}
	if errors.Is(err, errConnectionAlreadyActive) || errors.Is(err, errFullTunnelConnectionBecameActive) {
		return http.StatusConflict
	}
	var apiErr *daemonAPIError
	if !errors.As(err, &apiErr) {
		return http.StatusInternalServerError
	}
	switch apiErr.category {
	case daemonAPIErrorBadRequest:
		return http.StatusBadRequest
	case daemonAPIErrorConflict:
		return http.StatusConflict
	case daemonAPIErrorAccessDenied:
		return http.StatusForbidden
	case daemonAPIErrorServiceUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeDaemonAPIHTTPError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	http.Error(w, err.Error(), daemonAPIHTTPStatusCode(err))
}

func categorizeAuthorizationError(err error) error {
	switch {
	case errors.Is(err, ErrAuthorizationDenied):
		return daemonAPIAccessDenied(err)
	case errors.Is(err, ErrAuthorizationUnavailable):
		return daemonAPIServiceUnavailable(err)
	default:
		return daemonAPIInternal(err)
	}
}
