// REFERENCE: https://developers.docusign.com/docs/esign-rest-api/reference/
package docusign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"api-pacs/infrastructures/providers/api/docusign/types"
	apiError "api-pacs/internal/errors"
)

type DocusignAPI struct {
	IntegrationKey string
	UserID         string
	AccountBaseURI string
	AuthServer     string
	PrivateKey     string
	AccountID      string
}

var (
	client *http.Client = &http.Client{Timeout: 5 * time.Minute}
)

// Init initialize the DocuSignAPI
func Init(credential types.Credential) *DocusignAPI {
	return &DocusignAPI{
		IntegrationKey: credential.IntegrationKey,
		UserID:         credential.UserID,
		AccountBaseURI: credential.AccountBaseURI,
		AuthServer:     credential.AuthServer,
		PrivateKey:     credential.PrivateKey,
		AccountID:      credential.AccountID,
	}
}

// GetAccessToken get access token from docusign
func (d *DocusignAPI) GetAccessToken() (string, error) {
	/// generate jwt token
	jwtToken, err := d.generateJWT()
	if err != nil {
		log.Println("Error:", err)
		return "", errors.New(apiError.DocusignError)
	}

	data := map[string]string{
		"grant_type": "urn:ietf:params:oauth:grant-type:jwt-bearer",
		"assertion":  jwtToken,
	}

	buf := new(bytes.Buffer)
	err = json.NewEncoder(buf).Encode(data)
	if err != nil {
		log.Println("Error:", err)
		return "", errors.New(apiError.DocusignError)
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/oauth/token", d.AuthServer), buf)
	if err != nil {
		log.Println("Error:", err)
		return "", errors.New(apiError.DocusignError)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error:", err)
		return "", errors.New(apiError.DocusignError)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Println("Error:", err)
			return "", errors.New(apiError.DocusignError)
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)
		return "", errors.New(apiError.DocusignError)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		log.Println("Error:", err)
		return "", errors.New(apiError.DocusignError)
	}

	accessToken, exists := response["access_token"].(string)
	if !exists {
		log.Println("Error:", "access_token not found")
		return "", errors.New(apiError.DocusignError)
	}

	return accessToken, nil
}

// GetEnvelopes get envelopes
func (d *DocusignAPI) GetEnvelopes(accessToken string, request types.GetEnvelopeRequest) ([]types.Envelope, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/restapi/v2.1/accounts/%s/envelopes", d.AccountBaseURI, d.AccountID), nil)
	if err != nil {
		log.Println("Error:", err)
		return nil, errors.New(apiError.DocusignError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

	// add query params
	query := req.URL.Query()
	query.Add("from_date", request.FromDate)
	query.Add("search_text", request.SearchText)
	query.Add("include", request.Include)
	req.URL.RawQuery = query.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New(apiError.DocusignError)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Println("Error:", err)
			return nil, errors.New(apiError.DocusignError)
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)
		return nil, errors.New(apiError.DocusignError)
	}

	var response types.GetEnvelopeResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		log.Println("Error GetLatestEnvelopes decode:", err)
		return nil, errors.New(apiError.DocusignError)
	}

	return response.Envelopes, nil
}

// GetEnvelopeRecipients get recipients of an envelope
func (d *DocusignAPI) GetEnvelopeRecipients(accessToken, envelopeID string) ([]types.Signer, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/restapi/v2.1/accounts/%s/envelopes/%s/recipients", d.AccountBaseURI, d.AccountID, envelopeID), nil)
	if err != nil {
		log.Println("Error:", err)
		return nil, errors.New(apiError.DocusignError)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error:", err)
		return nil, errors.New(apiError.DocusignError)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Println("Error:", err)
			return nil, errors.New(apiError.DocusignError)
		}

		errorMessage := string(response)
		log.Println("Error:", errorMessage)
		return nil, errors.New(apiError.DocusignError)
	}

	var response types.GetEnvelopeRecipientResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		log.Println("Error GetEnvelopeRecipients decode:", err)
		return nil, errors.New(apiError.DocusignError)
	}

	return response.Signers, nil
}

func (d *DocusignAPI) generateJWT() (string, error) {
	privateKeyBytes := []byte(d.PrivateKey)
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(privateKeyBytes)
	if err != nil {
		return "", err
	}

	claims := jwt.MapClaims{
		"iss":   d.IntegrationKey,
		"sub":   d.UserID,
		"aud":   d.AuthServer,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Minute * 5).Unix(), // docusign max is 5 minutes
		"scope": "signature impersonation",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}
