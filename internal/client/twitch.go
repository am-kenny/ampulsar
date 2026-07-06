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

type twitchResponse[T any] struct {
	Data []T `json:"data"`
}

type userData struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	ProfileImageUrl string `json:"profile_image_url"`
	OfflineImageUrl string `json:"offline_image_url"`
	CreatedAt       string `json:"created_at"`
}

type streamData struct {
	ID           string `json:"id"`
	GameName     string `json:"game_name"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	StartedAt    string `json:"started_at"`
	ThumbnailURL string `json:"thumbnail_url"`
}

type videoData struct {
	ID           string `json:"id"`
	StreamID     string `json:"stream_id"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Type         string `json:"type"`
	Duration     string `json:"duration"`
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

func (tc *TwitchClient) callGetHelix(ctx context.Context, path string, params url.Values, result any) error {
	if err := tc.ensureToken(ctx); err != nil {
		return err
	}

	base := twitchBaseURL()
	u, err := url.JoinPath(base.String(), "helix", path)
	if err != nil {
		return err
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return err
	}

	parsed.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+tc.token)
	req.Header.Set("Client-Id", tc.clientID)

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// body, _ := io.ReadAll(resp.Body)
		// fmt.Println("raw response:", string(body))
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("%s request failed: %s", parsed.String(), resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return err
	}

	return nil
}

func (tc *TwitchClient) FetchUserByUsername(ctx context.Context, username string) (*userData, error) {
	params := url.Values{}
	params.Set("login", username)

	var resp twitchResponse[userData]

	if err := tc.callGetHelix(ctx, "users", params, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, nil
	}

	return &resp.Data[0], nil
}

func (tc *TwitchClient) FetchStreamByUsername(ctx context.Context, username string) (*streamData, error) {
	params := url.Values{}
	params.Set("user_login", username)

	var resp twitchResponse[streamData]

	if err := tc.callGetHelix(ctx, "streams", params, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, nil
	}

	return &resp.Data[0], nil
}

func (tc *TwitchClient) FetchStreamArchiveByUserIdAndStreamID(ctx context.Context, userID, streamID string) (*videoData, error) {
	params := url.Values{}
	params.Set("user_id", userID)
	params.Set("type", "archive")

	var resp twitchResponse[videoData]

	if err := tc.callGetHelix(ctx, "videos", params, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, nil
	}

	for _, v := range resp.Data {
		if v.StreamID == streamID {
			return &v, nil
		}
	}

	return nil, nil
}
