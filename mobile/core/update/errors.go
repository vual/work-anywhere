package update

import "errors"

// Stable error codes shared with Desktop (see
// desktop/docs/self-update/manifest.md#client-verification) plus Android ones.
const (
	CodeManifestUnavailable = CodeUnavailable
	CodeManifestInvalid     = CodeInvalid
	CodeDownloadFailed      = "update_download_failed"
	CodeNoAsset             = "update_no_asset"
	CodeInProgress          = "update_in_progress"
	CodeCancelled           = "update_cancelled"
)

// NewError builds a coded update error.
func NewError(code, message string) *Error { return &Error{Code: code, Message: message} }

// ErrorCode extracts a stable code, defaulting to fallback.
func ErrorCode(err error, fallback string) (string, string) {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code, coded.Message
	}
	return fallback, err.Error()
}
