package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type streamData struct {
	ID           string `json:"id"`
	GameName     string `json:"game_name"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	StartedAt    string `json:"started_at"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type streamsResponse struct {
	Data []streamData `json:"data"`
}

// TODO: make TwitchClient safe for concurrent use.
type TwitchClient struct {
	clientID        string
	clientSecret    string
	token           string
	tokenExpiration time.Time
	httpClient      *http.Client
}

func NewTwitchClient(clientID, clientSecret string) *TwitchClient {
	return &TwitchClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

func twitchBaseURL() url.URL {
	return url.URL{
		Scheme: "https",
		Host:   "api.twitch.tv",
	}
}

func (tc *TwitchClient) fetchToken(ctx context.Context) error {
	authURL := twitchBaseURL()
	authURL.Path = "oauth2/token"

	form := url.Values{}
	form.Set("client_id", tc.clientID)
	form.Set("client_secret", tc.clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("token request failed: %s", resp.Status)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return err
	}

	if tr.AccessToken == "" {
		return fmt.Errorf("token response contained no access token")
	}

	tc.token = tr.AccessToken
	tc.tokenExpiration = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)

	return nil
}

func (tc *TwitchClient) ensureToken(ctx context.Context) error {
	const tokenRefreshBuffer = time.Minute * 5

	if tc.token == "" || time.Now().After(tc.tokenExpiration.Add(-1*tokenRefreshBuffer)) {
		if err := tc.fetchToken(ctx); err != nil {
			return fmt.Errorf("failed to fetch token: %w", err)
		}
	}

	return nil
}

func (tc *TwitchClient) FetchStreamByUsername(ctx context.Context, channelName string) (*streamData, error) {
	if err := tc.ensureToken(ctx); err != nil {
		return nil, err
	}

	streamsURL := twitchBaseURL()
	streamsURL.Path = "/helix/streams"

	q := streamsURL.Query()
	q.Set("user_login", channelName)
	streamsURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamsURL.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+tc.token)
	req.Header.Set("Client-Id", tc.clientID)

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("check live request failed: %s", resp.Status)
	}

	var sr streamsResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}

	if len(sr.Data) == 0 {
		return nil, nil
	}

	return &sr.Data[0], nil
}
