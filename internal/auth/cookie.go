package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
)

func mac(value, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(value))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func Sign(value, secret string) string {
	return url.QueryEscape(value + "." + mac(value, secret))
}

func Unsign(raw, secret string) (string, bool) {
	s, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	i := strings.LastIndex(s, ".")
	if i < 1 {
		return "", false
	}
	value, sig := s[:i], s[i+1:]
	return value, hmac.Equal([]byte(sig), []byte(mac(value, secret)))
}

func CookieName(prefix string, secure bool, name string) string {
	n := prefix + "." + name
	if secure {
		n = "__Secure-" + n
	}
	return n
}
