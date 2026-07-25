package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type telegramResponse[T any] struct {
	OK          bool                `json:"ok"`
	Result      T                   `json:"result"`
	Description string              `json:"description"`
	ErrorCode   int                 `json:"error_code"`
	Parameters  *responseParameters `json:"parameters"`
}

type responseParameters struct {
	RetryAfter      int   `json:"retry_after"`
	MigrateToChatID int64 `json:"migrate_to_chat_id"`
}

type user struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type message struct {
	MessageId int `json:"message_id"`
}

type ChatAdministratorRights struct {
	CanManageChat   bool `json:"can_manage_chat"`
	CanPostMessages bool `json:"can_post_messages"`
	CanEditMessages bool `json:"can_edit_messages"`
	CanPinMessages  bool `json:"can_pin_messages"`
}

type TelegramClient struct {
	botToken   string
	httpClient *http.Client
}

func NewTelegramClient(botToken string) *TelegramClient {
	return &TelegramClient{
		botToken:   botToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (tc *TelegramClient) telegramBaseURL() url.URL {
	return url.URL{
		Scheme: "https",
		Host:   "api.telegram.org",
		Path:   "bot" + tc.botToken,
	}
}

func callTelegram[T any](ctx context.Context, tc *TelegramClient, method string, params url.Values, result *telegramResponse[T]) error {
	base := tc.telegramBaseURL()
	u, err := url.JoinPath(base.String(), method)
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

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK || !result.OK {
		s := fmt.Sprintf("telegram %s: %d ", method, resp.StatusCode)
		if result.Description != "" {
			s += result.Description
		} else {
			s += http.StatusText(resp.StatusCode)
		}
		if result.ErrorCode != 0 {
			s += strconv.Itoa(result.ErrorCode)
		}
		return errors.New(s)
	}

	return nil
}

func (tc *TelegramClient) GetMe(ctx context.Context) error {
	var resp telegramResponse[user]

	if err := callTelegram(ctx, tc, "getMe", url.Values{}, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) SendMessage(ctx context.Context, chatID, text string) (int, error) {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("text", text)

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "sendMessage", params, &resp); err != nil {
		return 0, err
	}

	return resp.Result.MessageId, nil
}

func (tc *TelegramClient) SendHTMLMessage(ctx context.Context, chatID, text string) (int, error) {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("text", text)
	params.Set("parse_mode", "HTML")

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "sendMessage", params, &resp); err != nil {
		return 0, err
	}

	return resp.Result.MessageId, nil
}

func (tc *TelegramClient) EditMessageText(ctx context.Context, chatID, messageId, text string) error {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("message_id", messageId)
	params.Set("text", text)

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "editMessageText", params, &resp); err != nil {
		return err
	}

	return nil

}

func (tc *TelegramClient) EditHTMLMessageText(ctx context.Context, chatID, messageId, text string) error {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("message_id", messageId)
	params.Set("text", text)
	params.Set("parse_mode", "HTML")

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "editMessageText", params, &resp); err != nil {
		return err
	}

	return nil

}

func (tc *TelegramClient) PinChatMessage(ctx context.Context, chatID, messageId string) error {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("message_id", messageId)

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "pinChatMessage", params, &resp); err != nil {
		return err
	}

	if !resp.Result {
		return fmt.Errorf("pinChatMessage: telegram returned false")
	}

	return nil

}

func (tc *TelegramClient) UnpinChatMessage(ctx context.Context, chatID, messageId string) error {
	params := url.Values{}
	params.Set("chat_id", chatID)
	params.Set("message_id", messageId)

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "unpinChatMessage", params, &resp); err != nil {
		return err
	}

	if !resp.Result {
		return fmt.Errorf("unpinChatMessage: telegram returned false")
	}

	return nil

}

func (tc *TelegramClient) SetMyDefaultAdministratorRights(ctx context.Context, rights ChatAdministratorRights, forChannels bool) error {
	encoded, err := json.Marshal(rights)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Set("rights", string(encoded))
	params.Set("for_channels", strconv.FormatBool(forChannels))

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "setMyDefaultAdministratorRights", params, &resp); err != nil {
		return err
	}

	if !resp.Result {
		return fmt.Errorf("setMyDefaultAdministratorRights: telegram returned false")
	}

	return nil

}

func (tc *TelegramClient) GetMyDefaultAdministratorRights(ctx context.Context, forChannels bool) (ChatAdministratorRights, error) {
	params := url.Values{}
	params.Set("for_channels", strconv.FormatBool(forChannels))

	var resp telegramResponse[ChatAdministratorRights]

	if err := callTelegram(ctx, tc, "getMyDefaultAdministratorRights", params, &resp); err != nil {
		return ChatAdministratorRights{}, err
	}

	return resp.Result, nil

}
