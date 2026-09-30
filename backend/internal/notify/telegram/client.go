// Package telegram — минимальный клиент Bot API: long polling, отправка
// сообщений с инлайн-кнопками, ответ на нажатие кнопки.
//
// Токен бота входит в URL каждого запроса, поэтому ни URL, ни исходная ошибка
// net/http наружу не отдаются: errors несут только метод и описание от Telegram.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config — настройки бота из окружения.
type Config struct {
	Token       string
	BotUsername string
	APIBaseURL  string
	ProxyURL    string
	AppBaseURL  string
}

// ConfigFromEnv читает TELEGRAM_BOT_TOKEN, TELEGRAM_BOT_USERNAME,
// TELEGRAM_API_BASE_URL, TELEGRAM_PROXY_URL и APP_BASE_URL.
func ConfigFromEnv() Config {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE_URL")), "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return Config{
		Token:       strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		BotUsername: strings.TrimPrefix(strings.TrimSpace(os.Getenv("TELEGRAM_BOT_USERNAME")), "@"),
		APIBaseURL:  base,
		ProxyURL:    strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_URL")),
		AppBaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/"),
	}
}

// Enabled — бот настроен. Без токена поллер и отправка не запускаются.
func (c Config) Enabled() bool { return c.Token != "" && c.BotUsername != "" }

// APIError — ответ Telegram с ok=false.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Permanent — повтор не поможет: бот заблокирован, чат не найден, запрос неверен.
func (e *APIError) Permanent() bool { return e.Code == 400 || e.Code == 403 }

// ErrTransport — сетевая ошибка без подробностей (подробности содержат URL с токеном).
var ErrTransport = errors.New("telegram: сетевая ошибка")

type Client struct {
	base string
	http *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.ProxyURL != "" {
		u, err := url.Parse(cfg.ProxyURL)
		if err != nil || u.Host == "" {
			return nil, errors.New("telegram: TELEGRAM_PROXY_URL некорректен")
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &Client{
		base: cfg.APIBaseURL + "/bot" + cfg.Token + "/",
		http: &http.Client{Transport: tr, Timeout: 60 * time.Second},
	}, nil
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram %s: marshal: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, ErrTransport)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("telegram %s: %w", method, ErrTransport)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, ответ не разобран", method, resp.StatusCode)
	}
	if !env.OK {
		apiErr := &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
		if env.Parameters != nil {
			apiErr.RetryAfter = env.Parameters.RetryAfter
		}
		return apiErr
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram %s: result: %w", method, err)
		}
	}
	return nil
}

// Update — нужные боту поля входящего обновления.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from"`
	Text      string `json:"text"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// InlineButton — кнопка с callback_data (≤ 64 байт) или ссылкой.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// GetUpdates — long polling; timeout в секундах держит соединение открытым.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

// SendMessage отправляет HTML-сообщение; keyboard может быть nil.
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string, keyboard [][]InlineButton) (int64, error) {
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if len(keyboard) > 0 {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	var msg Message
	if err := c.call(ctx, "sendMessage", body, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// AnswerCallback закрывает «часики» на кнопке и показывает короткий текст.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	}, nil)
}
