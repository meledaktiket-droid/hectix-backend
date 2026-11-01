package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.resend.com"

type Attachment struct {
	Filename    string
	Content     []byte
	ContentType string
}

type Sender interface {
	Send(ctx context.Context, to, subject, html, text string, att ...Attachment) error
}

type Mailer struct {
	APIKey  string
	From    string
	BaseURL string
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

type apiAttachment struct {
	Filename    string `json:"filename"`
	Content     []byte `json:"content"`
	ContentType string `json:"content_type,omitempty"`
}

type apiEmail struct {
	From        string          `json:"from"`
	To          []string        `json:"to"`
	Subject     string          `json:"subject"`
	HTML        string          `json:"html"`
	Text        string          `json:"text,omitempty"`
	Attachments []apiAttachment `json:"attachments,omitempty"`
}

func (m *Mailer) baseURL() string {
	if m.BaseURL == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(m.BaseURL, "/")
}

func (m *Mailer) Send(ctx context.Context, to, subject, html, text string, att ...Attachment) error {
	payload := apiEmail{From: m.From, To: []string{to}, Subject: subject, HTML: html, Text: text}
	for _, a := range att {
		payload.Attachments = append(payload.Attachments, apiAttachment(a))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL()+"/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("Resend rejected the send: status %d: %s", res.StatusCode, msg)
	}
	io.Copy(io.Discard, res.Body)
	return nil
}
