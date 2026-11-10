package server

// ErrorResponse is the single JSON error shape used by the API.
type ErrorResponse struct {
	Error string `json:"error"`
}
