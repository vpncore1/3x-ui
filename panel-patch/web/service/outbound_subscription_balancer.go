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

// selectorFromOutboundTags builds balancer selectors from the actual outbound tags
// fetched from the subscription, so the Balancers UI lists each config explicitly.
// Xray still matches via HasPrefix, so full tags select exactly those outbounds.
func selectorFromOutboundTags(outboundTags []string) []any {
	sel := make([]any, 0, len(outboundTags))
	for _, tag := range outboundTags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			sel = append(sel, tag)
		}
	}
	return sel
}

func replacePrefixedOutbounds(existing []any, prefix string, desired []map[string]any, prepend bool) []any {
	prefix = strings.TrimSpace(prefix)
	// Prefer "foo-" style so "m-server" does not also wipe "m-server2-...".
	match := prefix
	if match != "" && !strings.HasSuffix(match, "-") {
		match = match + "-"
	}
	kept := make([]any, 0, len(existing))
	desiredSet := map[string]bool{}
	for _, d := range desired {
		if dt, _ := d["tag"].(string); dt != "" {
			desiredSet[dt] = true
		}
	}
	for _, item := range existing {
		m, ok := item.(map[string]any)
		if !ok {
			kept = append(kept, item)
			continue
		}
		tag, _ := m["tag"].(string)
		if match != "" && strings.HasPrefix(tag, match) {
			continue
		}
		if desiredSet[tag] {
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
// with those outbound tags as selectors. Routing rules are left to the user.
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
	matchPrefix := strings.TrimSpace(prefix)
	if matchPrefix != "" && !strings.HasSuffix(matchPrefix, "-") {
		matchPrefix = matchPrefix + "-"
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
		balancerObj := map[string]any{
			"tag":      balancerTag,
			"selector": selectorFromOutboundTags(outboundTags),
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
	prefix = strings.TrimSpace(prefix)
	match := prefix
	if match != "" && !strings.HasSuffix(match, "-") {
		match = match + "-"
	}
	if match == "" && strings.TrimSpace(balancerTag) == "" {
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
	if outbounds, ok := cfg["outbounds"].([]any); ok && match != "" {
		kept := make([]any, 0, len(outbounds))
		for _, item := range outbounds {
			m, ok := item.(map[string]any)
			if !ok {
				kept = append(kept, item)
				continue
			}
			tag, _ := m["tag"].(string)
			if strings.HasPrefix(tag, match) {
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

// removeBalancerOnlyFromTemplate removes a balancer by tag without touching outbounds.
func removeBalancerOnlyFromTemplate(settingSvc *SettingService, balancerTag string) error {
	balancerTag = strings.TrimSpace(balancerTag)
	if balancerTag == "" {
		return nil
	}
	return removePrefixedOutboundsFromTemplate(settingSvc, "", balancerTag)
}
