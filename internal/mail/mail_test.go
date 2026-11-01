package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var _ Sender = (*Mailer)(nil)

func TestSend(t *testing.T) {
	var gotAuth, gotPath, gotCT string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotCT = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("Content-Type")
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("body: %v", err)
		}
		w.Write([]byte(`{"id":"e1"}`))
	}))
	defer srv.Close()

	m := &Mailer{APIKey: "re_key", From: "Hectix <no-reply@hectix.id>", BaseURL: srv.URL}
	pdf := []byte("%PDF-1.4\x00\xff")
	err := m.Send(context.Background(), "b@example.com", "Subj", "<p>h</p>", "t",
		Attachment{Filename: "t.pdf", Content: pdf, ContentType: "application/pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer re_key" || gotPath != "/emails" || !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("auth=%q path=%q ct=%q", gotAuth, gotPath, gotCT)
	}
	if body["from"] != "Hectix <no-reply@hectix.id>" || body["subject"] != "Subj" || body["html"] != "<p>h</p>" || body["text"] != "t" {
		t.Fatalf("body %v", body)
	}
	to, _ := body["to"].([]any)
	if len(to) != 1 || to[0] != "b@example.com" {
		t.Fatalf("to %v", body["to"])
	}
	atts, _ := body["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments %v", body["attachments"])
	}
	a := atts[0].(map[string]any)
	if a["filename"] != "t.pdf" || a["content"] != base64.StdEncoding.EncodeToString(pdf) || a["content_type"] != "application/pdf" {
		t.Fatalf("attachment %v", a)
	}
}

func TestSendNoAttachmentsOmitsField(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"id":"e1"}`))
	}))
	defer srv.Close()
	m := &Mailer{APIKey: "k", From: "f", BaseURL: srv.URL}
	if err := m.Send(context.Background(), "a@b.c", "s", "h", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["attachments"]; ok {
		t.Fatalf("attachments present: %v", body)
	}
	if _, ok := body["text"]; ok {
		t.Fatalf("empty text should be omitted: %v", body)
	}
}

func TestSendRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"name":"validation_error","message":"Invalid to field"}`))
	}))
	defer srv.Close()
	m := &Mailer{APIKey: "k", From: "f", BaseURL: srv.URL}
	err := m.Send(context.Background(), "bad", "s", "h", "t")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"Resend rejected the send", "422", "Invalid to field"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestSendRejectedBodyTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(strings.Repeat("x", 10000)))
	}))
	defer srv.Close()
	m := &Mailer{APIKey: "k", From: "f", BaseURL: srv.URL}
	err := m.Send(context.Background(), "a@b.c", "s", "h", "t")
	if err == nil || strings.Count(err.Error(), "x") > 2048+5 {
		t.Fatalf("err len %d", len(err.Error()))
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if got := (&Mailer{}).baseURL(); got != "https://api.resend.com" {
		t.Fatal(got)
	}
}
