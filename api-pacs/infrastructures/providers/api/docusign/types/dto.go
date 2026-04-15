package types

type EnvelopeStatus string

const (
	EnvelopeStatusCompleted EnvelopeStatus = "completed"
)

const (
	EnvelopeStatusSent      EnvelopeStatus = "sent"
	EnvelopeStatusDelivered EnvelopeStatus = "delivered"
)

type GetEnvelopeRequest struct {
	FromDate   string `json:"from_date"`
	SearchText string `json:"search_text,omitempty"`
	Include    string `json:"include,omitempty"`
	Email      string `json:"email,omitempty"`
}

type GetEnvelopeRecipientResponse struct {
	Signers []Signer `json:"signers"`
}

type GetEnvelopeResponse struct {
	Envelopes []Envelope `json:"envelopes"`
}

type Credential struct {
	IntegrationKey string
	UserID         string
	AccountBaseURI string
	AuthServer     string
	PrivateKey     string
	AccountID      string
}

type Envelope struct {
	EnvelopeID string     `json:"envelopeId"`
	Recipients *Recipient `json:"recipients"`
}

type Recipient struct {
	Signers []Signer `json:"signers"`
}

type Signer struct {
	Email  string         `json:"email"`
	Status EnvelopeStatus `json:"status"`
}
