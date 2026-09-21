package tiktok_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/tiktok"
)

// Response bodies, trimmed to the fields the client reads. To capture fresh ones:
//
//	curl -s 'https://www.tiktok.com/api-live/user/room/?aid=1988&sourceType=54&uniqueId=USERNAME'
//	curl -s 'https://www.tiktok.com/oembed?url=https://www.tiktok.com/@USERNAME'
const (
	username  = "test_user"
	startTime = 1758276000

	profile         = `{"version":"1.0","type":"rich","title":"Test User's Creator Profile","author_url":"https://www.tiktok.com/@test_user","author_name":"Test User","provider_name":"TikTok","embed_product_id":"test_user","embed_type":"profile"}`
	profileNotFound = `{"message":"Something went wrong","code":400}`

	liveRoom       = `{"statusCode":0,"message":"","data":{"user":{"id":"100","uniqueId":"test_user","nickname":"Test User","roomId":"200","status":2},"liveRoom":{"streamId":"300","title":"Title with & and <tags>","startTime":1758276000,"status":2}}}`
	pausedRoom     = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200","status":3},"liveRoom":{"streamId":"300","title":"brb","startTime":1758276000,"status":3}}}`
	endedRoom      = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200","status":4},"liveRoom":{"streamId":"300","title":"old title","startTime":1758276000,"status":4}}}`
	noLiveRoom     = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"","status":4}}}`
	statusMismatch = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200","status":2},"liveRoom":{"streamId":"300","status":4}}}`

	liveNoRoomID   = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","status":2},"liveRoom":{"title":"t","status":2}}}`
	liveNoLiveRoom = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200","status":2}}}`
	unknownStatus  = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200","status":1},"liveRoom":{"streamId":"300","status":1}}}`
	missingStatus  = `{"statusCode":0,"data":{"user":{"id":"100","uniqueId":"test_user","roomId":"200"},"liveRoom":{"title":"t"}}}`
	missingUser    = `{"statusCode":0,"data":{}}`
	missingUserID  = `{"statusCode":0,"data":{"user":{"uniqueId":"test_user","status":4}}}`

	userNotFound = `{"statusCode":19881007,"message":"user_not_found","data":{}}`
	apiError     = `{"statusCode":10101,"message":"params_error"}`
	captchaPage  = `<!DOCTYPE html><html><head><title>Security Check</title></head><body></body></html>`
)

func newClient(t *testing.T, handler http.HandlerFunc, opts ...tiktok.Option) *tiktok.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return tiktok.NewClient(append([]tiktok.Option{tiktok.WithAPIURL(srv.URL)}, opts...)...)
}

// respond is the common case: one fixed status and body for every request
func respond(t *testing.T, status int, body string) *tiktok.Client {
	t.Helper()
	return newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestFetchProfileHappyPath(t *testing.T) {
	c := respond(t, http.StatusOK, profile)

	p, err := c.FetchProfileByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("FetchProfileByUsername: %v", err)
	}

	want := tiktok.ProfileData{AuthorName: "Test User", EmbedProductID: username}
	if *p != want {
		t.Errorf("profile = %+v, want %+v", *p, want)
	}
}

// A user who has never gone live still has a profile, which is why resolving
// goes through oEmbed rather than the LIVE endpoint.
func TestProfileRequestCarriesProfileURL(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oembed" {
			t.Errorf("path = %q, want /oembed", r.URL.Path)
		}
		if got := r.URL.Query().Get("url"); got != "https://www.tiktok.com/@"+username {
			t.Errorf("url param = %q, want the profile URL", got)
		}
		_, _ = w.Write([]byte(profile))
	})

	if _, err := c.FetchProfileByUsername(context.Background(), username); err != nil {
		t.Fatalf("FetchProfileByUsername: %v", err)
	}
}

func TestProfileNotFound(t *testing.T) {
	c := respond(t, http.StatusBadRequest, profileNotFound)

	_, err := c.FetchProfileByUsername(context.Background(), username)
	if err == nil {
		t.Fatal("want error for missing user, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %q, want it to say the user was not found", err)
	}
}

func TestProfileErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantMsg string
	}{
		{"no username in response", http.StatusOK, `{"author_name":"Test User"}`, "contained no username"},
		{"captcha", http.StatusOK, captchaPage, "decode response"},
		{"server error", http.StatusBadGateway, "<html>502</html>", "502 Bad Gateway"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := respond(t, tt.status, tt.body)

			p, err := c.FetchProfileByUsername(context.Background(), username)
			if err == nil {
				t.Fatalf("got %+v, want error", p)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestFetchUserHappyPath(t *testing.T) {
	c := respond(t, http.StatusOK, liveRoom)

	u, err := c.FetchUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("FetchUserByUsername: %v", err)
	}

	want := tiktok.UserData{ID: "100", UniqueID: username, Nickname: "Test User", RoomID: "200", Status: 2}
	if *u != want {
		t.Errorf("user = %+v, want %+v", *u, want)
	}
}

// The LIVE endpoint answers user_not_found both for a user who does not exist
// and for one who has never gone live.
func TestUserWithoutLiveRoom(t *testing.T) {
	c := respond(t, http.StatusOK, userNotFound)

	_, err := c.FetchUserByUsername(context.Background(), username)
	if !errors.Is(err, tiktok.ErrNoLiveRoom) {
		t.Fatalf("err = %v, want ErrNoLiveRoom", err)
	}
}

func TestUserRequestCarriesParams(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api-live/user/room/" {
			t.Errorf("path = %q, want /api-live/user/room/", r.URL.Path)
		}
		for k, want := range map[string]string{"uniqueId": username, "aid": "1988", "sourceType": "54", "app_name": "tiktok_web"} {
			if got := r.URL.Query().Get(k); got != want {
				t.Errorf("query %s = %q, want %q", k, got, want)
			}
		}
		_, _ = w.Write([]byte(liveRoom))
	})

	if _, err := c.FetchUserByUsername(context.Background(), username); err != nil {
		t.Fatalf("FetchUserByUsername: %v", err)
	}
}

func TestUserErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantMsg string
	}{
		{"missing user", http.StatusOK, missingUser, "contained no user"},
		{"missing id", http.StatusOK, missingUserID, "contained no user id"},
		{"api error", http.StatusOK, apiError, "status code 10101: params_error"},
		{"captcha", http.StatusOK, captchaPage, "decode response"},
		{"empty body", http.StatusOK, "", "decode response"},
		{"forbidden", http.StatusForbidden, "denied", "403 Forbidden: denied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := respond(t, tt.status, tt.body)

			u, err := c.FetchUserByUsername(context.Background(), username)
			if err == nil {
				t.Fatalf("got %+v, want error", u)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestLiveRoomReturnsBroadcast(t *testing.T) {
	c := respond(t, http.StatusOK, liveRoom)

	room, err := c.FetchUserRoomByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("FetchUserRoomByUsername: %v", err)
	}
	if room == nil {
		t.Fatal("want a room, got nil (offline)")
	}
	if room.User.RoomID != "200" {
		t.Errorf("room id = %q, want 200", room.User.RoomID)
	}
	if room.LiveRoom.Title != "Title with & and <tags>" {
		t.Errorf("title = %q", room.LiveRoom.Title)
	}
	if room.LiveRoom.StartTime != startTime {
		t.Errorf("start time = %d, want %d", room.LiveRoom.StartTime, startTime)
	}
}

// A pause is not the end of a broadcast.
func TestPausedRoomCountsAsLive(t *testing.T) {
	c := respond(t, http.StatusOK, pausedRoom)

	room, err := c.FetchUserRoomByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("FetchUserRoomByUsername: %v", err)
	}
	if room == nil {
		t.Fatal("want a room, got nil (offline)")
	}
	if room.LiveRoom.Title != "brb" {
		t.Errorf("title = %q, want brb", room.LiveRoom.Title)
	}
}

func TestEndedRoomReturnsNil(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		// The room id stays set after a stream ends; only the status decides.
		{"ended room", endedRoom},
		{"no live room in response", noLiveRoom},
		// liveRoom.status wins over user.status.
		{"statuses disagree", statusMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := respond(t, http.StatusOK, tt.body)

			room, err := c.FetchUserRoomByUsername(context.Background(), username)
			if err != nil {
				t.Fatalf("FetchUserRoomByUsername: %v", err)
			}
			if room != nil {
				t.Errorf("want nil room, got %+v", *room)
			}
		})
	}
}

// Anything other than a confirmed ended room must be an error.
func TestAmbiguousAnswerIsNeverOffline(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"captcha", http.StatusOK, captchaPage},
		{"empty body", http.StatusOK, ""},
		{"forbidden", http.StatusForbidden, "denied"},
		{"rate limited", http.StatusTooManyRequests, ""},
		{"bad gateway", http.StatusBadGateway, "<html>502</html>"},
		{"api error", http.StatusOK, apiError},
		{"missing user", http.StatusOK, missingUser},
		{"missing status", http.StatusOK, missingStatus},
		{"unknown status", http.StatusOK, unknownStatus},
		{"live without room id", http.StatusOK, liveNoRoomID},
		{"live without live room", http.StatusOK, liveNoLiveRoom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := respond(t, tt.status, tt.body)

			room, err := c.FetchUserRoomByUsername(context.Background(), username)
			if err == nil {
				t.Fatalf("got (%v, nil), want an error", room)
			}
			if room != nil {
				t.Errorf("got room %+v alongside error", *room)
			}
		})
	}
}

func TestAPIErrorCarriesStatusPathAndBody(t *testing.T) {
	c := respond(t, http.StatusBadGateway, strings.Repeat("x", 5000))

	_, err := c.FetchUserRoomByUsername(context.Background(), username)

	var apiErr *tiktok.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("want *APIError 502, got %v", err)
	}
	if apiErr.Path != "/api-live/user/room/" {
		t.Errorf("path = %q, want /api-live/user/room/", apiErr.Path)
	}
	if len(apiErr.Body) != 1<<10 {
		t.Errorf("body length = %d, want %d (bounded prefix)", len(apiErr.Body), 1<<10)
	}
}

// The remaining tests cover the ways a request can fail to complete at all.

func TestTimeoutAborts(t *testing.T) {
	c := newClient(t, blockUntilDone(t), tiktok.WithTimeout(50*time.Millisecond))

	if _, err := c.FetchUserRoomByUsername(context.Background(), username); err == nil {
		t.Fatal("want a timeout error, got nil")
	}
}

func TestCancelledContextAborts(t *testing.T) {
	c := newClient(t, blockUntilDone(t))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.FetchUserRoomByUsername(ctx, username)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func blockUntilDone(t *testing.T) http.HandlerFunc {
	t.Helper()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	return func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
}
