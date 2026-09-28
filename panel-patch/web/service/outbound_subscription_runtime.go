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

// ApplyRuntimeSync fetches subscription outbounds into xray and updates the balancer pool.
// Routing rules are not touched â€” configure them manually in the panel.
func (s *OutboundSubscriptionService) ApplyRuntimeSync(sub *model.OutboundSubscription, apiPort int) error {
	if !UsesRuntimeSync(sub) {
		return nil
	}

	prefix := effectiveTagPrefix(sub)

	var desired []map[string]any
	if strings.TrimSpace(sub.LastFetchedOutbounds) != "" {
		if err := json.Unmarshal([]byte(sub.LastFetchedOutbounds), &desired); err != nil {
			return err
		}
	}

	outboundTags := make([]string, 0, len(desired))
	for _, ob := range desired {
		if tag, _ := ob["tag"].(string); strings.TrimSpace(tag) != "" {
			outboundTags = append(outboundTags, tag)
		}
	}
	if len(outboundTags) == 0 {
		return common.NewError("no outbound tags in subscription")
	}

	// Persist balancer in template first so it survives xray restarts even when gRPC is down.
	if err := ensureBalancerInTemplate(&s.settingService, sub.BalancerTag, outboundTags, effectiveBalancerStrategy(sub), sub.FallbackTag); err != nil {
		logger.Warningf("outbound sub %d: template balancer persist failed: %v", sub.Id, err)
		return common.NewError("balancer template:", err)
	}

	if apiPort <= 0 {
		logger.Infof("outbound sub %d: balancer saved to template; restart xray to apply", sub.Id)
		return common.NewError("xray is not running; restart xray to apply balancer")
	}

	api := xray.XrayAPI{}
	if err := api.Init(apiPort); err != nil {
		logger.Infof("outbound sub %d: balancer saved to template; restart xray to apply live", sub.Id)
		return common.NewError("xray gRPC API is not available; restart xray to apply balancer")
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

	for _, tag := range existing {
		if !strings.HasPrefix(tag, prefix) {
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

// RemoveRuntimeOutbounds drops all outbounds with this subscription's tag prefix from xray.
func (s *OutboundSubscriptionService) RemoveRuntimeOutbounds(sub *model.OutboundSubscription, apiPort int) error {
	if !UsesRuntimeSync(sub) || apiPort <= 0 {
		return nil
	}
	prefix := effectiveTagPrefix(sub)
	api := xray.XrayAPI{}
	if err := api.Init(apiPort); err != nil {
		return err
	}
	defer api.Close()

	tags, err := api.ListOutboundTags()
	if err != nil {
		return err
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, prefix) {
			if err := api.RemoveOutbound(tag); err != nil {
				logger.Warningf("outbound sub %d: remove %s: %v", sub.Id, tag, err)
			}
		}
	}
	return nil
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

