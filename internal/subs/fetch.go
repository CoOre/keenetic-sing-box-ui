package subs

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

// maxBody caps a subscription response; real ones are a few KiB.
const maxBody = 2 << 20

// Fetched is the parsed result of one subscription download.
type Fetched struct {
	Servers []share.Server
	Skipped int
	Info    *UserInfo
	Title   string // profile-title header, if the provider sent one
}

// Fetch downloads url with client and parses the body.
func Fetch(ctx context.Context, client *http.Client, url, userAgent string) (*Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	servers, skipped, err := ParseBody(body)
	if err != nil {
		return nil, &parseError{err}
	}
	return &Fetched{
		Servers: servers,
		Skipped: skipped,
		Info:    parseUserInfo(resp.Header.Get("Subscription-Userinfo")),
		Title:   decodeTitle(resp.Header.Get("Profile-Title")),
	}, nil
}

// parseError marks a response that arrived but couldn't be parsed.
type parseError struct{ err error }

func (e *parseError) Error() string { return e.err.Error() }
func (e *parseError) Unwrap() error { return e.err }

// ParseBody extracts servers from a subscription body: share links one per
// line, either as plain text or base64-encoded as a whole. Links that fail to
// parse and provider placeholders (the "traffic left: 12 GB" pseudo-servers
// pointing at 0.0.0.0) are counted in skipped. An empty result is an error, so
// a broken response never wipes the subscription's servers.
func ParseBody(body []byte) (servers []share.Server, skipped int, err error) {
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if text == "" {
		return nil, 0, errors.New("пустой ответ")
	}
	if !strings.Contains(text, "://") {
		dec, derr := decodeBase64(text)
		if derr != nil {
			return nil, 0, unsupportedFormat(text)
		}
		text = string(dec)
	}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "://") {
			continue
		}
		s, perr := share.ParseLink(line)
		if perr != nil || isPlaceholder(s) || s.Validate() != nil {
			skipped++
			continue
		}
		servers = append(servers, *s)
	}
	if len(servers) == 0 {
		if skipped > 0 {
			return nil, skipped, fmt.Errorf("ни одна из %d ссылок не распознана (поддерживаются vless, trojan, ss, vmess, hysteria2, tuic)", skipped)
		}
		return nil, 0, unsupportedFormat(text)
	}
	return servers, skipped, nil
}

func unsupportedFormat(text string) error {
	switch {
	case strings.HasPrefix(text, "{"), strings.HasPrefix(text, "["):
		return errors.New("провайдер отдал JSON-конфиг вместо списка ссылок — укажите другой User-Agent (например, v2rayN) или ссылку на формат v2ray/base64")
	case strings.Contains(text, "proxies:"):
		return errors.New("провайдер отдал Clash YAML вместо списка ссылок — укажите другой User-Agent (например, v2rayN) или ссылку на формат v2ray/base64")
	case strings.HasPrefix(strings.ToLower(text), "<!doctype"), strings.HasPrefix(strings.ToLower(text), "<html"):
		return errors.New("по ссылке HTML-страница, а не подписка")
	}
	return errors.New("не удалось разобрать ответ: ожидается список share-ссылок (plain или base64)")
}

// decodeBase64 accepts std/url alphabets, with or without padding, and
// ignores line breaks inside the blob.
func decodeBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// isPlaceholder reports provider pseudo-servers that only carry a label
// (remaining traffic, expiry date, "renew your plan") and can't be dialled.
func isPlaceholder(s *share.Server) bool {
	if s.ServerPort == 1 {
		return true
	}
	host := strings.Trim(strings.ToLower(s.Server), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsUnspecified() || ip.IsLoopback())
}

// parseUserInfo parses "upload=1; download=2; total=3; expire=4".
func parseUserInfo(h string) *UserInfo {
	if h == "" {
		return nil
	}
	var ui UserInfo
	found := false
	for part := range strings.SplitSeq(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		found = true
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			ui.Upload = int64(n)
		case "download":
			ui.Download = int64(n)
		case "total":
			ui.Total = int64(n)
		case "expire":
			ui.Expire = int64(n)
		}
	}
	if !found {
		return nil
	}
	return &ui
}

// decodeTitle handles the "base64:..." form some panels use for non-ASCII
// profile titles.
func decodeTitle(h string) string {
	h = strings.TrimSpace(h)
	if rest, ok := strings.CutPrefix(h, "base64:"); ok {
		if b, err := decodeBase64(rest); err == nil {
			return strings.TrimSpace(string(bytes.ToValidUTF8(b, nil)))
		}
		return ""
	}
	return h
}
