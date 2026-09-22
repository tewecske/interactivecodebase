// Package weather calls a third-party forecast API.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const baseURL = "https://api.weather.example/v1/forecast"

// Forecast is a short forecast.
type Forecast struct {
	City    string  `json:"city"`
	Summary string  `json:"summary"`
	TempC   float64 `json:"temp_c"`
}

// Client calls the forecast API.
type Client struct {
	apiKey string
	http   *http.Client
}

// NewClient returns a Client authenticating with apiKey.
func NewClient(apiKey string) *Client {
	return &Client{apiKey: apiKey, http: &http.Client{}}
}

// Forecast fetches the forecast for city.
func (c *Client) Forecast(ctx context.Context, city string) (Forecast, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"?city="+url.QueryEscape(city), nil)
	if err != nil {
		return Forecast{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return Forecast{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Forecast{}, fmt.Errorf("weather: status %d", resp.StatusCode)
	}
	var f Forecast
	return f, json.NewDecoder(resp.Body).Decode(&f)
}
