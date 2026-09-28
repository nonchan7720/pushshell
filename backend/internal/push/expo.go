package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultExpoEndpoint は Expo Push API のエンドポイント。
const DefaultExpoEndpoint = "https://exp.host/--/api/v2/push/send"

// expoBatchSize は Expo Push API の 1 リクエスト上限。
const expoBatchSize = 100

// ExpoSender は Expo Push API 経由で送信する Sender。
type ExpoSender struct {
	// Endpoint は省略時 DefaultExpoEndpoint。
	Endpoint string
	// AccessToken は Expo のアクセストークン (任意。設定すると Bearer で送る)。
	AccessToken string
	// HTTPClient は省略時 10 秒タイムアウトのクライアント。
	HTTPClient *http.Client
}

type expoMessage struct {
	To        string         `json:"to"`
	Title     string         `json:"title,omitempty"`
	Body      string         `json:"body,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Sound     string         `json:"sound,omitempty"`
	Badge     *int           `json:"badge,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
}

type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details *struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

type expoResponse struct {
	Data   []expoTicket `json:"data"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// Send implements Sender.
func (s *ExpoSender) Send(ctx context.Context, messages []Message) ([]Result, error) {
	results := make([]Result, 0, len(messages))
	for start := 0; start < len(messages); start += expoBatchSize {
		end := min(start+expoBatchSize, len(messages))
		batch, err := s.sendBatch(ctx, messages[start:end])
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}

// expoTarget は expoToken を持つメッセージを、元の messages 内でのインデックス
// と一緒に保持する (トークンなしのメッセージを送信対象から除きつつ、結果の
// 順序を保つため)。
type expoTarget struct {
	idx int
	msg Message
}

func (s *ExpoSender) sendBatch(ctx context.Context, messages []Message) ([]Result, error) {
	results := make([]Result, len(messages))
	targets := make([]expoTarget, 0, len(messages))
	for i, m := range messages {
		if m.ExpoToken == "" {
			results[i] = Result{Error: "device has no expo push token"}
			continue
		}
		targets = append(targets, expoTarget{idx: i, msg: m})
	}
	if len(targets) == 0 {
		return results, nil
	}

	payload := make([]expoMessage, len(targets))
	for i, t := range targets {
		payload[i] = expoMessage{
			To:        t.msg.ExpoToken,
			Title:     t.msg.Title,
			Body:      t.msg.Body,
			Data:      t.msg.Data,
			Sound:     t.msg.Sound,
			Badge:     t.msg.Badge,
			ChannelID: t.msg.ChannelID,
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("expo: marshal: %w", err)
	}

	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = DefaultExpoEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("expo: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if s.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	}

	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("expo: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("expo: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("expo: unexpected status %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	var parsed expoResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("expo: decode response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("expo: %s: %s", parsed.Errors[0].Code, parsed.Errors[0].Message)
	}
	if len(parsed.Data) != len(targets) {
		return nil, fmt.Errorf("expo: ticket count mismatch: got %d want %d", len(parsed.Data), len(targets))
	}

	for i, t := range parsed.Data {
		idx := targets[i].idx
		if t.Status == "ok" {
			results[idx] = Result{OK: true}
			continue
		}
		r := Result{Error: t.Message}
		if t.Details != nil {
			if t.Details.Error != "" {
				r.Error = t.Details.Error + ": " + t.Message
			}
			r.Unregistered = t.Details.Error == "DeviceNotRegistered"
		}
		results[idx] = r
	}
	return results, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
