package config

import (
	"encoding/json"
	"fmt"
)

// Inbound modes for an assembled router config.
const (
	InboundTun      = "tun"      // transparent, captures default route (often conflicts with router VPN routing)
	InboundSocks    = "socks"    // mixed SOCKS+HTTP proxy on a port; no routing capture
	InboundTProxy   = "tproxy"   // transparent via TPROXY + iptables (TCP+UDP), selective
	InboundRedirect = "redirect" // transparent via nat REDIRECT + iptables (TCP only), selective
)

// TunOptions controls the tun inbound of an assembled router config.
type TunOptions struct {
	Address string // e.g. "172.19.0.1/30"
	MTU     int    // e.g. 1380
	Stack   string // "gvisor" (default), "system", "mixed"
}

// AssembleOptions bundles everything needed to build a full router config.
type AssembleOptions struct {
	DefaultOptions
	// InboundMode selects how clients' traffic reaches sing-box: tun, socks
	// (mixed proxy), tproxy or redirect. Defaults to socks (safest on routers
	// whose native routing conflicts with tun).
	InboundMode string
	// InboundPort is the listen port for socks/tproxy/redirect modes (default 2080).
	InboundPort int
	Tun         TunOptions

	// RouteDomains/RouteCIDR drive SELECTIVE routing in transparent modes
	// (tproxy/redirect): only traffic to these domains (matched by TLS/HTTP
	// sniffing) or destination CIDRs is sent through the proxy; everything else
	// egresses directly. Ignored in socks/tun modes, which route everything
	// through the proxy.
	RouteDomains []string
	RouteCIDR    []string
	// ExtraRouteDomains are domains from URL-based list sources. Matched via
	// sing-box domain_suffix rules (succinct trie — cheap, ~comparable to the
	// ip_cidr trie). Combined with RouteDomains at assembly time. This is the
	// stable, precise mechanism for CDN-fronted services (Cloudflare/AWS), whose
	// IPs rotate and are shared — so an ip_cidr snapshot can never be both clean
	// and complete; the hostname is the only reliable selector.
	ExtraRouteDomains []string
	// ExtraRouteCIDR are CIDRs from URL-based list sources. Matched via sing-box
	// ip_cidr rules (Patricia trie — negligible memory regardless of count).
	// Combined with RouteCIDR at assembly time.
	ExtraRouteCIDR []string

	// Multiplex enables h2mux on the proxy server outbounds (see settings.Multiplex).
	Multiplex bool
}

// ProxyOutbound is a single proxy server outbound (a sing-box outbound object,
// e.g. produced by share.Server.ToOutbound) together with a display tag.
type ProxyOutbound struct {
	Tag    string
	Object map[string]any
}

// Assemble builds a complete, service-managed sing-box router config: a tun
// inbound (gvisor stack by default — avoids the sing-tun system-stack panic),
// DNS, sniff + DNS-hijack route rules, clash_api, cache_file, and the given
// proxy servers grouped under a "proxy" selector with "direct" as a fallback.
// route.final points at the selector. The user only supplies the servers; the
// rest is owned here so the config is always valid.
func Assemble(opts AssembleOptions, servers []ProxyOutbound) ([]byte, error) {
	if opts.LogPath == "" {
		opts.LogPath = DefaultLogPath
	}
	if opts.CachePath == "" {
		opts.CachePath = DefaultCachePath
	}
	if opts.ClashAddr == "" {
		opts.ClashAddr = DefaultClashAddr
	}
	if opts.URLTestProbe == "" {
		opts.URLTestProbe = DefaultURLTestProbe
	}
	tun := opts.Tun
	if tun.Address == "" {
		tun.Address = "172.19.0.1/30"
	}
	if tun.MTU == 0 {
		tun.MTU = 1380
	}
	if tun.Stack == "" {
		tun.Stack = "gvisor"
	}
	if opts.InboundMode == "" {
		opts.InboundMode = InboundSocks
	}
	if opts.InboundPort == 0 {
		opts.InboundPort = 2080
	}

	// Build outbounds: each server, then selector + auto(urltest) + direct.
	outbounds := make([]any, 0, len(servers)+3)
	serverTags := make([]string, 0, len(servers))
	for _, s := range servers {
		obj := s.Object
		if obj == nil {
			continue
		}
		if s.Tag != "" {
			obj["tag"] = s.Tag
		}
		tag, _ := obj["tag"].(string)
		if tag == "" {
			continue
		}
		if opts.Multiplex {
			// Collapse many short-lived proxy connections (e.g. Telegram's DC
			// fan-out) onto a few persistent tunnels, removing the per-connection
			// TLS handshake through the proxy chain. Server must accept sing-box mux.
			obj["multiplex"] = map[string]any{
				"enabled":         true,
				"protocol":        "h2mux",
				"max_connections": 4,
				"padding":         false,
			}
		}
		serverTags = append(serverTags, tag)
		outbounds = append(outbounds, obj)
	}

	// proxy selector lists the servers (+auto if >1) and direct.
	selectorList := append([]string{}, serverTags...)
	if len(serverTags) > 1 {
		selectorList = append([]string{OutboundAutoTag}, selectorList...)
		outbounds = append(outbounds, map[string]any{
			"type":      "urltest",
			"tag":       OutboundAutoTag,
			"outbounds": serverTags,
			"url":       opts.URLTestProbe,
			"interval":  "5m",
		})
	}
	selectorList = append(selectorList, OutboundDirectTag)

	defaultPick := OutboundDirectTag
	if len(serverTags) > 1 {
		defaultPick = OutboundAutoTag
	} else if len(serverTags) == 1 {
		defaultPick = serverTags[0]
	}

	outbounds = append(outbounds,
		map[string]any{
			"type":      "selector",
			"tag":       OutboundProxyTag,
			"outbounds": selectorList,
			"default":   defaultPick,
		},
		map[string]any{"type": "direct", "tag": OutboundDirectTag},
	)

	clashAPI := map[string]any{"external_controller": opts.ClashAddr}
	if opts.ClashSecret != "" {
		clashAPI["secret"] = opts.ClashSecret
	}

	cfg := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"output":    opts.LogPath,
			"timestamp": true,
		},
		"dns":       dnsFor(opts),
		"inbounds":  inboundsFor(opts, tun),
		"outbounds": outbounds,
		"route": map[string]any{
			"rules":                   routeRulesFor(opts),
			"final":                   routeFinalFor(opts.InboundMode),
			"auto_detect_interface":   true,
			"default_domain_resolver": map[string]any{"server": "local"},
		},
		"experimental": map[string]any{
			"clash_api": clashAPI,
			"cache_file": map[string]any{
				"enabled": true,
				"path":    opts.CachePath,
			},
		},
	}

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return body, nil
}

// dnsFor builds the dns block. Plain resolver for every mode (no FakeIP).
func dnsFor(opts AssembleOptions) map[string]any {
	// Plain resolver for every mode. Transparent modes (tproxy/redirect) select
	// at the iptables layer against the route ipset and do NOT hijack client DNS,
	// so clients keep using the router/ISP resolver directly (fast, geo-correct).
	return map[string]any{
		"servers": []map[string]any{
			{"type": "tls", "tag": "google", "server": "8.8.8.8"},
			{"type": "local", "tag": "local"},
		},
		"strategy": "ipv4_only",
	}
}

// inboundsFor builds the inbound list for the selected mode.
func inboundsFor(opts AssembleOptions, tun TunOptions) []any {
	switch opts.InboundMode {
	case InboundSocks:
		// mixed = SOCKS + HTTP on one port. No routing capture, so it never
		// conflicts with the router's own VPN/policy routing.
		return []any{
			map[string]any{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "0.0.0.0",
				"listen_port": opts.InboundPort,
			},
		}
	case InboundTProxy:
		// Transparent TPROXY target (TCP+UDP); the firewall engine installs the
		// iptables mangle rules that send selected traffic here.
		return []any{
			map[string]any{
				"type":        "tproxy",
				"tag":         "tproxy-in",
				"listen":      "0.0.0.0",
				"listen_port": opts.InboundPort,
			},
		}
	case InboundRedirect:
		// Transparent REDIRECT target (TCP only); the firewall engine installs
		// the iptables nat REDIRECT rules that send selected traffic here.
		return []any{
			map[string]any{
				"type":        "redirect",
				"tag":         "redirect-in",
				"listen":      "0.0.0.0",
				"listen_port": opts.InboundPort,
			},
		}
	default: // InboundTun
		return []any{
			map[string]any{
				"type":         "tun",
				"tag":          "tun-in",
				"address":      []string{tun.Address},
				"mtu":          tun.MTU,
				"auto_route":   true,
				"strict_route": false,
				"stack":        tun.Stack,
			},
		}
	}
}

// transparentInboundTag returns the inbound tag used by a transparent mode,
// matching the tags set in inboundsFor.
func transparentInboundTag(mode string) string {
	switch mode {
	case InboundTProxy:
		return "tproxy-in"
	case InboundRedirect:
		return "redirect-in"
	}
	return ""
}

// isTransparent reports whether the mode transparently captures real client
// traffic (as opposed to socks, an explicit proxy clients opt into).
func isTransparent(mode string) bool {
	return mode == InboundTProxy || mode == InboundRedirect || mode == InboundTun
}

// routeFinalFor picks the catch-all outbound. Every mode now sends final=proxy:
//   - redirect/tproxy: SELECTIVE happens at the iptables layer (the route ipset),
//     so sing-box only ever receives traffic that should be proxied.
//   - socks/tun (full tunnel): everything is proxied by definition.
func routeFinalFor(mode string) string {
	return OutboundProxyTag
}

// routeRulesFor returns route rules tuned per inbound mode.
//
//   - All modes: sniff (to recover TLS SNI / HTTP host) + DNS hijack.
//   - Transparent capture (tun/tproxy/redirect): bypass private ranges. QUIC is
//     rejected ONLY in redirect mode (TCP-only) to force a sniffable TCP/TLS
//     fallback; tproxy/tun capture UDP, so QUIC is proxied normally.
//   - tproxy AND redirect are SELECTIVE at the iptables layer (the route ipset,
//     kept current by the DNS resolver), so neither carries domain/ip_cidr rules
//     here — sing-box proxies what it receives (final=proxy). This keeps direct
//     traffic out of sing-box userspace entirely (no relay/DNS-hijack overhead).
func routeRulesFor(opts AssembleOptions) []map[string]any {
	mode := opts.InboundMode
	rules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
	}
	if isTransparent(mode) {
		// Loop guard (tproxy/redirect): a connection whose destination port is
		// the transparent inbound's own port can only be a self-connection
		// (e.g. a health probe dialing the listen port, or a stray direct-to-
		// router:port). Legitimate captured traffic always carries the real
		// site's port. Without this, such a connection routes to direct, which
		// dials the inbound port again — an amplifying redirect loop that
		// crashes sing-box. Reject it early.
		if tag := transparentInboundTag(mode); tag != "" && opts.InboundPort > 0 {
			rules = append(rules, map[string]any{
				"inbound": []string{tag},
				"port":    opts.InboundPort,
				"action":  "reject",
			})
		}
		rules = append(rules, map[string]any{"ip_is_private": true, "outbound": OutboundDirectTag})
		// QUIC reject only for redirect (TCP-only): UDP/443 can't be proxied
		// there, so reject it to push browsers onto the redirected TCP path.
		// tproxy (and tun) capture UDP, so QUIC is proxied — no reject.
		if mode == InboundRedirect {
			rules = append(rules, map[string]any{"protocol": "quic", "action": "reject"})
		}
	}
	return rules
}
