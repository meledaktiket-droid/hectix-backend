package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type App struct {
	Name    string
	Host    string
	BaseURL string
	Secure  bool
}

type Config struct {
	Addr               string
	DatabaseURL        string
	AuthSecret         string
	GoogleClientID     string
	GoogleClientSecret string
	MidtransServerKey  string
	MidtransProduction bool
	ResendAPIKey       string
	MailFrom           string
	UploadDir          string
	Apps               map[string]App
}

func Load() (Config, error) {
	c := Config{
		Addr:               env("ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		AuthSecret:         os.Getenv("AUTH_SECRET"),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		MidtransServerKey:  os.Getenv("MIDTRANS_SERVER_KEY"),
		MidtransProduction: os.Getenv("MIDTRANS_IS_PRODUCTION") == "true",
		ResendAPIKey:       os.Getenv("RESEND_API_KEY"),
		MailFrom:           env("MAIL_FROM", "Hectix <no-reply@hectix.id>"),
		UploadDir:          env("UPLOAD_DIR", "/var/hectix/uploads"),
		Apps:               map[string]App{},
	}
	for k, v := range map[string]string{
		"DATABASE_URL":         c.DatabaseURL,
		"AUTH_SECRET":   c.AuthSecret,
		"GOOGLE_CLIENT_ID":     c.GoogleClientID,
		"GOOGLE_CLIENT_SECRET": c.GoogleClientSecret,
		"MIDTRANS_SERVER_KEY":  c.MidtransServerKey,
		"RESEND_API_KEY":       c.ResendAPIKey,
	} {
		if v == "" {
			return c, fmt.Errorf("%s is required", k)
		}
	}
	for _, name := range []string{"web", "organizer", "admin"} {
		key := strings.ToUpper(name) + "_BASE_URL"
		base := strings.TrimRight(os.Getenv(key), "/")
		app, err := parseApp(name, key, base)
		if err != nil {
			return c, err
		}
		c.Apps[app.Host] = app
	}
	if base := strings.TrimRight(os.Getenv("SCANNER_BASE_URL"), "/"); base != "" {
		app, err := parseApp("scanner", "SCANNER_BASE_URL", base)
		if err != nil {
			return c, err
		}
		c.Apps[app.Host] = app
	}
	return c, nil
}

func parseApp(name, key, base string) (App, error) {
	u, err := url.Parse(base)
	if base == "" || err != nil || u.Host == "" {
		return App{}, fmt.Errorf("%s must be an absolute URL", key)
	}
	return App{Name: name, Host: u.Host, BaseURL: base, Secure: u.Scheme == "https"}, nil
}

func (c Config) AppByName(name string) (App, bool) {
	for _, app := range c.Apps {
		if app.Name == name {
			return app, true
		}
	}
	return App{}, false
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
