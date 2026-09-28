package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// UsesRuntimeSync is true when this subscription manages its own balancer pool via gRPC.
func UsesRuntimeSync(sub *model.OutboundSubscription) bool {
	return sub != nil && strings.TrimSpace(sub.BalancerTag) != ""
}

func effectiveTagPrefix(sub *model.OutboundSubscription) string {
	prefix := strings.TrimSpace(sub.TagPrefix)
	if prefix != "" {
		return prefix
	}
	if tag := strings.TrimSpace(sub.BalancerTag); tag != "" {
		return tag + "-"
	}
	return fmt.Sprintf("sub%d-", sub.Id)
}

func effectiveBalancerStrategy(sub *model.OutboundSubscription) string {
	s := strings.TrimSpace(sub.BalancerStrategy)
	if s == "" {
		return "roundRobin"
	}
	return s
}

func desiredOutboundsFromSub(sub *model.OutboundSubscription) ([]map[string]any, []string, error) {
	var desired []map[string]any
	if strings.TrimSpace(sub.LastFetchedOutbounds) != "" {
		if err := json.Unmarshal([]byte(sub.LastFetchedOutbounds), &desired); err != nil {
			return nil, nil, err
		}
	}
	outboundTags := make([]string, 0, len(desired))
	for _, ob := range desired {
		if tag, _ := ob["tag"].(string); strings.TrimSpace(tag) != "" {
			outboundTags = append(outboundTags, tag)
		}
	}
	return desired, outboundTags, nil
}

// ApplyTemplateSync writes subscription outbounds (and optional balancer) into the
// xray template so they show under Outbounds and survive restart.
func (s *OutboundSubscriptionService) ApplyTemplateSync(sub *model.OutboundSubscription) error {
	if sub == nil {
		return nil
	}
	desired, outboundTags, err := desiredOutboundsFromSub(sub)
	if err != nil {
		return err
	}
	if len(outboundTags) == 0 {
		return common.NewError("no outbound tags in subscription")
	}
	return syncSubscriptionIntoTemplate(
		&s.settingService,
		sub.BalancerTag,
		effectiveTagPrefix(sub),
		effectiveBalancerStrategy(sub),
		sub.FallbackTag,
		desired,
		sub.Prepend,
	)
}

// ApplyRuntimeSync persists outbounds into the template, then hot-reloads them via gRPC.
// Routing rules are not touched — configure them manually in the panel (balancerTag).
func (s *OutboundSubscriptionService) ApplyRuntimeSync(sub *model.OutboundSubscription, apiPort int) error {
	if sub == nil {
		return nil
	}

	desired, outboundTags, err := desiredOutboundsFromSub(sub)
	if err != nil {
		return err
	}
	if len(outboundTags) == 0 {
		return common.NewError("no outbound tags in subscription")
	}

	prefix := effectiveTagPrefix(sub)

	// Always write into template so Outbounds UI shows the configs.
	if err := syncSubscriptionIntoTemplate(
		&s.settingService,
		sub.BalancerTag,
		prefix,
		effectiveBalancerStrategy(sub),
		sub.FallbackTag,
		desired,
		sub.Prepend,
	); err != nil {
		logger.Warningf("outbound sub %d: template sync failed: %v", sub.Id, err)
		return common.NewError("template sync:", err)
	}

	if !UsesRuntimeSync(sub) {
		return nil
	}

	if apiPort <= 0 {
		logger.Infof("outbound sub %d: saved to template; restart xray to apply", sub.Id)
		return common.NewError("xray is not running; restart xray to apply")
	}

	api := xray.XrayAPI{}
	if err := api.Init(apiPort); err != nil {
		logger.Infof("outbound sub %d: saved to template; restart xray to apply live", sub.Id)
		return common.NewError("xray gRPC API is not available; restart xray to apply")
	}
	defer api.Close()

	existing, err := api.ListOutboundTags()
	if err != nil {
		logger.Warningf("outbound sub %d: ListOutboundTags: %v (continuing with desired tags only)", sub.Id, err)
		existing = nil
	}

	desiredTags := map[string]map[string]any{}
	for _, ob := range desired {
		tag, _ := ob["tag"].(string)
		if tag == "" {
			continue
		}
		desiredTags[tag] = ob
	}

	matchPrefix := strings.TrimSpace(prefix)
	if matchPrefix != "" && !strings.HasSuffix(matchPrefix, "-") {
		matchPrefix = matchPrefix + "-"
	}
	for _, tag := range existing {
		if matchPrefix != "" && !strings.HasPrefix(tag, matchPrefix) {
			continue
		}
		if matchPrefix == "" {
			continue
		}
		if _, ok := desiredTags[tag]; !ok {
			if err := api.RemoveOutbound(tag); err != nil {
				logger.Warningf("outbound sub %d: remove %s: %v", sub.Id, tag, err)
			}
		}
	}

	for tag, ob := range desiredTags {
		raw, err := json.Marshal(ob)
		if err != nil {
			continue
		}
		_ = api.RemoveOutbound(tag)
		if err := api.AddOutboundFromJSON(raw); err != nil {
			logger.Warningf("outbound sub %d: add %s: %v", sub.Id, tag, err)
		}
	}

	if err := api.EnsureBalancer(sub.BalancerTag, outboundTags, effectiveBalancerStrategy(sub), sub.FallbackTag); err != nil {
		logger.Warningf("outbound sub %d: live balancer gRPC failed: %v (template updated)", sub.Id, err)
		return common.NewError("balancer live sync failed; restart xray:", err)
	}
	return nil
}

// RemoveRuntimeOutbounds drops runtime outbounds and cleans template entries.
func (s *OutboundSubscriptionService) RemoveRuntimeOutbounds(sub *model.OutboundSubscription, apiPort int) error {
	if sub == nil {
		return nil
	}
	prefix := effectiveTagPrefix(sub)
	_ = removePrefixedOutboundsFromTemplate(&s.settingService, prefix, sub.BalancerTag)

	if apiPort <= 0 {
		return nil
	}
	api := xray.XrayAPI{}
	if err := api.Init(apiPort); err != nil {
		return err
	}
	defer api.Close()

	tags, err := api.ListOutboundTags()
	if err != nil {
		return err
	}
	matchPrefix := strings.TrimSpace(prefix)
	if matchPrefix != "" && !strings.HasSuffix(matchPrefix, "-") {
		matchPrefix = matchPrefix + "-"
	}
	for _, tag := range tags {
		if matchPrefix != "" && strings.HasPrefix(tag, matchPrefix) {
			if err := api.RemoveOutbound(tag); err != nil {
				logger.Warningf("outbound sub %d: remove %s: %v", sub.Id, tag, err)
			}
		}
	}
	return nil
}

// RemoveBalancerOnly drops a balancer entry from the xray template (outbounds untouched).
func (s *OutboundSubscriptionService) RemoveBalancerOnly(balancerTag string) error {
	return removeBalancerOnlyFromTemplate(&s.settingService, balancerTag)
}

// SyncAllRuntime applies runtime sync for enabled subscriptions with a balancer name.
// Returns true when xray should restart to pick up balancer changes from template.
func (s *OutboundSubscriptionService) SyncAllRuntime(apiPort int) bool {
	db := database.GetDB()
	var subs []*model.OutboundSubscription
	if err := db.Where("enabled = ? AND balancer_tag <> ''", true).Find(&subs).Error; err != nil {
		return false
	}
	needRestart := false
	for _, sub := range subs {
		if err := s.ApplyRuntimeSync(sub, apiPort); err != nil {
			logger.Warningf("outbound sub %d runtime sync: %v", sub.Id, err)
			needRestart = true
		}
	}
	return needRestart
}
