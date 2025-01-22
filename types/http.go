package types

type BaseHttpResponse struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
