package history

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   error  `json:"-"`
}

func (err *Error) Error() string {
	if err.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", err.Code, err.Message, err.Cause)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func (err *Error) Unwrap() error { return err.Cause }

func failure(code, message string, cause error) error {
	return &Error{Code: code, Message: message, Cause: cause}
}
