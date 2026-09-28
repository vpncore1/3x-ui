package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"
	routerCommand "github.com/xtls/xray-core/app/router/command"
	routerConf "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AddOutboundFromJSON registers an outbound handler at runtime via gRPC.
func (x *XrayAPI) AddOutboundFromJSON(outboundJSON []byte) error {
	if x.HandlerServiceClient == nil {
		return fmt.Errorf("handler service not initialized")
	}
	client := *x.HandlerServiceClient

	detour := new(conf.OutboundDetourConfig)
	if err := json.Unmarshal(outboundJSON, detour); err != nil {
		return fmt.Errorf("invalid outbound json: %w", err)
	}
	if strings.TrimSpace(detour.Tag) == "" {
		return fmt.Errorf("outbound tag is required")
	}
	built, err := detour.Build()
	if err != nil {
		return fmt.Errorf("build outbound %q: %w", detour.Tag, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = client.AddOutbound(ctx, &command.AddOutboundRequest{Outbound: built})
	return err
}

// RemoveOutbound removes a runtime outbound by tag.
func (x *XrayAPI) RemoveOutbound(tag string) error {
	if x.HandlerServiceClient == nil {
		return fmt.Errorf("handler service not initialized")
	}
	client := *x.HandlerServiceClient
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.RemoveOutbound(ctx, &command.RemoveOutboundRequest{Tag: tag})
	return err
}

// ListOutboundTags returns tags of all non-API outbounds currently loaded in xray.
func (x *XrayAPI) ListOutboundTags() ([]string, error) {
	if x.HandlerServiceClient == nil {
		return nil, fmt.Errorf("handler service not initialized")
	}
	client := *x.HandlerServiceClient
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.ListOutbounds(ctx, &command.ListOutboundsRequest{})
	if err != nil {
		// Older HandlerService builds may lack ListOutbounds — caller can still add/remove by known tags.
		return nil, err
	}
	tags := make([]string, 0, len(resp.GetOutbounds()))
	for _, ob := range resp.GetOutbounds() {
		if tag := ob.GetTag(); tag != "" && tag != "api" {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// EnsureBalancer creates or refreshes a balancer pool with explicit outbound tags.
// Does not create routing rules â€” the user configures those in the panel.
func (x *XrayAPI) EnsureBalancer(balancerTag string, outboundTags []string, strategy, fallbackTag string) error {
	if x.RoutingServiceClient == nil {
		return fmt.Errorf("routing service not initialized")
	}
	if strings.TrimSpace(balancerTag) == "" {
		return fmt.Errorf("balancerTag is required")
	}
	if len(outboundTags) == 0 {
		return fmt.Errorf("balancer %q needs at least one outbound tag", balancerTag)
	}
	if strings.TrimSpace(strategy) == "" {
		strategy = "roundRobin"
	}

	client := *x.RoutingServiceClient
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Drop legacy auto-routing rules from older patch versions.
	if listed, err := client.ListRule(ctx, &routerCommand.ListRuleRequest{}); err == nil {
		prefix := fmt.Sprintf("rule-%s-inbound-", balancerTag)
		for _, r := range listed.GetRules() {
			if strings.HasPrefix(r.GetRuleTag(), prefix) {
				_, _ = client.RemoveRule(ctx, &routerCommand.RemoveRuleRequest{RuleTag: r.GetRuleTag()})
			}
		}
	}

	routerCfg := &routerConf.Config{
		BalancingRule: []*routerConf.BalancingRule{
			{
				Tag:              balancerTag,
				OutboundSelector: outboundTags,
				Strategy:         strategy,
				FallbackTag:      fallbackTag,
			},
		},
	}

	req := &routerCommand.AddRuleRequest{
		Config:       serial.ToTypedMessage(routerCfg),
		ShouldAppend: true,
	}
	_, err := client.AddRule(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return nil
		}
		logger.Warningf("EnsureBalancer AddRule tag=%q: %v", balancerTag, err)
	}
	return err
}

