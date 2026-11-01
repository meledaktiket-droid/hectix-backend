package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type midtrans struct {
	ServerKey string
	BaseURL   string
}

var midtransHTTP = &http.Client{Timeout: 20 * time.Second}

func (m *midtrans) Cancel(ctx context.Context, orderID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/v2/"+url.PathEscape(orderID)+"/cancel", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(m.ServerKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := midtransHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("midtrans cancel: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("midtrans cancel: HTTP %d: %s", resp.StatusCode, raw)
	}
	var res struct {
		StatusCode json.RawMessage `json:"status_code"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("midtrans cancel: decode: %w", err)
	}
	if len(res.StatusCode) > 0 {
		var s string
		if json.Unmarshal(res.StatusCode, &s) != nil {
			s = string(res.StatusCode)
		}
		if code, err := strconv.Atoi(s); err == nil && code >= 400 && code != 407 {
			return fmt.Errorf("midtrans cancel: status %d: %s", code, raw)
		}
	}
	return nil
}
