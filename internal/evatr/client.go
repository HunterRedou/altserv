package evatr

import(
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const apiURL = "https://api.evatr.vies.bzst.de/app/v1/abfrage"

type ApiReq struct{
	RequestingVATID	string `json:"anfragendeUstid"`
	RequestedVATID	string `json:"angefragteUstid"`
	CompanyName	string `json:"firmenname,omitempty"`
	Street	string `json:"strasse,omitempty"`
	PostalCode	string `json:"plz,omitempty"`
	City	string `json:"ort,omitempty"`
}

type ApiRes struct{
	ID	string `json:"id"`
	RequestTime	string `json:"anfrageZeitpunkt"`
	ValidForm	string `json:"gueltigAb"`
	ValidUntil	string `json:"gueltigBis"`
	Status	string `json:"status"`
	CompanyNameMatch	string `json:"ergFirmenname"`
	StreetMatch	string `json:"ergStrasse"`
	PostalCodeMatch	string `json:"ergPlz"`
	CityMatch	string `json:"ergOrt"`
}

type Client struct{
	httpClient *http.Client
}

func NewClient() *Client{
	return &Client{
		httpClient: &http.Client{},
	}
}

func (c *Client) CheckUst(ctx context.Context, req ApiReq) (*ApiRes, error){
	body, err := json.Marshal(req)
	if err != nil{
		return nil, fmt.Errorf("send eVATR request: %w", err)
	}
	
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		apiURL,
		bytes.NewReader(body),
	)
	if err != nil{
		return nil, fmt.Errorf("create eVATR request: %w", err)
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	res, err := c.httpClient.Do(httpRequest)
	if err != nil{
		return nil, fmt.Errorf("send eVATR request: %w", err)
	}
	defer res.Body.Close()

	var result ApiRes

	if err := json.NewDecoder(res.Body).Decode(&result); err != nil{
		return nil, fmt.Errorf("decode eVATR response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300{
		return nil, fmt.Errorf(
			"eVATR HTTP %d: %s",
			res.StatusCode,
			result.Status,
		)
	}

	return &result, nil
}
