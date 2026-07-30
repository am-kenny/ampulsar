package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type ParseMode string

const (
	ParsePlainText ParseMode = ""
	ParseHTML      ParseMode = "HTML"
	ParseMarkdown  ParseMode = "MarkdownV2"
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

type User struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type message struct {
	MessageID int `json:"message_id"`
}

type ChatAdministratorRights struct {
	CanManageChat   bool `json:"can_manage_chat"`
	CanPostMessages bool `json:"can_post_messages"`
	CanEditMessages bool `json:"can_edit_messages"`
	CanPinMessages  bool `json:"can_pin_messages"`
}

type sendMessageRequest struct {
	ChatID    string    `json:"chat_id"`
	Text      string    `json:"text"`
	ParseMode ParseMode `json:"parse_mode,omitempty"`
}

type editMessageTextRequest struct {
	ChatID    string    `json:"chat_id"`
	MessageID int       `json:"message_id"`
	Text      string    `json:"text"`
	ParseMode ParseMode `json:"parse_mode,omitempty"`
}

type chatMessageRequest struct {
	ChatID    string `json:"chat_id"`
	MessageID int    `json:"message_id"`
}

type setRightsRequest struct {
	Rights      ChatAdministratorRights `json:"rights"`
	ForChannels bool                    `json:"for_channels"`
}

type forChannelsRequest struct {
	ForChannels bool `json:"for_channels"`
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

// callTelegram performs a Telegram API call with method POST and JSON body
// This is a function and not a method to allow type parameter
func callTelegram[T any](ctx context.Context, tc *TelegramClient, method string, body any, result *telegramResponse[T]) error {
	u := url.URL{
		Scheme: "https",
		Host:   "api.telegram.org",
		Path:   "/bot" + tc.botToken + "/" + method,
	}

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal %s request: %w", method, err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), reqBody)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := tc.httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: reading body: %w", method, err)
	}

	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("telegram %s: %d %s: non-JSON body: %q",
			method, resp.StatusCode, http.StatusText(resp.StatusCode),
			bytes.TrimSpace(raw[:min(len(raw), 256)]))
	}

	if resp.StatusCode != http.StatusOK || !result.OK {
		return &TelegramAPIError{
			Method:      method,
			StatusCode:  resp.StatusCode,
			Description: result.Description,
			ErrorCode:   result.ErrorCode,
			parameters:  result.Parameters,
		}
	}

	return nil
}

func (tc *TelegramClient) GetMe(ctx context.Context) (User, error) {
	var resp telegramResponse[User]

	if err := callTelegram(ctx, tc, "getMe", nil, &resp); err != nil {
		return User{}, err
	}

	return resp.Result, nil
}

func (tc *TelegramClient) SendMessage(ctx context.Context, chatID, text string, parseMode ParseMode) (int, error) {
	body := sendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: parseMode,
	}

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "sendMessage", body, &resp); err != nil {
		return 0, err
	}

	return resp.Result.MessageID, nil
}

func (tc *TelegramClient) SendHTMLMessage(ctx context.Context, chatID, text string) (int, error) {
	return tc.SendMessage(ctx, chatID, text, ParseHTML)
}

func (tc *TelegramClient) EditMessageText(ctx context.Context, chatID string, messageId int, text string, parseMode ParseMode) error {
	body := editMessageTextRequest{
		ChatID:    chatID,
		MessageID: messageId,
		Text:      text,
		ParseMode: parseMode,
	}

	var resp telegramResponse[message]

	if err := callTelegram(ctx, tc, "editMessageText", body, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) EditHTMLMessageText(ctx context.Context, chatID string, messageId int, text string) error {
	return tc.EditMessageText(ctx, chatID, messageId, text, ParseHTML)
}

func (tc *TelegramClient) DeleteMessage(ctx context.Context, chatID string, messageId int) error {
	body := chatMessageRequest{
		ChatID:    chatID,
		MessageID: messageId,
	}

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "deleteMessage", body, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) PinChatMessage(ctx context.Context, chatID string, messageId int) error {
	body := chatMessageRequest{
		ChatID:    chatID,
		MessageID: messageId,
	}

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "pinChatMessage", body, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) UnpinChatMessage(ctx context.Context, chatID string, messageId int) error {
	body := chatMessageRequest{
		ChatID:    chatID,
		MessageID: messageId,
	}

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "unpinChatMessage", body, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) SetMyDefaultAdministratorRights(ctx context.Context, rights ChatAdministratorRights, forChannels bool) error {
	body := setRightsRequest{
		Rights:      rights,
		ForChannels: forChannels,
	}

	var resp telegramResponse[bool]

	if err := callTelegram(ctx, tc, "setMyDefaultAdministratorRights", body, &resp); err != nil {
		return err
	}

	return nil
}

func (tc *TelegramClient) GetMyDefaultAdministratorRights(ctx context.Context, forChannels bool) (ChatAdministratorRights, error) {
	body := forChannelsRequest{
		ForChannels: forChannels,
	}

	var resp telegramResponse[ChatAdministratorRights]

	if err := callTelegram(ctx, tc, "getMyDefaultAdministratorRights", body, &resp); err != nil {
		return ChatAdministratorRights{}, err
	}

	return resp.Result, nil
}

type TelegramAPIError struct {
	Method      string
	StatusCode  int
	Description string
	ErrorCode   int
	parameters  *responseParameters
}

func (e *TelegramAPIError) Error() string {
	s := fmt.Sprintf("telegram %s: %d ", e.Method, e.StatusCode)
	if e.Description != "" {
		s += e.Description
	} else {
		s += http.StatusText(e.StatusCode)
	}
	if e.ErrorCode != 0 && e.ErrorCode != e.StatusCode {
		s += fmt.Sprintf(" (error_code %d)", e.ErrorCode)
	}
	return s
}

func (e *TelegramAPIError) RetryAfter() time.Duration {
	if e.parameters != nil && e.parameters.RetryAfter > 0 {
		return time.Duration(e.parameters.RetryAfter) * time.Second
	}
	return 0
}

func (e *TelegramAPIError) MigrateToChatID() int64 {
	if e.parameters != nil {
		return e.parameters.MigrateToChatID
	}
	return 0
}
