package clash

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Proxy reverse-proxies UI requests under a path prefix to the sing-box
// Clash API (default 127.0.0.1:9090). The Clash secret is injected as a
// Bearer token on the backend side so it never reaches the browser.
type Proxy struct {
	prefix string
	rp     *httputil.ReverseProxy
}

// New builds a reverse proxy. addr is the Clash external_controller, e.g.
// "127.0.0.1:9090". secret is the clash_api secret. prefix is the UI path
// prefix to strip, e.g. "/api/clash".
func New(addr, secret, prefix string) (*Proxy, error) {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	target, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	rp := &httputil.ReverseProxy{
		// FlushInterval -1 streams responses immediately, which the Clash
		// /traffic and /logs endpoints rely on (newline-delimited JSON).
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Strip the UI prefix so /api/clash/traffic -> /traffic.
			p := strings.TrimPrefix(pr.In.URL.Path, prefix)
			if p == "" {
				p = "/"
			}
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			pr.Out.URL.Path = p
			pr.Out.URL.RawPath = ""
			if secret != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+secret)
			}
			// Drop any inbound auth/cookies so the browser session can't
			// leak into the Clash backend.
			pr.Out.Header.Del("Cookie")
		},
	}

	return &Proxy{prefix: prefix, rp: rp}, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}
