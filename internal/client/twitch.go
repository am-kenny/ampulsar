package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	twitchAuthHost = "id.twitch.tv"
	twitchAPIHost  = "api.twitch.tv"
)

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type twitchResponse[T any] struct {
	Data []T `json:"data"`
}

type UserData struct {
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

// TwitchClient is safe for concurrent use.
type TwitchClient struct {
	clientID     string
	clientSecret string

	mu              sync.Mutex
	token           string
	tokenExpiration time.Time

	httpClient *http.Client
}

func NewTwitchClient(clientID, clientSecret string) *TwitchClient {
	return &TwitchClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// fetchToken performs the token request and touches no client state.
// It is called with tc.mu held, so it must not take the lock itself.
func (tc *TwitchClient) fetchToken(ctx context.Context) (tokenResponse, error) {
	const path = "/oauth2/token"

	authURL := url.URL{
		Scheme: "https",
		Host:   twitchAuthHost,
		Path:   path,
	}

	form := url.Values{}
	form.Set("client_id", tc.clientID)
	form.Set("client_secret", tc.clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, newTwitchAPIError(resp, path)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return tokenResponse{}, err
	}

	if tr.AccessToken == "" {
		return tokenResponse{}, errors.New("token response contained no access token")
	}

	return tr, nil
}

func (tc *TwitchClient) ensureToken(ctx context.Context) (string, error) {
	const tokenRefreshBuffer = time.Minute * 5

	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token == "" || time.Now().After(tc.tokenExpiration.Add(-1*tokenRefreshBuffer)) {
		tr, err := tc.fetchToken(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch token: %w", err)
		}

		tc.token = tr.AccessToken
		tc.tokenExpiration = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}

	return tc.token, nil
}

// invalidateToken clears the cached token only if it is still the one that
// failed. A concurrent goroutine may already have fetched a replacement, and
// discarding that would cause an unnecessary token request.
func (tc *TwitchClient) invalidateToken(stale string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token == stale {
		tc.token = ""
	}
}

func (tc *TwitchClient) callGetHelix(ctx context.Context, path string, params url.Values, result any) error {
	const maxAttempts = 2

	var err error

	for range maxAttempts {
		var token string

		token, err = tc.ensureToken(ctx)
		if err != nil {
			return err
		}

		err = tc.doGetHelix(ctx, token, path, params, result)
		if err == nil {
			return nil
		}

		var apiErr *TwitchAPIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
			return err
		}

		// Token rejected
		tc.invalidateToken(token)
	}

	return fmt.Errorf("after %d attempts: %w", maxAttempts, err)
}

func (tc *TwitchClient) doGetHelix(ctx context.Context, token, path string, params url.Values, result any) error {
	fullPath := "/helix/" + path

	u := url.URL{
		Scheme:   "https",
		Host:     twitchAPIHost,
		Path:     fullPath,
		RawQuery: params.Encode(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", tc.clientID)

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return newTwitchAPIError(resp, fullPath)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return err
	}

	return nil
}

func (tc *TwitchClient) FetchUserByUsername(ctx context.Context, username string) (*UserData, error) {
	params := url.Values{}
	params.Set("login", username)

	var resp twitchResponse[UserData]

	if err := tc.callGetHelix(ctx, "users", params, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("twitch user %q not found", username)
	}

	return &resp.Data[0], nil
}

func (tc *TwitchClient) FetchStreamByUsername(ctx context.Context, username string) (*streamData, error) {
	params := url.Values{}
	params.Add("user_login", username)

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

// TwitchAPIError is a non-2xx response from the Twitch API.
type TwitchAPIError struct {
	Status int
	Path   string
	Body   string
}

func (e *TwitchAPIError) Error() string {
	s := fmt.Sprintf("twitch %s: %d %s", e.Path, e.Status, http.StatusText(e.Status))
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

// newTwitchAPIError builds an error from a non-2xx response, reading a bounded
// prefix of the body for context.
func newTwitchAPIError(resp *http.Response, path string) *TwitchAPIError {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	return &TwitchAPIError{
		Status: resp.StatusCode,
		Path:   path,
		Body:   string(bytes.TrimSpace(b)),
	}
}
