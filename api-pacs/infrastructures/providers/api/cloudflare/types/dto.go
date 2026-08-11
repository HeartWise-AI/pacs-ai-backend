package types

type ValidateTurnstileTokenResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}
