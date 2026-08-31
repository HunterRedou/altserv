package vies

import(
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiURL = "https://ec.europa.eu/taxation_customs/vies/rest-api"

var transientErrors = map[string]bool{
	"GLOBAL_MAX_CONCURRENT_REQ": true,
	"MS_MAX_CONCURRENT_REQ":     true,
	"MS_UNAVAILABLE":            true,
	"SERVICE_UNAVAILABLE":       true,
	"TIMEOUT":                   true,
}

type ApiRes struct {
	IsValid           bool   `json:"isValid"`
	RequestDate       string `json:"requestDate"`
	UserError         string `json:"userError"`
	Name              string `json:"name"`
	Address           string `json:"address"`
	RequestIdentifier string `json:"requestIdentifier"`
	OriginalVATNumber string `json:"originalVatNumber"`
	VATNumber         string `json:"vatNumber"`
	ViesApproximate   struct {
		Name             string `json:"name"`
		Street           string `json:"street"`
		PostalCode       string `json:"postalCode"`
		City             string `json:"city"`
		CompanyType      string `json:"companyType"`
		MatchName        int    `json:"matchName"`
		MatchStreet      int    `json:"matchStreet"`
		MatchPostalCode  int    `json:"matchPostalCode"`
		MatchCity        int    `json:"matchCity"`
		MatchCompanyType int    `json:"matchCompanyType"`
	} `json:"viesApproximate"`
}

type ApiError struct{
	Code string
	Transient bool
}

func (e *ApiError) Error() string{
	return fmt.Sprintf("vies: query failed: %s", e.Code)
}

type Client struct{
	httpClient *http.Client
	apiURL string
}

func NewClient() *Client{
	return &Client{
		httpClient: &http.Client{Timeout: 20 * time.Second},
		apiURL: apiURL,
	}
}

func (c *Client) CheckUst(ctx context.Context, countyCode string, vatNumber string) (*ApiRes, error){
	url := fmt.Sprintf("%s/ms/%s/vat/%s", c.apiURL, countyCode, vatNumber)

	
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)
	if err != nil{
		return nil, fmt.Errorf("create vies request: %w", err)
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	res, err := c.httpClient.Do(httpRequest)
	if err != nil{
		return nil, fmt.Errorf("send vies request: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil{
		return nil, fmt.Errorf("read vies response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300{
		return nil, fmt.Errorf("vies HTTP %d: %s", res.StatusCode, string(body))
	}
	var result ApiRes

	if err := json.Unmarshal(body, &result); err != nil{
		return nil, fmt.Errorf("decode vies response: %w", err)
	}

	if result.UserError != "" && result.UserError != "INVALID"{
		return nil, &ApiError{
			Code:	result.UserError,
			Transient: transientErrors[result.UserError],
		}
	}

	return &result, nil
}

func IsValidUST(ctx context.Context, ustID string) (bool, error){
	if len(ustID) < 3{
		return false, fmt.Errorf("ustID %q too short", ustID)
	}
	res, err := NewClient().CheckUst(ctx, ustID[:2], ustID[2:])
	if err != nil{
		return false, err
	}
	return res.IsValid, nil
}
