package types

type BaseHttpResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
