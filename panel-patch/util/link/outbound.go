// Package link provides parsers for VPN share links and subscription bodies.
// Output matches the panel's Xray outbound wire format for direct injection.
package link

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// Outbound is the wire shape emitted for each parsed link.
type Outbound map[string]any

// ParseResult holds a parsed outbound plus a stable identity for tag reuse.
type ParseResult struct {
	Outbound Outbound
	Identity string
}

// ParseSubscriptionBody accepts raw subscription content and returns parsed
// outbounds. Supported body formats:
//   - base64 (std / url-safe) of newline-separated share links
//   - plain newline-separated share links
//   - JSON array of share-link strings
//   - JSON array / object of ready Xray outbound objects
//   - Clash / Clash Meta YAML with proxies:
func ParseSubscriptionBody(body []byte) ([]Outbound, []string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, nil, nil
	}

	// Prefer structured formats before treating as opaque base64.
	if obs, ids, ok := tryParseJSONSubscription(text); ok {
		obs, ids = sanitizeAll(obs, ids)
		return obs, ids, nil
	}
	if obs, ids, ok := tryParseClashProxies(text); ok {
		obs, ids = sanitizeAll(obs, ids)
		return obs, ids, nil
	}

	// Base64 blob of links (common provider encoding). Also try once more after decode.
	candidates := []string{text}
	if decoded, ok := tryBase64(text); ok {
		decoded = strings.TrimSpace(decoded)
		if decoded != "" && decoded != text {
			candidates = append([]string{decoded}, candidates...)
		}
	}

	for _, cand := range candidates {
		if obs, ids, ok := tryParseJSONSubscription(cand); ok {
			obs, ids = sanitizeAll(obs, ids)
			return obs, ids, nil
		}
		if obs, ids, ok := tryParseClashProxies(cand); ok {
			obs, ids = sanitizeAll(obs, ids)
			return obs, ids, nil
		}
		if obs, ids := parseLinkLines(cand); len(obs) > 0 {
			obs, ids = sanitizeAll(obs, ids)
			return obs, ids, nil
		}
	}
	return nil, nil, nil
}

func sanitizeAll(obs []Outbound, ids []string) ([]Outbound, []string) {
	for i := range obs {
		SanitizeOutboundHTTPHeaders(obs[i])
	}
	return obs, ids
}

func parseLinkLines(text string) ([]Outbound, []string) {
	var outbounds []Outbound
	var identities []string
	for _, ln := range splitLines(text) {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "//") {
			continue
		}
		// Some providers wrap links in quotes.
		ln = strings.Trim(ln, `"'`)
		res, err := ParseLink(ln)
		if err != nil || res == nil {
			continue
		}
		outbounds = append(outbounds, res.Outbound)
		identities = append(identities, res.Identity)
	}
	return outbounds, identities
}

func tryParseJSONSubscription(text string) ([]Outbound, []string, bool) {
	trim := strings.TrimSpace(text)
	if !strings.HasPrefix(trim, "[") && !strings.HasPrefix(trim, "{") {
		return nil, nil, false
	}

	// Array of share-link strings.
	var links []string
	if err := json.Unmarshal([]byte(trim), &links); err == nil && len(links) > 0 {
		var obs []Outbound
		var ids []string
		for _, ln := range links {
			res, err := ParseLink(strings.TrimSpace(ln))
			if err != nil || res == nil {
				continue
			}
			obs = append(obs, res.Outbound)
			ids = append(ids, res.Identity)
		}
		if len(obs) > 0 {
			return obs, ids, true
		}
	}

	// Array of outbound objects.
	var arr []map[string]any
	if err := json.Unmarshal([]byte(trim), &arr); err == nil && len(arr) > 0 {
		obs, ids := outboundsFromMaps(arr)
		if len(obs) > 0 {
			return obs, ids, true
		}
	}

	// Object wrappers: { "outbounds": [...] } or { "proxies": [...] }
	var top map[string]any
	if err := json.Unmarshal([]byte(trim), &top); err == nil {
		for _, key := range []string{"outbounds", "proxies", "nodes", "servers"} {
			raw, ok := top[key]
			if !ok {
				continue
			}
			b, _ := json.Marshal(raw)
			if obs, ids, ok2 := tryParseJSONSubscription(string(b)); ok2 {
				return obs, ids, true
			}
			// Clash-like proxies as JSON objects
			var proxies []map[string]any
			if json.Unmarshal(b, &proxies) == nil {
				if obs, ids := clashProxiesToOutbounds(proxies); len(obs) > 0 {
					return obs, ids, true
				}
			}
		}
	}
	return nil, nil, false
}

func outboundsFromMaps(arr []map[string]any) ([]Outbound, []string) {
	var obs []Outbound
	var ids []string
	for _, m := range arr {
		// Share link string embedded as {"link":"vless://..."}
		if linkStr, _ := m["link"].(string); linkStr != "" {
			if res, err := ParseLink(linkStr); err == nil && res != nil {
				obs = append(obs, res.Outbound)
				ids = append(ids, res.Identity)
				continue
			}
		}
		proto, _ := m["protocol"].(string)
		if proto == "" {
			// Maybe a clash proxy object in JSON form.
			if converted := clashProxyToOutbound(m); converted != nil {
				obs = append(obs, converted.Outbound)
				ids = append(ids, converted.Identity)
			}
			continue
		}
		ob := Outbound(cloneMap(m))
		SanitizeOutboundHTTPHeaders(ob)
		tag, _ := ob["tag"].(string)
		ids = append(ids, "json:"+proto+":"+tag)
		obs = append(obs, ob)
	}
	return obs, ids
}

func tryParseClashProxies(text string) ([]Outbound, []string, bool) {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "proxies:") && !strings.Contains(lower, "proxy-providers:") {
		return nil, nil, false
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil, false
	}
	raw, ok := doc["proxies"]
	if !ok {
		return nil, nil, false
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, nil, false
	}
	var proxies []map[string]any
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			proxies = append(proxies, m)
		}
	}
	obs, ids := clashProxiesToOutbounds(proxies)
	if len(obs) == 0 {
		return nil, nil, false
	}
	return obs, ids, true
}

func clashProxiesToOutbounds(proxies []map[string]any) ([]Outbound, []string) {
	var obs []Outbound
	var ids []string
	for _, p := range proxies {
		res := clashProxyToOutbound(p)
		if res == nil {
			continue
		}
		obs = append(obs, res.Outbound)
		ids = append(ids, res.Identity)
	}
	return obs, ids
}

func clashProxyToOutbound(p map[string]any) *ParseResult {
	typ := strings.ToLower(strings.TrimSpace(anyString(p["type"])))
	name := anyString(p["name"])
	server := anyString(p["server"])
	port := anyInt(p["port"])
	if server == "" || port <= 0 {
		return nil
	}

	switch typ {
	case "vmess":
		j := map[string]any{
			"v": "2", "ps": name, "add": server, "port": port,
			"id": anyString(p["uuid"]), "aid": anyInt(p["alterId"]),
			"scy": firstNonEmpty(anyString(p["cipher"]), "auto"),
			"net": firstNonEmpty(anyString(p["network"]), "tcp"),
			"tls": boolTLS(p),
			"sni": firstNonEmpty(anyString(p["servername"]), anyString(p["sni"])),
			"fp":  anyString(p["client-fingerprint"]),
			"alpn": joinAny(p["alpn"]),
		}
		applyClashTransportToVmessJSON(j, p)
		raw, _ := json.Marshal(j)
		return mustParse(parseVmess("vmess://" + base64.StdEncoding.EncodeToString(raw)))
	case "vless":
		q := url.Values{}
		q.Set("type", normalizeNetwork(firstNonEmpty(anyString(p["network"]), "tcp")))
		q.Set("security", clashSecurity(p))
		q.Set("encryption", firstNonEmpty(anyString(p["encryption"]), "none"))
		if flow := anyString(p["flow"]); flow != "" {
			q.Set("flow", flow)
		}
		fillClashQuery(q, p)
		link := fmt.Sprintf("vless://%s@%s:%d?%s#%s",
			url.PathEscape(anyString(p["uuid"])), hostportLiteral(server), port, q.Encode(), url.PathEscape(name))
		return mustParse(parseVless(link))
	case "trojan":
		q := url.Values{}
		q.Set("type", normalizeNetwork(firstNonEmpty(anyString(p["network"]), "tcp")))
		q.Set("security", firstNonEmpty(clashSecurity(p), "tls"))
		fillClashQuery(q, p)
		link := fmt.Sprintf("trojan://%s@%s:%d?%s#%s",
			url.PathEscape(anyString(p["password"])), hostportLiteral(server), port, q.Encode(), url.PathEscape(name))
		return mustParse(parseTrojan(link))
	case "ss", "shadowsocks":
		method := firstNonEmpty(anyString(p["cipher"]), anyString(p["method"]))
		pass := anyString(p["password"])
		user := base64.StdEncoding.EncodeToString([]byte(method + ":" + pass))
		link := fmt.Sprintf("ss://%s@%s:%d#%s", user, hostportLiteral(server), port, url.PathEscape(name))
		return mustParse(parseShadowsocks(link))
	case "hysteria2", "hy2":
		q := url.Values{}
		if sni := firstNonEmpty(anyString(p["sni"]), anyString(p["servername"])); sni != "" {
			q.Set("sni", sni)
		}
		if fp := anyString(p["client-fingerprint"]); fp != "" {
			q.Set("fp", fp)
		}
		if insecureBool(p) {
			q.Set("insecure", "1")
		}
		auth := firstNonEmpty(anyString(p["password"]), anyString(p["auth"]))
		link := fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s",
			url.PathEscape(auth), hostportLiteral(server), port, q.Encode(), url.PathEscape(name))
		return mustParse(parseHysteria2(link))
	case "hysteria", "hy":
		// Best-effort map hy1 → hysteria outbound v2 with auth.
		auth := firstNonEmpty(anyString(p["auth_str"]), anyString(p["auth-str"]), anyString(p["password"]), anyString(p["auth"]))
		q := url.Values{}
		if sni := firstNonEmpty(anyString(p["sni"]), anyString(p["servername"])); sni != "" {
			q.Set("sni", sni)
		}
		link := fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s",
			url.PathEscape(auth), hostportLiteral(server), port, q.Encode(), url.PathEscape(name))
		res := mustParse(parseHysteria2(link))
		if res != nil {
			if settings, ok := res.Outbound["settings"].(map[string]any); ok {
				settings["version"] = 1
			}
			if stream, ok := res.Outbound["streamSettings"].(map[string]any); ok {
				if hs, ok := stream["hysteriaSettings"].(map[string]any); ok {
					hs["version"] = 1
				}
			}
		}
		return res
	case "wireguard", "wg":
		q := url.Values{}
		q.Set("publickey", anyString(p["public-key"]))
		if ip := joinAny(p["ip"]); ip != "" {
			q.Set("address", ip)
		}
		if allowed := joinAny(p["allowed-ips"]); allowed != "" {
			q.Set("allowedips", allowed)
		}
		if psk := anyString(p["pre-shared-key"]); psk != "" {
			q.Set("presharedkey", psk)
		}
		if mtu := anyInt(p["mtu"]); mtu > 0 {
			q.Set("mtu", strconv.Itoa(mtu))
		}
		secret := anyString(p["private-key"])
		link := fmt.Sprintf("wireguard://%s@%s:%d?%s#%s",
			url.PathEscape(secret), hostportLiteral(server), port, q.Encode(), url.PathEscape(name))
		return mustParse(parseWireguard(link))
	case "socks", "socks5", "socks4":
		user := anyString(p["username"])
		pass := anyString(p["password"])
		return parseSocksHTTP("socks", server, port, user, pass, name)
	case "http", "https":
		user := anyString(p["username"])
		pass := anyString(p["password"])
		return parseSocksHTTP("http", server, port, user, pass, name)
	default:
		return nil
	}
}

func applyClashTransportToVmessJSON(j map[string]any, p map[string]any) {
	net := normalizeNetwork(anyString(j["net"]))
	j["net"] = net
	opts, _ := p["ws-opts"].(map[string]any)
	if opts == nil {
		opts, _ = p["ws_opts"].(map[string]any)
	}
	switch net {
	case "ws":
		if opts != nil {
			j["path"] = anyString(opts["path"])
			if h, ok := opts["headers"].(map[string]any); ok {
				j["host"] = anyString(h["Host"])
			}
		}
		if j["path"] == "" {
			j["path"] = anyString(p["path"])
		}
		if j["host"] == "" {
			j["host"] = anyString(p["host"])
		}
	case "grpc":
		gopts, _ := p["grpc-opts"].(map[string]any)
		if gopts != nil {
			j["path"] = firstNonEmpty(anyString(gopts["grpc-service-name"]), anyString(gopts["serviceName"]))
		}
	case "httpupgrade", "xhttp":
		j["path"] = anyString(p["path"])
		j["host"] = anyString(p["host"])
		if m := anyString(p["mode"]); m != "" {
			j["mode"] = m
		}
	case "tcp":
		if anyString(p["http-opts"]) != "" || anyString(p["headerType"]) == "http" {
			j["type"] = "http"
			j["host"] = anyString(p["host"])
			j["path"] = firstNonEmpty(anyString(p["path"]), "/")
		}
	}
}

func fillClashQuery(q url.Values, p map[string]any) {
	net := q.Get("type")
	host := firstNonEmpty(anyString(p["host"]), clashWSHost(p))
	path := firstNonEmpty(anyString(p["path"]), clashWSPath(p))
	if host != "" {
		q.Set("host", host)
	}
	if path != "" {
		q.Set("path", path)
	}
	if sni := firstNonEmpty(anyString(p["servername"]), anyString(p["sni"])); sni != "" {
		q.Set("sni", sni)
	}
	if fp := anyString(p["client-fingerprint"]); fp != "" {
		q.Set("fp", fp)
	}
	if alpn := joinAny(p["alpn"]); alpn != "" {
		q.Set("alpn", alpn)
	}
	if ro, ok := p["reality-opts"].(map[string]any); ok {
		if v := anyString(ro["public-key"]); v != "" {
			q.Set("pbk", v)
		}
		if v := anyString(ro["short-id"]); v != "" {
			q.Set("sid", v)
		}
	}
	if net == "grpc" {
		if gopts, ok := p["grpc-opts"].(map[string]any); ok {
			if v := firstNonEmpty(anyString(gopts["grpc-service-name"]), anyString(gopts["serviceName"])); v != "" {
				q.Set("serviceName", v)
			}
		}
	}
	if net == "tcp" && (anyString(p["headerType"]) == "http" || p["http-opts"] != nil) {
		q.Set("headerType", "http")
	}
}

func clashWSHost(p map[string]any) string {
	if opts, ok := p["ws-opts"].(map[string]any); ok {
		if h, ok := opts["headers"].(map[string]any); ok {
			return anyString(h["Host"])
		}
	}
	return ""
}

func clashWSPath(p map[string]any) string {
	if opts, ok := p["ws-opts"].(map[string]any); ok {
		return anyString(opts["path"])
	}
	return ""
}

func clashSecurity(p map[string]any) string {
	if p["reality-opts"] != nil {
		return "reality"
	}
	tls := p["tls"]
	switch v := tls.(type) {
	case bool:
		if v {
			return "tls"
		}
	case string:
		if strings.EqualFold(v, "true") || strings.EqualFold(v, "tls") {
			return "tls"
		}
	}
	return "none"
}

func boolTLS(p map[string]any) string {
	if clashSecurity(p) == "tls" || clashSecurity(p) == "reality" {
		return "tls"
	}
	return ""
}

func insecureBool(p map[string]any) bool {
	switch v := p["skip-cert-verify"].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func mustParse(res *ParseResult, err error) *ParseResult {
	if err != nil || res == nil {
		return nil
	}
	return res
}

func hostportLiteral(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// ParseLink parses a single share link.
func ParseLink(raw string) (*ParseResult, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "vmess://"):
		return parseVmess(raw)
	case strings.HasPrefix(lower, "vless://"):
		return parseVless(raw)
	case strings.HasPrefix(lower, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(lower, "ss://"):
		return parseShadowsocks(raw)
	case strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		return parseHysteria2(raw)
	case strings.HasPrefix(lower, "hysteria://"), strings.HasPrefix(lower, "hy://"):
		return parseHysteria1(raw)
	case strings.HasPrefix(lower, "wireguard://"), strings.HasPrefix(lower, "wg://"):
		return parseWireguard(raw)
	case strings.HasPrefix(lower, "socks://"), strings.HasPrefix(lower, "socks5://"), strings.HasPrefix(lower, "socks4://"):
		return parseSocksLink(raw)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		// Only treat as HTTP proxy URI when userinfo is present (avoid eating random URLs).
		if u, err := url.Parse(raw); err == nil && u.User != nil && u.Host != "" {
			return parseHTTPProxyLink(raw)
		}
		return nil, fmt.Errorf("not a proxy http link")
	default:
		return nil, fmt.Errorf("unsupported link scheme")
	}
}

// --- vmess ---

func parseVmess(link string) (*ParseResult, error) {
	lower := strings.ToLower(link)
	idx := strings.Index(lower, "vmess://")
	if idx < 0 {
		return nil, fmt.Errorf("not vmess")
	}
	b64 := link[idx+8:]
	// strip query/fragment some clients append
	if i := strings.IndexAny(b64, "?#"); i >= 0 {
		b64 = b64[:i]
	}
	raw, err := base64DecodeBytes(b64)
	if err != nil {
		return nil, fmt.Errorf("vmess decode: %w", err)
	}
	var j map[string]any
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("vmess json: %w", err)
	}

	identity := vmessIdentity(j)
	network := normalizeNetwork(getString(j, "net", "tcp"))
	security := "none"
	tlsField := strings.ToLower(getString(j, "tls", ""))
	if tlsField == "tls" || tlsField == "1" || tlsField == "true" {
		security = "tls"
	}
	if strings.EqualFold(getString(j, "type", ""), "reality") || getString(j, "pbk", "") != "" {
		// rare vmess+reality style fields
		if security == "none" && getString(j, "pbk", "") != "" {
			security = "reality"
		}
	}
	stream := buildStream(network, security)
	applyVmessTransport(stream, j, network)

	if security == "tls" {
		tls := stream["tlsSettings"].(map[string]any)
		tls["serverName"] = firstNonEmpty(getString(j, "sni", ""), getString(j, "host", ""))
		tls["fingerprint"] = getString(j, "fp", "")
		if alpn := getString(j, "alpn", ""); alpn != "" {
			tls["alpn"] = splitComma(alpn)
		}
	}

	port := num(j["port"])
	aid := num(j["aid"])
	user := map[string]any{
		"id":       getString(j, "id", ""),
		"security": firstNonEmpty(getString(j, "scy", ""), "auto"),
	}
	if aid > 0 {
		user["alterId"] = aid
	}
	ob := Outbound{
		"protocol": "vmess",
		"tag":      getString(j, "ps", ""),
		"settings": map[string]any{
			"vnext": []any{
				map[string]any{
					"address": getString(j, "add", ""),
					"port":    port,
					"users":   []any{user},
				},
			},
		},
		"streamSettings": stream,
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

func applyVmessTransport(stream map[string]any, j map[string]any, network string) {
	host := getString(j, "host", "")
	path := getString(j, "path", "/")
	switch network {
	case "ws":
		setWS(stream, host, firstNonEmpty(path, "/"))
	case "grpc":
		gs := stream["grpcSettings"].(map[string]any)
		gs["serviceName"] = firstNonEmpty(getString(j, "path", ""), getString(j, "serviceName", ""))
		if auth := getString(j, "authority", ""); auth != "" {
			gs["authority"] = auth
		}
		gs["multiMode"] = getString(j, "type", "") == "multi" || getString(j, "mode", "") == "multi"
	case "httpupgrade":
		setHTTPUpgrade(stream, host, firstNonEmpty(path, "/"))
	case "xhttp":
		xh := stream["xhttpSettings"].(map[string]any)
		xh["host"] = host
		xh["path"] = firstNonEmpty(path, "/")
		if m := firstNonEmpty(getString(j, "mode", ""), getString(j, "type", "")); m != "" && m != "http" {
			xh["mode"] = m
		}
	case "kcp", "mkcp":
		ks := stream["kcpSettings"].(map[string]any)
		ks["header"] = map[string]any{"type": firstNonEmpty(getString(j, "type", ""), "none")}
		if path != "" && path != "/" {
			ks["seed"] = path
		}
	case "tcp":
		headerType := strings.ToLower(getString(j, "type", ""))
		if headerType == "http" {
			stream["tcpSettings"] = tcpHTTPSettings(host, firstNonEmpty(path, "/"))
		}
	}
}

func vmessIdentity(j map[string]any) string {
	core := map[string]any{}
	for k, v := range j {
		if k == "ps" {
			continue
		}
		core[k] = v
	}
	b, _ := json.Marshal(core)
	return "vmess:" + string(b)
}

// --- vless / trojan ---

func parseVless(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	id := u.User.Username()
	host := u.Hostname()
	port := defaultPort(u.Port(), 443)
	params := u.Query()
	network := normalizeNetwork(firstNonEmpty(params.Get("type"), "tcp"))
	security := firstNonEmpty(params.Get("security"), "none")
	stream := buildStream(network, security)
	applyTransport(stream, params, network)
	applySecurity(stream, params)
	applyFinalMask(stream, params)

	identity := "vless:" + id + "@" + host + ":" + strconv.Itoa(port) + "?" + canonicalQuery(params)
	ob := Outbound{
		"protocol": "vless",
		"tag":      decodeHash(u.Fragment),
		"settings": map[string]any{
			"address":    host,
			"port":       port,
			"id":         id,
			"flow":       params.Get("flow"),
			"encryption": firstNonEmpty(params.Get("encryption"), "none"),
		},
		"streamSettings": stream,
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

func parseTrojan(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	pw, _ := url.QueryUnescape(u.User.Username())
	host := u.Hostname()
	port := defaultPort(u.Port(), 443)
	params := u.Query()
	network := normalizeNetwork(firstNonEmpty(params.Get("type"), "tcp"))
	security := firstNonEmpty(params.Get("security"), "tls")
	stream := buildStream(network, security)
	applyTransport(stream, params, network)
	applySecurity(stream, params)
	applyFinalMask(stream, params)

	identity := "trojan:" + pw + "@" + host + ":" + strconv.Itoa(port) + "?" + canonicalQuery(params)
	ob := Outbound{
		"protocol": "trojan",
		"tag":      decodeHash(u.Fragment),
		"settings": map[string]any{
			"servers": []any{
				map[string]any{"address": host, "port": port, "password": pw},
			},
		},
		"streamSettings": stream,
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

// --- shadowsocks ---

func parseShadowsocks(link string) (*ParseResult, error) {
	remark := ""
	core := link
	if i := strings.Index(core, "#"); i >= 0 {
		remark, _ = url.QueryUnescape(core[i+1:])
		core = core[:i]
	}
	// Strip plugin/query for host:port parse; SIP003 plugins are not mapped into xray SS outbound.
	query := ""
	if i := strings.Index(core, "?"); i >= 0 {
		query = core[i+1:]
		core = core[:i]
	}
	_ = query

	core = strings.TrimPrefix(core, "ss://")
	if idx := strings.Index(strings.ToLower(link), "ss://"); idx >= 0 && !strings.HasPrefix(link, "ss://") {
		core = link[idx+5:]
		if i := strings.Index(core, "#"); i >= 0 {
			core = core[:i]
		}
		if i := strings.Index(core, "?"); i >= 0 {
			core = core[:i]
		}
	}

	var method, pass, host string
	var port int

	if at := strings.Index(core, "@"); at >= 0 {
		userPart := core[:at]
		hp := core[at+1:]
		userInfo, err := base64DecodeFlexible(userPart)
		if err != nil {
			userInfo, _ = url.QueryUnescape(userPart)
		}
		method, pass = splitMethodPass(userInfo)
		host, port = splitHostPortFlexible(hp)
	} else {
		dec, err := base64DecodeFlexible(core)
		if err != nil {
			return nil, err
		}
		at := strings.Index(dec, "@")
		if at < 0 {
			return nil, fmt.Errorf("bad legacy ss")
		}
		method, pass = splitMethodPass(dec[:at])
		host, port = splitHostPortFlexible(dec[at+1:])
	}
	if host == "" || port <= 0 {
		return nil, fmt.Errorf("bad ss host:port")
	}

	identity := "ss:" + method + ":" + pass + "@" + host + ":" + strconv.Itoa(port)
	ob := Outbound{
		"protocol": "shadowsocks",
		"tag":      remark,
		"settings": map[string]any{
			"servers": []any{
				map[string]any{
					"address":    host,
					"port":       port,
					"password":   pass,
					"method":     method,
					"uot":        false,
					"UoTVersion": 2,
				},
			},
		},
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

func splitMethodPass(userInfo string) (string, string) {
	colon := strings.Index(userInfo, ":")
	if colon < 0 {
		return "aes-256-gcm", userInfo
	}
	return userInfo[:colon], userInfo[colon+1:]
}

func splitHostPortFlexible(hp string) (string, int) {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return "", 0
	}
	// bracketed IPv6
	if strings.HasPrefix(hp, "[") {
		host, portStr, err := net.SplitHostPort(hp)
		if err != nil {
			return "", 0
		}
		port, _ := strconv.Atoi(portStr)
		return host, port
	}
	// host:port or ipv4:port
	host, portStr, err := net.SplitHostPort(hp)
	if err == nil {
		port, _ := strconv.Atoi(portStr)
		return host, port
	}
	// last-colon fallback
	colon := strings.LastIndex(hp, ":")
	if colon < 0 {
		return hp, 0
	}
	port, _ := strconv.Atoi(hp[colon+1:])
	return hp[:colon], port
}

// --- hysteria ---

func parseHysteria2(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	auth, _ := url.QueryUnescape(u.User.Username())
	if pass, ok := u.User.Password(); ok {
		// user:pass style rare
		auth = auth + ":" + pass
	}
	host := u.Hostname()
	port := defaultPort(u.Port(), 443)
	params := u.Query()

	hs := map[string]any{
		"version":        2,
		"auth":           auth,
		"udpIdleTimeout": 60,
	}
	for _, key := range []string{"congestion", "up", "down", "udphopPort"} {
		if v := firstParam(params, key, strings.ToLower(key)); v != "" {
			hs[key] = v
		}
	}
	if v := firstParam(params, "maxIdleTimeout", "idle", "udpIdleTimeout"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hs["udpIdleTimeout"] = n
		}
	}

	security := "tls"
	if params.Get("security") == "none" {
		security = "none"
	}
	stream := map[string]any{
		"network":          "hysteria",
		"security":         security,
		"hysteriaSettings": hs,
	}
	if security == "tls" {
		stream["tlsSettings"] = map[string]any{
			"serverName":           firstNonEmpty(params.Get("sni"), params.Get("peer"), host),
			"alpn":                 toAnySlice(splitCommaOrDefault(params.Get("alpn"), []string{"h3"})),
			"fingerprint":          params.Get("fp"),
			"echConfigList":        params.Get("ech"),
			"verifyPeerCertByName": "",
			"pinnedPeerCertSha256": firstParam(params, "pinSHA256", "pinsha256", "pcs"),
		}
	}
	applyFinalMask(stream, params)

	identity := "hysteria2:" + auth + "@" + host + ":" + strconv.Itoa(port) + "?" + canonicalQuery(params)
	ob := Outbound{
		"protocol":       "hysteria",
		"tag":            decodeHash(u.Fragment),
		"settings":       map[string]any{"address": host, "port": port, "version": 2},
		"streamSettings": stream,
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

func parseHysteria1(link string) (*ParseResult, error) {
	// hysteria://host:port?auth=...&peer=...#remark  OR hy://auth@host:port
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	params := u.Query()
	auth := firstNonEmpty(u.User.Username(), params.Get("auth"), params.Get("auth_str"))
	host := u.Hostname()
	port := defaultPort(u.Port(), 443)
	fake := fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s",
		url.PathEscape(auth), hostportLiteral(host), port, params.Encode(), u.EscapedFragment())
	res, err := parseHysteria2(fake)
	if err != nil {
		return nil, err
	}
	if settings, ok := res.Outbound["settings"].(map[string]any); ok {
		settings["version"] = 1
	}
	if stream, ok := res.Outbound["streamSettings"].(map[string]any); ok {
		if hs, ok := stream["hysteriaSettings"].(map[string]any); ok {
			hs["version"] = 1
		}
	}
	res.Identity = "hysteria1:" + auth + "@" + host + ":" + strconv.Itoa(port)
	return res, nil
}

// --- wireguard ---

func parseWireguard(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	secret, _ := url.QueryUnescape(u.User.Username())
	params := u.Query()
	host := u.Hostname()
	portStr := u.Port()
	endpoint := host
	if portStr != "" {
		endpoint = net.JoinHostPort(host, portStr)
	}

	addrs := splitComma(firstParam(params, "address", "ip"))
	if len(addrs) == 0 {
		addrs = []string{"10.0.0.2/32"}
	}
	allowed := splitComma(firstParam(params, "allowedips", "allowed_ips", "allowedIPs"))
	if len(allowed) == 0 {
		allowed = []string{"0.0.0.0/0", "::/0"}
	}

	peer := map[string]any{
		"publicKey":  firstParam(params, "publickey", "publicKey", "public_key", "peerPublicKey"),
		"endpoint":   endpoint,
		"allowedIPs": allowed,
	}
	if psk := firstParam(params, "presharedkey", "preshared_key", "pre-shared-key", "psk"); psk != "" {
		peer["preSharedKey"] = psk
	}
	if ka := firstParam(params, "keepalive", "persistentkeepalive", "persistent_keepalive"); ka != "" {
		if n, err := strconv.Atoi(ka); err == nil {
			peer["keepAlive"] = n
		}
	}

	settings := map[string]any{
		"secretKey": secret,
		"address":   addrs,
		"peers":     []any{peer},
	}
	if mtu := params.Get("mtu"); mtu != "" {
		if n, err := strconv.Atoi(mtu); err == nil {
			settings["mtu"] = n
		}
	}
	if res := params.Get("reserved"); res != "" {
		var iv []int
		for _, p := range splitComma(res) {
			if n, err := strconv.Atoi(p); err == nil {
				iv = append(iv, n)
			}
		}
		if len(iv) > 0 {
			settings["reserved"] = iv
		}
	}

	identity := "wireguard:" + secret + "@" + endpoint
	ob := Outbound{
		"protocol": "wireguard",
		"tag":      decodeHash(u.Fragment),
		"settings": settings,
	}
	return &ParseResult{Outbound: ob, Identity: identity}, nil
}

// --- socks / http ---

func parseSocksLink(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	return parseSocksHTTP("socks", u.Hostname(), defaultPort(u.Port(), 1080), user, pass, decodeHash(u.Fragment)), nil
}

func parseHTTPProxyLink(link string) (*ParseResult, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	defPort := 80
	if u.Scheme == "https" {
		defPort = 443
	}
	return parseSocksHTTP("http", u.Hostname(), defaultPort(u.Port(), defPort), user, pass, decodeHash(u.Fragment)), nil
}

func parseSocksHTTP(protocol, host string, port int, user, pass, remark string) *ParseResult {
	server := map[string]any{"address": host, "port": port}
	if user != "" || pass != "" {
		server["users"] = []any{map[string]any{"user": user, "pass": pass}}
	}
	identity := protocol + ":" + user + "@" + host + ":" + strconv.Itoa(port)
	ob := Outbound{
		"protocol": protocol,
		"tag":      remark,
		"settings": map[string]any{"servers": []any{server}},
	}
	return &ParseResult{Outbound: ob, Identity: identity}
}

// --- stream helpers ---

func normalizeNetwork(n string) string {
	n = strings.ToLower(strings.TrimSpace(n))
	switch n {
	case "", "none", "raw":
		return "tcp"
	case "h2", "http", "http2":
		return "xhttp" // closest supported panel transport
	case "splithttp", "split":
		return "xhttp"
	case "mkcp":
		return "kcp"
	case "gun":
		return "grpc"
	default:
		return n
	}
}

func buildStream(network, security string) map[string]any {
	network = normalizeNetwork(network)
	security = strings.ToLower(strings.TrimSpace(security))
	if security == "" {
		security = "none"
	}
	stream := map[string]any{"network": network, "security": security}
	switch network {
	case "tcp":
		stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "none"}}
	case "kcp":
		stream["kcpSettings"] = map[string]any{
			"mtu": 1350, "tti": 20, "uplinkCapacity": 5, "downlinkCapacity": 20,
			"congestion": false, "readBufferSize": 2, "writeBufferSize": 2,
			"header": map[string]any{"type": "none"}, "seed": "",
		}
	case "ws":
		stream["wsSettings"] = map[string]any{"path": "/", "host": "", "headers": map[string]any{}, "heartbeatPeriod": 0}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": "", "authority": "", "multiMode": false}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": "/", "host": "", "headers": map[string]any{}}
	case "xhttp":
		stream["xhttpSettings"] = map[string]any{
			"path": "/", "host": "", "mode": "auto", "headers": map[string]any{},
			"xPaddingBytes": "100-1000", "scMaxEachPostBytes": "1000000",
		}
	default:
		stream["network"] = "tcp"
		stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "none"}}
	}
	if security == "tls" {
		stream["tlsSettings"] = map[string]any{
			"serverName": "", "alpn": []any{}, "fingerprint": "",
			"echConfigList": "", "verifyPeerCertByName": "", "pinnedPeerCertSha256": "",
		}
	} else if security == "reality" {
		stream["realitySettings"] = map[string]any{
			"publicKey": "", "fingerprint": "chrome", "serverName": "",
			"shortId": "", "spiderX": "", "mldsa65Verify": "",
		}
	}
	return stream
}

func setWS(stream map[string]any, host, path string) {
	ws := stream["wsSettings"].(map[string]any)
	ws["host"] = host
	ws["path"] = firstNonEmpty(path, "/")
}

func setHTTPUpgrade(stream map[string]any, host, path string) {
	h := stream["httpupgradeSettings"].(map[string]any)
	h["host"] = host
	h["path"] = firstNonEmpty(path, "/")
}

func applyTransport(stream map[string]any, p url.Values, network string) {
	host := firstNonEmpty(p.Get("host"), p.Get("Host"))
	path := firstNonEmpty(p.Get("path"), "/")
	switch network {
	case "ws":
		setWS(stream, host, path)
	case "grpc":
		gs := stream["grpcSettings"].(map[string]any)
		gs["serviceName"] = firstNonEmpty(p.Get("serviceName"), p.Get("path"))
		gs["authority"] = p.Get("authority")
		gs["multiMode"] = p.Get("mode") == "multi"
	case "httpupgrade":
		setHTTPUpgrade(stream, host, path)
	case "xhttp":
		xh := stream["xhttpSettings"].(map[string]any)
		xh["host"] = host
		xh["path"] = path
		if m := p.Get("mode"); m != "" {
			xh["mode"] = m
		}
		for _, k := range []string{"xPaddingBytes", "scMaxEachPostBytes", "scMinPostsIntervalMs", "uplinkChunkSize"} {
			if v := p.Get(k); v != "" {
				xh[k] = v
			}
		}
	case "kcp":
		ks := stream["kcpSettings"].(map[string]any)
		ks["header"] = map[string]any{"type": firstNonEmpty(p.Get("headerType"), p.Get("type"), "none")}
		if seed := firstNonEmpty(p.Get("seed"), path); seed != "" && seed != "/" {
			ks["seed"] = seed
		}
	case "tcp":
		if p.Get("headerType") == "http" || p.Get("type") == "http" {
			stream["tcpSettings"] = tcpHTTPSettings(host, path)
		}
	}
}

func tcpHTTPSettings(host, path string) map[string]any {
	headers := map[string]any{}
	if hostVals := splitComma(host); len(hostVals) > 0 {
		headers["Host"] = hostVals
	}
	pathVals := splitComma(path)
	if len(pathVals) == 0 {
		pathVals = []string{"/"}
	}
	return map[string]any{
		"header": map[string]any{
			"type": "http",
			"request": map[string]any{
				"version": "1.1",
				"method":  "GET",
				"path":    pathVals,
				"headers": headers,
			},
		},
	}
}

// SanitizeOutboundHTTPHeaders removes null/empty HTTP camouflage header values.
func SanitizeOutboundHTTPHeaders(ob map[string]any) {
	if ob == nil {
		return
	}
	stream, _ := ob["streamSettings"].(map[string]any)
	if stream == nil {
		return
	}
	tcp, _ := stream["tcpSettings"].(map[string]any)
	if tcp == nil {
		return
	}
	header, _ := tcp["header"].(map[string]any)
	if header == nil {
		return
	}
	req, _ := header["request"].(map[string]any)
	if req == nil {
		return
	}
	headers, _ := req["headers"].(map[string]any)
	if headers == nil {
		return
	}
	for k, v := range headers {
		if isEmptyHeaderValue(v) {
			delete(headers, k)
		}
	}
}

func isEmptyHeaderValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []any:
		if len(t) == 0 {
			return true
		}
		for _, item := range t {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return true
			}
		}
		return false
	case []string:
		if len(t) == 0 {
			return true
		}
		for _, s := range t {
			if strings.TrimSpace(s) == "" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func applySecurity(stream map[string]any, p url.Values) {
	sec, _ := stream["security"].(string)
	if sec == "tls" {
		tls := stream["tlsSettings"].(map[string]any)
		tls["serverName"] = firstNonEmpty(p.Get("sni"), p.Get("peer"))
		tls["fingerprint"] = p.Get("fp")
		if alpn := p.Get("alpn"); alpn != "" {
			tls["alpn"] = splitComma(alpn)
		}
		tls["echConfigList"] = p.Get("ech")
		tls["pinnedPeerCertSha256"] = firstParam(p, "pcs", "pinSHA256")
	} else if sec == "reality" {
		re := stream["realitySettings"].(map[string]any)
		re["serverName"] = p.Get("sni")
		re["fingerprint"] = firstNonEmpty(p.Get("fp"), "chrome")
		re["publicKey"] = p.Get("pbk")
		re["shortId"] = p.Get("sid")
		re["spiderX"] = p.Get("spx")
		re["mldsa65Verify"] = p.Get("pqv")
	}
}

func applyFinalMask(stream map[string]any, p url.Values) {
	if fm := p.Get("fm"); fm != "" {
		var parsed any
		if json.Unmarshal([]byte(fm), &parsed) == nil {
			stream["finalmask"] = parsed
		}
	}
}

// --- misc helpers ---

func tryBase64(s string) (string, bool) {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for len(clean)%4 != 0 {
		clean += "="
	}
	if b, err := base64.StdEncoding.DecodeString(clean); err == nil {
		out := string(b)
		if looksLikeSubscriptionText(out) {
			return out, true
		}
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(clean, "=")); err == nil {
		out := string(b)
		if looksLikeSubscriptionText(out) {
			return out, true
		}
	}
	if b, err := base64.URLEncoding.DecodeString(clean); err == nil {
		out := string(b)
		if looksLikeSubscriptionText(out) {
			return out, true
		}
	}
	return "", false
}

func looksLikeSubscriptionText(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	if strings.Contains(low, "proxies:") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
		return true
	}
	for _, p := range []string{"vmess://", "vless://", "trojan://", "ss://", "hysteria", "hy2://", "wireguard://", "socks"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}

func firstNonEmpty(vals ...string) string {
	for _, a := range vals {
		if strings.TrimSpace(a) != "" {
			return a
		}
	}
	return ""
}

func firstParam(p url.Values, keys ...string) string {
	for _, k := range keys {
		if v := p.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func canonicalQuery(p url.Values) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range p[k] {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "&")
}

func decodeHash(h string) string {
	if h == "" {
		return ""
	}
	if dec, err := url.QueryUnescape(h); err == nil {
		return dec
	}
	return h
}

func defaultPort(p string, def int) int {
	if p == "" {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case float32:
		return int(x)
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case uint:
		return int(x)
	case uint32:
		return int(x)
	case uint64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

func anyInt(v any) int { return num(v) }

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

func joinAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(anyString(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(t, ",")
	default:
		return anyString(v)
	}
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func getString(m map[string]any, key, def string) string {
	if v, ok := m[key]; ok {
		if s := anyString(v); s != "" {
			return s
		}
	}
	return def
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitCommaOrDefault(s string, def []string) []string {
	if s == "" {
		return def
	}
	return splitComma(s)
}

func padBase64(s string) string {
	for len(s)%4 != 0 {
		s += "="
	}
	return s
}

func base64DecodeFlexible(s string) (string, error) {
	b, err := base64DecodeBytes(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func base64DecodeBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	padded := padBase64(s)
	if b, err := base64.StdEncoding.DecodeString(padded); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(padded); err == nil {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "=")); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("base64 decode failed")
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// SuggestTag / SlugRemark keep tag allocation stable for the service layer.

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func SuggestTag(prefix, remark string, idx int) string {
	base := SlugRemark(remark)
	if base == "" {
		base = fmt.Sprintf("%d", idx)
	}
	p := strings.TrimSuffix(prefix, "-")
	if p != "" {
		return p + "-" + base
	}
	return base
}

func SlugRemark(remark string) string {
	s := strings.ToLower(strings.TrimSpace(remark))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}
