package tiktok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Room statuses reported by TikTok
const (
	roomStatusLive   = 2
	roomStatusPaused = 3 // the streamer paused; the broadcast has not ended
	roomStatusEnded  = 4
)

// ErrNoLiveRoom means TikTok has no LIVE room for the username: the user
// either does not exist or has never gone live.
var ErrNoLiveRoom = errors.New("no live room")

type ProfileData struct {
	AuthorName     string `json:"author_name"`      // The display name
	EmbedProductID string `json:"embed_product_id"` // The username
}

type userRoomResponse struct {
	StatusCode int          `json:"statusCode"` // 0 on success
	Message    string       `json:"message"`
	Data       userRoomData `json:"data"`
}

type userRoomData struct {
	User     *UserData     `json:"user"`
	LiveRoom *liveRoomData `json:"liveRoom"`
}

type UserData struct {
	ID       string `json:"id"`
	UniqueID string `json:"uniqueId"` // The username
	Nickname string `json:"nickname"`
	RoomID   string `json:"roomId"` // Stays set after the broadcast ends; not a liveness signal
	Status   int    `json:"status"`
}

type liveRoomData struct {
	Title     string `json:"title"`
	StartTime int64  `json:"startTime"` // Unix time in seconds of when the broadcast began
	Status    int    `json:"status"`
}

type Client struct {
	apiURL string

	timeout    time.Duration
	httpClient *http.Client
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		apiURL:  "https://www.tiktok.com",
		timeout: 10 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	c.httpClient = &http.Client{Timeout: c.timeout}

	return c
}

type Option func(*Client)

func WithAPIURL(u string) Option {
	return func(c *Client) { c.apiURL = u }
}

func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

func (tc *Client) doGet[T any](ctx context.Context, path string, params url.Values, result T) error {
	u, err := url.Parse(tc.apiURL)
	if err != nil {
		return fmt.Errorf("tiktok %s: invalid url %q: %w", path, tc.apiURL, err)
	}
	u.Path = path
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return newAPIError(resp, path)
	}

	// A blocked request can come back as 200 with a captcha page or an empty
	// body. Both fail to decode and surface as an error, never as "offline"
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("tiktok %s: decode response: %w", path, err)
	}

	return nil
}

func (tc *Client) getUserRoom(ctx context.Context, username string) (*userRoomResponse, error) {
	const path = "/api-live/user/room/"

	params := url.Values{}
	params.Set("aid", "1988")
	params.Set("app_name", "tiktok_web")
	params.Set("device_platform", "web_pc")
	params.Set("sourceType", "54")
	params.Set("uniqueId", username)

	var resp userRoomResponse

	if err := tc.doGet(ctx, path, params, &resp); err != nil {
		return nil, err
	}

	switch {
	case resp.Message == "user_not_found":
		return nil, fmt.Errorf("tiktok user %q: %w", username, ErrNoLiveRoom)
	case resp.StatusCode != 0:
		return nil, fmt.Errorf("tiktok %s: status code %d: %s", path, resp.StatusCode, resp.Message)
	case resp.Data.User == nil:
		return nil, fmt.Errorf("tiktok %s: response for %q contained no user", path, username)
	}

	return &resp, nil
}

// FetchProfileByUsername works for any existing user, including one who has never gone live.
func (tc *Client) FetchProfileByUsername(ctx context.Context, username string) (*ProfileData, error) {
	const path = "/oembed"

	params := url.Values{}
	params.Set("url", "https://www.tiktok.com/@"+username)

	var profile ProfileData

	if err := tc.doGet(ctx, path, params, &profile); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
			return nil, fmt.Errorf("tiktok user %q not found", username)
		}
		return nil, err
	}

	if profile.EmbedProductID == "" {
		return nil, fmt.Errorf("tiktok %s: response for %q contained no username", path, username)
	}

	return &profile, nil
}

// FetchUserByUsername returns ErrNoLiveRoom for a user who has never gone live.
func (tc *Client) FetchUserByUsername(ctx context.Context, username string) (*UserData, error) {
	resp, err := tc.getUserRoom(ctx, username)
	if err != nil {
		return nil, err
	}

	if resp.Data.User.ID == "" {
		return nil, fmt.Errorf("tiktok user %q: response contained no user id", username)
	}

	return resp.Data.User, nil
}

// FetchUserRoomByUsername returns nil if the user is offline.
// A non-nil result always has both User and LiveRoom set.
func (tc *Client) FetchUserRoomByUsername(ctx context.Context, username string) (*userRoomData, error) {
	resp, err := tc.getUserRoom(ctx, username)
	if err != nil {
		return nil, err
	}

	user, room := resp.Data.User, resp.Data.LiveRoom

	// liveRoom.status is the authoritative one; user.status is the fallback
	// for users whose response carries no liveRoom.
	status := user.Status
	if room != nil {
		status = room.Status
	}

	switch status {
	case roomStatusLive, roomStatusPaused:
		if room == nil {
			return nil, fmt.Errorf("tiktok user %q is live but the response contained no liveRoom", username)
		}
		if user.RoomID == "" {
			return nil, fmt.Errorf("tiktok user %q is live but the response contained no room id", username)
		}
		return &resp.Data, nil
	case roomStatusEnded:
		return nil, nil
	default:
		return nil, fmt.Errorf("tiktok user %q: unknown room status %d", username, status)
	}
}

// APIError is a non-2xx response from TikTok.
type APIError struct {
	StatusCode int
	Path       string
	Body       string
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("tiktok %s: %d %s", e.Path, e.StatusCode, http.StatusText(e.StatusCode))
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
