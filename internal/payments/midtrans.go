package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Midtrans struct {
	ServerKey string
	BaseURL   string
}

func MidtransBaseURL(production bool) string {
	if production {
		return "https://api.midtrans.com"
	}
	return "https://api.sandbox.midtrans.com"
}

var midtransHTTP = &http.Client{Timeout: 20 * time.Second}

type chargeResponse struct {
	StatusCode    string `json:"status_code"`
	StatusMessage string `json:"status_message"`
	BillKey       string `json:"bill_key"`
	VANumbers     []struct {
		VANumber string `json:"va_number"`
	} `json:"va_numbers"`
	Actions []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"actions"`
}

func (m *Midtrans) Charge(ctx context.Context, orderID string, total int, method, bank string) (va, qris string, err error) {
	details := map[string]any{"order_id": orderID, "gross_amount": total}
	expiry := map[string]any{"expiry_duration": 15, "unit": "minute"}
	var body map[string]any
	switch {
	case method == "va_bank" && bank == "mandiri":
		body = map[string]any{
			"payment_type":        "echannel",
			"transaction_details": details,
			"echannel":            map[string]string{"bill_info1": "Payment:", "bill_info2": "Online purchase"},
			"custom_expiry":       expiry,
		}
	case method == "va_bank" && bank != "":
		body = map[string]any{
			"payment_type":        "bank_transfer",
			"transaction_details": details,
			"bank_transfer":       map[string]string{"bank": bank},
			"custom_expiry":       expiry,
		}
	case method == "qris":
		body = map[string]any{"payment_type": "gopay", "transaction_details": details}
	default:
		return "", "", fmt.Errorf("midtrans: unsupported payment method %q bank %q", method, bank)
	}

	res, err := m.post(ctx, body)
	if err != nil {
		return "", "", err
	}
	switch {
	case method == "qris":
		for _, a := range res.Actions {
			if a.Name == "generate-qr-code" && a.URL != "" {
				return "", a.URL, nil
			}
		}
		return "", "", errors.New("midtrans: no generate-qr-code action in response")
	case bank == "mandiri":
		if res.BillKey == "" {
			return "", "", errors.New("midtrans: no bill_key in response")
		}
		return res.BillKey, "", nil
	default:
		if len(res.VANumbers) == 0 || res.VANumbers[0].VANumber == "" {
			return "", "", errors.New("midtrans: no va_numbers in response")
		}
		return res.VANumbers[0].VANumber, "", nil
	}
}

func (m *Midtrans) post(ctx context.Context, body any) (*chargeResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/v2/charge", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(m.ServerKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := midtransHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("midtrans: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("midtrans: %w", err)
	}
	var res chargeResponse
	jsonErr := json.Unmarshal(raw, &res)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("midtrans: HTTP %d: %s", resp.StatusCode, res.StatusMessage)
	}
	if jsonErr != nil {
		return nil, fmt.Errorf("midtrans: decode response: %w", jsonErr)
	}
	if res.StatusCode != "200" && res.StatusCode != "201" {
		return nil, fmt.Errorf("midtrans: status %s: %s", res.StatusCode, res.StatusMessage)
	}
	return &res, nil
}
