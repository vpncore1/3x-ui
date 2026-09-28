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

// balancerSelectorPrefix returns the xray balancer selector prefix for a subscription.
// Xray matches outbound tags with strings.HasPrefix, so a short prefix is correct.
func balancerSelectorPrefix(tagPrefix, balancerTag string, outboundTags []string) string {
	p := strings.TrimSpace(tagPrefix)
	p = strings.TrimSuffix(p, "-")
	if p != "" {
		return p
	}
	bt := strings.TrimSpace(balancerTag)
	if bt != "" {
		return bt
	}
	if len(outboundTags) > 0 {
		return outboundTags[0]
	}
	return ""
}

func replacePrefixedOutbounds(existing []any, prefix string, desired []map[string]any, prepend bool) []any {
	prefix = strings.TrimSpace(prefix)
	kept := make([]any, 0, len(existing))
	for _, item := range existing {
		m, ok := item.(map[string]any)
		if !ok {
			kept = append(kept, item)
			continue
		}
		tag, _ := m["tag"].(string)
		if prefix != "" && strings.HasPrefix(tag, prefix) {
			continue
		}
		// Also drop exact desired tags (handles prefix changes).
		drop := false
		for _, d := range desired {
			if dt, _ := d["tag"].(string); dt != "" && dt == tag {
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		kept = append(kept, item)
	}

	injected := make([]any, 0, len(desired))
	for _, d := range desired {
		injected = append(injected, d)
	}
	if prepend {
		return append(injected, kept...)
	}
	return append(kept, injected...)
}

// syncSubscriptionIntoTemplate writes fetched outbounds into xrayTemplateConfig
// (so they appear under Outbounds) and, when balancerTag is set, upserts the balancer
// with a prefix selector. Routing rules are left to the user.
func syncSubscriptionIntoTemplate(settingSvc *SettingService, balancerTag, tagPrefix, strategy, fallbackTag string, desired []map[string]any, prepend bool) error {
	if len(desired) == 0 {
		return nil
	}

	outboundTags := make([]string, 0, len(desired))
	for _, ob := range desired {
		if tag, _ := ob["tag"].(string); strings.TrimSpace(tag) != "" {
			outboundTags = append(outboundTags, tag)
		}
	}
	if len(outboundTags) == 0 {
		return fmt.Errorf("no outbound tags")
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

	changed := false

	prefix := strings.TrimSpace(tagPrefix)
	if prefix == "" && strings.TrimSpace(balancerTag) != "" {
		prefix = strings.TrimSpace(balancerTag) + "-"
	}
	matchPrefix := strings.TrimSuffix(prefix, "-")
	if matchPrefix == "" && len(outboundTags) > 0 {
		matchPrefix = outboundTags[0]
	}

	outbounds, _ := cfg["outbounds"].([]any)
	if outbounds == nil {
		outbounds = []any{}
	}
	newOutbounds := replacePrefixedOutbounds(outbounds, matchPrefix, desired, prepend)
	if !outboundListsEqual(outbounds, newOutbounds) {
		cfg["outbounds"] = newOutbounds
		changed = true
	} else {
		cfg["outbounds"] = outbounds
	}

	if strings.TrimSpace(balancerTag) != "" {
		if ensureRoutingAPIService(cfg) {
			changed = true
		}
		if strings.TrimSpace(strategy) == "" {
			strategy = "roundRobin"
		}
		selector := balancerSelectorPrefix(tagPrefix, balancerTag, outboundTags)
		balancerObj := map[string]any{
			"tag":      balancerTag,
			"selector": []any{selector},
			"strategy": map[string]any{"type": strategy},
		}
		if strings.TrimSpace(fallbackTag) != "" {
			balancerObj["fallbackTag"] = fallbackTag
		}

		routing, _ := cfg["routing"].(map[string]any)
		if routing == nil {
			routing = map[string]any{}
			cfg["routing"] = routing
			changed = true
		}
		balancers, _ := routing["balancers"].([]any)
		if balancers == nil {
			balancers = []any{}
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
				if fmt.Sprint(m["strategy"]) != fmt.Sprint(balancerObj["strategy"]) {
					changed = true
				}
				if fmt.Sprint(m["fallbackTag"]) != fmt.Sprint(balancerObj["fallbackTag"]) {
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
	}

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

func outboundListsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// removePrefixedOutboundsFromTemplate drops outbounds matching prefix (used on delete).
func removePrefixedOutboundsFromTemplate(settingSvc *SettingService, prefix, balancerTag string) error {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "-")
	if prefix == "" && strings.TrimSpace(balancerTag) == "" {
		return nil
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

	changed := false
	if outbounds, ok := cfg["outbounds"].([]any); ok && prefix != "" {
		kept := make([]any, 0, len(outbounds))
		for _, item := range outbounds {
			m, ok := item.(map[string]any)
			if !ok {
				kept = append(kept, item)
				continue
			}
			tag, _ := m["tag"].(string)
			if strings.HasPrefix(tag, prefix) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		cfg["outbounds"] = kept
	}

	if bt := strings.TrimSpace(balancerTag); bt != "" {
		if routing, ok := cfg["routing"].(map[string]any); ok {
			if balancers, ok := routing["balancers"].([]any); ok {
				kept := make([]any, 0, len(balancers))
				for _, b := range balancers {
					m, ok := b.(map[string]any)
					if ok && m["tag"] == bt {
						changed = true
						continue
					}
					kept = append(kept, b)
				}
				routing["balancers"] = kept
			}
		}
	}

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
