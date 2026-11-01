package kiro

// InvalidRequestError identifies client input that Kiro cannot represent.
type InvalidRequestError struct {
	Message string
}

func (e *InvalidRequestError) Error() string {
	return e.Message
}
