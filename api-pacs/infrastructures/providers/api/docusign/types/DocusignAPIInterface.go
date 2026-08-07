package types

// DocusignAPIInterface is the interface for the docusign API
type DocusignAPIInterface interface {
	// GetAccessToken get access token from docusign
	GetAccessToken() (string, error)
	// GetEnvelopes get envelopes
	GetEnvelopes(accessToken string, request GetEnvelopeRequest) ([]Envelope, error)
	// GetEnvelopeRecipients get recipients of an envelope
	GetEnvelopeRecipients(accessToken, envelopeID string) ([]Signer, error)
}
