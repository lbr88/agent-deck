package session

import "fmt"

// NonFatalWarning reports a mutation side effect that failed after the primary
// state change was already persisted. Callers should surface it as a warning,
// not as a failed mutation.
type NonFatalWarning struct {
	Message string
	Err     error
}

func NewNonFatalWarning(message string, err error) *NonFatalWarning {
	if err == nil {
		return nil
	}
	return &NonFatalWarning{Message: message, Err: err}
}

func (w *NonFatalWarning) Error() string {
	if w == nil {
		return ""
	}
	if w.Err == nil {
		return w.Message
	}
	if w.Message == "" {
		return w.Err.Error()
	}
	return fmt.Sprintf("%s: %v", w.Message, w.Err)
}

func (w *NonFatalWarning) Unwrap() error {
	if w == nil {
		return nil
	}
	return w.Err
}
