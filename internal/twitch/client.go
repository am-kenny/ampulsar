package twitch

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
	ProfileImageURL string `json:"profile_image_url"`
	OfflineImageURL string `json:"offline_image_url"`
	CreatedAt       string `json:"created_at"`
}

type streamData struct {
	ID           string `json:"id"`
	GameName     string `json:"game_name"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	StartedAt    string `json:"started_at"`    // The UTC date and time (in RFC3339 format) of when the broadcast began
	ThumbnailURL string `json:"thumbnail_url"` // A URL to an image of a frame from the last 5 minutes of the stream.
}

type videoData struct {
	ID           string `json:"id"`
	StreamID     string `json:"stream_id"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Type         string `json:"type"`     // The video's type. Possible values are: archive, highlight, upload
	Duration     string `json:"duration"` // The video's length in ISO 8601 duration format.
}

// Client is safe for concurrent use.
type Client struct {
	clientID     string
	clientSecret string

	authURL string
	apiURL  string

	mu              sync.Mutex
	token           string
	tokenExpiration time.Time
	now             func() time.Time // function to retrieve current time

	timeout    time.Duration
	httpClient *http.Client
}

func NewClient(clientID, clientSecret string, opts ...Option) *Client {
	c := &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		authURL:      "https://id.twitch.tv",
		apiURL:       "https://api.twitch.tv",
		now:          time.Now,
		timeout:      10 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	c.httpClient = &http.Client{Timeout: c.timeout}

	return c
}

type Option func(*Client)

func WithAuthURL(u string) Option {
	return func(c *Client) { c.authURL = u }
}

func WithAPIURL(u string) Option {
	return func(c *Client) { c.apiURL = u }
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// fetchToken performs the token request and touches no client state.
// It is called with tc.mu held, so it must not take the lock itself.
func (tc *Client) fetchToken(ctx context.Context) (tokenResponse, error) {
	const path = "/oauth2/token"

	authURL, err := url.Parse(tc.authURL)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("twitch auth: invalid auth url %q: %w", tc.authURL, err)
	}
	authURL.Path = path

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
		return tokenResponse{}, newAPIError(resp, path)
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

func (tc *Client) ensureToken(ctx context.Context) (string, error) {
	const tokenRefreshBuffer = time.Minute * 5

	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token == "" || tc.now().After(tc.tokenExpiration.Add(-1*tokenRefreshBuffer)) {
		tr, err := tc.fetchToken(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch token: %w", err)
		}

		tc.token = tr.AccessToken
		tc.tokenExpiration = tc.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}

	return tc.token, nil
}

// invalidateToken clears the cached token only if it is still the one that
// failed. A concurrent goroutine may already have fetched a replacement, and
// discarding that would cause an unnecessary token request.
func (tc *Client) invalidateToken(stale string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.token == stale {
		tc.token = ""
	}
}

func (tc *Client) callGetHelix[T any](ctx context.Context, path string, params url.Values, result *twitchResponse[T]) error {
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

		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
			return err
		}

		// Token rejected
		tc.invalidateToken(token)
	}

	return fmt.Errorf("after %d attempts: %w", maxAttempts, err)
}

func (tc *Client) doGetHelix[T any](ctx context.Context, token, path string, params url.Values, result *twitchResponse[T]) error {
	fullPath := "/helix/" + path

	u, err := url.Parse(tc.apiURL)
	if err != nil {
		return fmt.Errorf("twitch %s: invalid url %q: %w", fullPath, tc.apiURL, err)
	}
	u.Path = fullPath
	u.RawQuery = params.Encode()

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
		return newAPIError(resp, fullPath)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return err
	}

	return nil
}

func (tc *Client) FetchUserByUsername(ctx context.Context, username string) (*UserData, error) {
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

func (tc *Client) FetchStreamByUsername(ctx context.Context, username string) (*streamData, error) {
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

func (tc *Client) FetchStreamArchiveByUserIdAndStreamID(ctx context.Context, userID, streamID string) (*videoData, error) {
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

// APIError is a non-2xx response from the Twitch API.
type APIError struct {
	StatusCode int
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("twitch %s: %d %s", e.Path, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

// newAPIError builds an error from a non-2xx response, reading a bounded
// prefix of the body for context.
func newAPIError(resp *http.Response, path string) *APIError {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	return &APIError{
		StatusCode: resp.StatusCode,
		Path:       path,
		Body:       string(bytes.TrimSpace(b)),
	}
}
