package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// UnwrapXrayTemplateConfig peels accidental nested {"xraySetting": ...} wrappers.
func UnwrapXrayTemplateConfig(raw string) string {
	const maxDepth = 8
	for range maxDepth {
		var top map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &top); err != nil {
			return raw
		}
		inner, ok := top["xraySetting"]
		if !ok {
			return raw
		}
		for _, k := range []string{"inbounds", "outbounds", "routing", "api", "dns", "log", "policy", "stats"} {
			if _, hit := top[k]; hit {
				return raw
			}
		}
		unwrapped := string(inner)
		var asStr string
		if err := json.Unmarshal(inner, &asStr); err == nil {
			unwrapped = asStr
		}
		raw = unwrapped
	}
	return raw
}

func selectorFromTags(outboundTags []string) []any {
	sel := make([]any, 0, len(outboundTags))
	for _, tag := range outboundTags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			sel = append(sel, tag)
		}
	}
	return sel
}

func normalizeSelectorTags(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func selectorTagsEqual(a, b any) bool {
	left := normalizeSelectorTags(a)
	right := normalizeSelectorTags(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func ensureRoutingAPIService(cfg map[string]any) bool {
	apiRaw, ok := cfg["api"]
	if !ok {
		cfg["api"] = map[string]any{
			"tag": "api",
			"services": []any{
				"HandlerService",
				"LoggerService",
				"StatsService",
				"RoutingService",
			},
		}
		return true
	}

	api, ok := apiRaw.(map[string]any)
	if !ok {
		return false
	}

	services := make([]any, 0)
	if raw, ok := api["services"].([]any); ok {
		services = append(services, raw...)
	}
	for _, item := range services {
		if fmt.Sprint(item) == "RoutingService" {
			return false
		}
	}
	services = append(services, "RoutingService")
	api["services"] = services
	return true
}

// ensureBalancerInTemplate persists only the balancer in xrayTemplateConfig.
// Routing rules are left to the user in the panel.
func ensureBalancerInTemplate(settingSvc *SettingService, balancerTag string, outboundTags []string, strategy, fallbackTag string) error {
	if strings.TrimSpace(balancerTag) == "" {
		return nil
	}
	if len(outboundTags) == 0 {
		return nil
	}
	if strings.TrimSpace(strategy) == "" {
		strategy = "roundRobin"
	}

	raw, err := settingSvc.GetXrayConfigTemplate()
	if err != nil {
		return err
	}
	raw = UnwrapXrayTemplateConfig(raw)

	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return err
	}

	changed := ensureRoutingAPIService(cfg)

	routing, _ := cfg["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
		cfg["routing"] = routing
	}

	balancers, _ := routing["balancers"].([]any)
	if balancers == nil {
		balancers = []any{}
	}

	balancerObj := map[string]any{
		"tag":      balancerTag,
		"selector": selectorFromTags(outboundTags),
		"strategy": map[string]any{"type": strategy},
	}
	if strings.TrimSpace(fallbackTag) != "" {
		balancerObj["fallbackTag"] = fallbackTag
	}

	found := false
	for i, b := range balancers {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if m["tag"] == balancerTag {
			found = true
			if !selectorTagsEqual(m["selector"], balancerObj["selector"]) {
				changed = true
			}
			balancers[i] = balancerObj
			break
		}
	}
	if !found {
		balancers = append(balancers, balancerObj)
		changed = true
	}
	routing["balancers"] = balancers

	if !changed {
		return nil
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	xraySetting := &XraySettingService{SettingService: *settingSvc}
	return xraySetting.SaveXraySetting(string(out))
}

