#!/usr/bin/env python3
"""Apply panel-patch overlay onto a 3x-ui v2.9.0 source tree.

Backports Outbound Subscriptions from v3.3.0 + balancer/runtime sync.
"""

from __future__ import annotations

import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/opt/3x-ui-build")
PATCH = Path(__file__).resolve().parent


def copy_overlay() -> None:
    files = [
        "util/link/outbound.go",
        "web/service/outbound_subscription.go",
        "web/service/outbound_subscription_runtime.go",
        "web/service/outbound_subscription_balancer.go",
        "web/service/url_safety.go",
        "web/service/panel_self_update.go",
        "web/service/panel_self_update_linux.go",
        "web/service/panel_self_update_other.go",
        "web/job/outbound_subscription_job.go",
        "xray/outbound_runtime.go",
    ]
    for rel in files:
        src = PATCH / rel
        dst = ROOT / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)


def patch_api_go() -> None:
    path = ROOT / "xray/api.go"
    text = path.read_text(encoding="utf-8")
    if "RoutingServiceClient" in text:
        return
    text = text.replace(
        '\t"github.com/xtls/xray-core/app/proxyman/command"\n',
        '\t"github.com/xtls/xray-core/app/proxyman/command"\n'
        '\trouterCommand "github.com/xtls/xray-core/app/router/command"\n',
    )
    text = text.replace(
        "\tHandlerServiceClient *command.HandlerServiceClient\n",
        "\tHandlerServiceClient  *command.HandlerServiceClient\n"
        "\tRoutingServiceClient  *routerCommand.RoutingServiceClient\n",
    )
    text = text.replace(
        "\tx.HandlerServiceClient = &hsClient\n",
        "\tx.HandlerServiceClient = &hsClient\n\n"
        "\trClient := routerCommand.NewRoutingServiceClient(conn)\n"
        "\tx.RoutingServiceClient = &rClient\n",
    )
    text = text.replace(
        "\tx.HandlerServiceClient = nil\n",
        "\tx.HandlerServiceClient = nil\n"
        "\tx.RoutingServiceClient = nil\n",
    )
    path.write_text(text, encoding="utf-8")


def patch_model() -> None:
    path = ROOT / "database/model/model.go"
    text = path.read_text(encoding="utf-8")
    if "type OutboundSubscription struct" in text:
        if "BalancerTag" not in text:
            raise RuntimeError("OutboundSubscription exists without BalancerTag")
        return
    block = '''
// OutboundSubscription stores a remote subscription URL whose outbounds are
// merged into the Xray config (with optional named balancer + runtime sync).
type OutboundSubscription struct {
	Id                   int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Remark               string `json:"remark" form:"remark"`
	Url                  string `json:"url" form:"url"`
	Enabled              bool   `json:"enabled" form:"enabled" gorm:"default:true"`
	AllowPrivate         bool   `json:"allowPrivate" form:"allowPrivate" gorm:"default:false"`
	TagPrefix            string `json:"tagPrefix" form:"tagPrefix"`
	BalancerTag          string `json:"balancerTag" form:"balancerTag"`
	TargetInboundTag     string `json:"targetInboundTag" form:"targetInboundTag"`
	BalancerStrategy     string `json:"balancerStrategy" form:"balancerStrategy" gorm:"default:roundRobin"`
	FallbackTag          string `json:"fallbackTag" form:"fallbackTag"`
	UpdateInterval       int    `json:"updateInterval" form:"updateInterval" gorm:"default:600"`
	Priority             int    `json:"priority" form:"priority" gorm:"default:0"`
	Prepend              bool   `json:"prepend" form:"prepend" gorm:"default:false"`
	LastUpdated          int64  `json:"lastUpdated" form:"lastUpdated"`
	LastError            string `json:"lastError" form:"lastError"`
	LastFetchedOutbounds string `json:"lastFetchedOutbounds" form:"lastFetchedOutbounds" gorm:"type:text"`
	LinkIdentities       string `json:"-" gorm:"type:text;column:link_identities"`
	CreatedAt            int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt            int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
	OutboundCount        int    `json:"outboundCount" gorm:"-"`
}
'''
    # Append before end of file
    text = text.rstrip() + "\n" + block + "\n"
    path.write_text(text, encoding="utf-8")


def patch_db() -> None:
    path = ROOT / "database/db.go"
    text = path.read_text(encoding="utf-8")
    if "OutboundSubscription{}" in text:
        return
    text = text.replace(
        "\t\t&model.CustomGeoResource{},\n",
        "\t\t&model.CustomGeoResource{},\n"
        "\t\t&model.OutboundSubscription{},\n",
    )
    if "OutboundSubscription{}" not in text:
        text = text.replace(
            "\t\t&model.HistoryOfSeeders{},\n",
            "\t\t&model.HistoryOfSeeders{},\n"
            "\t\t&model.OutboundSubscription{},\n",
        )
    path.write_text(text, encoding="utf-8")


def patch_xray_service() -> None:
    path = ROOT / "web/service/xray.go"
    text = path.read_text(encoding="utf-8")

    if "GetXrayAPIPort" not in text:
        text = text.rstrip() + """

// GetXrayAPIPort returns the local xray gRPC API port, or 0 if not running.
func (s *XrayService) GetXrayAPIPort() int {
	if p == nil || !p.IsRunning() {
		return 0
	}
	return p.GetAPIPort()
}
"""

    if "mergeSubscriptionOutbounds" not in text:
        if '"encoding/json"' not in text:
            text = text.replace(
                "import (\n",
                'import (\n\t"encoding/json"\n',
                1,
            )
        if "json_util" not in text:
            text = text.replace(
                '"github.com/mhsanaei/3x-ui/v2/xray"\n',
                '"github.com/mhsanaei/3x-ui/v2/util/json_util"\n'
                '\t"github.com/mhsanaei/3x-ui/v2/xray"\n',
            )
        merge_fn = '''
// mergeSubscriptionOutbounds injects subscription outbounds around template outbounds.
// Tags already present in the template (after template sync) are skipped to avoid duplicates.
func mergeSubscriptionOutbounds(cfg *xray.Config, prepend, appendList []any) {
	if len(prepend) == 0 && len(appendList) == 0 {
		return
	}
	var templateOutbounds []any
	if len(cfg.OutboundConfigs) > 0 {
		if err := json.Unmarshal(cfg.OutboundConfigs, &templateOutbounds); err != nil {
			return
		}
	}
	existing := map[string]bool{}
	for _, item := range templateOutbounds {
		if m, ok := item.(map[string]any); ok {
			if tag, _ := m["tag"].(string); tag != "" {
				existing[tag] = true
			}
		}
	}
	filterNew := func(list []any) []any {
		out := make([]any, 0, len(list))
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			tag, _ := m["tag"].(string)
			if tag == "" || existing[tag] {
				continue
			}
			existing[tag] = true
			out = append(out, item)
		}
		return out
	}
	prepend = filterNew(prepend)
	appendList = filterNew(appendList)
	if len(prepend) == 0 && len(appendList) == 0 {
		return
	}
	merged := make([]any, 0, len(prepend)+len(templateOutbounds)+len(appendList))
	merged = append(merged, prepend...)
	merged = append(merged, templateOutbounds...)
	merged = append(merged, appendList...)
	combined, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return
	}
	cfg.OutboundConfigs = json_util.RawMessage(combined)
}
'''
        text = text.rstrip() + "\n" + merge_fn + "\n"

        # Inject merge call before final return of GetXrayConfig
        old = "\t\tinboundConfig := inbound.GenXrayInboundConfig()\n\t\txrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, *inboundConfig)\n\t}\n\treturn xrayConfig, nil\n}"
        new = (
            "\t\tinboundConfig := inbound.GenXrayInboundConfig()\n"
            "\t\txrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, *inboundConfig)\n"
            "\t}\n\n"
            "\tsubSvc := &OutboundSubscriptionService{}\n"
            "\tif prepend, appendList, err := subSvc.activeOutboundsSplit(); err == nil && (len(prepend) > 0 || len(appendList) > 0) {\n"
            "\t\tmergeSubscriptionOutbounds(xrayConfig, prepend, appendList)\n"
            "\t}\n\n"
            "\treturn xrayConfig, nil\n}"
        )
        if old not in text:
            raise RuntimeError("patch_xray_service: GetXrayConfig return anchor not found")
        text = text.replace(old, new, 1)

    path.write_text(text, encoding="utf-8")


def patch_web_cron() -> None:
    path = ROOT / "web/web.go"
    text = path.read_text(encoding="utf-8")
    if "NewOutboundSubscriptionJob" in text:
        return
    text = text.replace(
        '\ts.cron.AddJob("@every 10s", job.NewCheckClientIpJob())\n',
        '\ts.cron.AddJob("@every 10s", job.NewCheckClientIpJob())\n'
        '\ts.cron.AddJob("@every 1m", job.NewOutboundSubscriptionJob())\n',
    )
    path.write_text(text, encoding="utf-8")


CONTROLLER_HANDLERS = r'''
func parseBalancerSubFields(c *gin.Context) (balancerTag, targetInboundTag, balancerStrategy, fallbackTag string) {
	balancerTag = strings.TrimSpace(c.PostForm("balancerTag"))
	targetInboundTag = strings.TrimSpace(c.PostForm("targetInboundTag"))
	balancerStrategy = strings.TrimSpace(c.PostForm("balancerStrategy"))
	fallbackTag = strings.TrimSpace(c.PostForm("fallbackTag"))
	if balancerStrategy == "" {
		balancerStrategy = "roundRobin"
	}
	return
}

func (a *XraySettingController) afterOutboundSubChange(subID int) {
	sub, err := a.OutboundSubscriptionService.Get(subID)
	if err != nil {
		a.XrayService.SetToNeedRestart()
		return
	}
	if !sub.Enabled {
		_ = a.OutboundSubscriptionService.RemoveRuntimeOutbounds(sub, a.XrayService.GetXrayAPIPort())
		a.XrayService.SetToNeedRestart()
		return
	}
	// Always write outbounds into the xray template so they appear under Outbounds.
	if err := a.OutboundSubscriptionService.ApplyRuntimeSync(sub, a.XrayService.GetXrayAPIPort()); err != nil {
		logger.Warningf("outbound sub %d sync failed: %v", subID, err)
		a.XrayService.SetToNeedRestart()
		return
	}
	if !service.UsesRuntimeSync(sub) {
		a.XrayService.SetToNeedRestart()
	}
}

func (a *XraySettingController) listOutboundSubs(c *gin.Context) {
	list, err := a.OutboundSubscriptionService.List()
	if err != nil {
		jsonMsg(c, "Failed to list outbound subscriptions", err)
		return
	}
	jsonObj(c, list, nil)
}

func (a *XraySettingController) createOutboundSub(c *gin.Context) {
	remark := c.PostForm("remark")
	rawURL := c.PostForm("url")
	prefix := c.PostForm("tagPrefix")
	enabled := c.PostForm("enabled") != "false"
	allowPrivate := c.PostForm("allowPrivate") == "true"
	prepend := c.PostForm("prepend") == "true"
	interval, _ := strconv.Atoi(c.PostForm("updateInterval"))
	if interval <= 0 {
		interval = 600
	}
	bt, tit, bs, fb := parseBalancerSubFields(c)
	sub, err := a.OutboundSubscriptionService.Create(remark, rawURL, prefix, enabled, interval, allowPrivate, prepend, bt, tit, bs, fb)
	if err != nil {
		jsonMsg(c, "Failed to create outbound subscription", err)
		return
	}
	if _, err := a.OutboundSubscriptionService.Refresh(sub.Id); err != nil {
		logger.Warningf("outbound sub %d initial refresh: %v", sub.Id, err)
	}
	a.afterOutboundSubChange(sub.Id)
	jsonObj(c, sub, nil)
}

func (a *XraySettingController) updateOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	before, _ := a.OutboundSubscriptionService.Get(subID)
	remark := c.PostForm("remark")
	rawURL := c.PostForm("url")
	prefix := c.PostForm("tagPrefix")
	enabled := c.PostForm("enabled") != "false"
	allowPrivate := c.PostForm("allowPrivate") == "true"
	prepend := c.PostForm("prepend") == "true"
	interval, _ := strconv.Atoi(c.PostForm("updateInterval"))
	if interval <= 0 {
		interval = 600
	}
	bt, tit, bs, fb := parseBalancerSubFields(c)
	if err := a.OutboundSubscriptionService.Update(subID, remark, rawURL, prefix, enabled, interval, allowPrivate, prepend, bt, tit, bs, fb); err != nil {
		jsonMsg(c, "Failed to update outbound subscription", err)
		return
	}
	// Balancer name removed: drop the old balancer from the template.
	if before != nil && strings.TrimSpace(before.BalancerTag) != "" && strings.TrimSpace(bt) == "" {
		_ = a.OutboundSubscriptionService.RemoveBalancerOnly(before.BalancerTag)
	}
	if enabled {
		if _, err := a.OutboundSubscriptionService.Refresh(subID); err != nil {
			logger.Warningf("outbound sub %d refresh on update: %v", subID, err)
		}
	}
	a.afterOutboundSubChange(subID)
	jsonObj(c, "", nil)
}

func (a *XraySettingController) deleteOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	subBeforeDel, _ := a.OutboundSubscriptionService.Get(subID)
	if err := a.OutboundSubscriptionService.Delete(subID); err != nil {
		jsonMsg(c, "Failed to delete outbound subscription", err)
		return
	}
	if subBeforeDel != nil {
		_ = a.OutboundSubscriptionService.RemoveRuntimeOutbounds(subBeforeDel, a.XrayService.GetXrayAPIPort())
	}
	a.XrayService.SetToNeedRestart()
	jsonObj(c, "", nil)
}

func (a *XraySettingController) refreshOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	obs, err := a.OutboundSubscriptionService.Refresh(subID)
	if err != nil {
		jsonMsg(c, "Refresh failed", err)
		return
	}
	a.afterOutboundSubChange(subID)
	jsonObj(c, obs, nil)
}

func (a *XraySettingController) moveOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	up := c.PostForm("dir") == "up"
	sub, _ := a.OutboundSubscriptionService.Get(subID)
	if err := a.OutboundSubscriptionService.Move(subID, up); err != nil {
		jsonMsg(c, "Failed to reorder outbound subscription", err)
		return
	}
	if sub == nil || !service.UsesRuntimeSync(sub) {
		a.XrayService.SetToNeedRestart()
	}
	jsonObj(c, "", nil)
}

func (a *XraySettingController) parseOutboundSubURL(c *gin.Context) {
	rawURL := c.PostForm("url")
	allowPrivate := c.PostForm("allowPrivate") == "true"
	svc := service.OutboundSubscriptionService{}
	tmp, err := svc.Create("preview", rawURL, "", false, 600, allowPrivate, false, "", "", "", "")
	if err != nil {
		jsonMsg(c, "Parse failed", err)
		return
	}
	obs, err := svc.Refresh(tmp.Id)
	_ = svc.Delete(tmp.Id)
	if err != nil {
		jsonMsg(c, "Parse failed", err)
		return
	}
	jsonObj(c, obs, nil)
}
'''


def patch_controller() -> None:
    path = ROOT / "web/controller/xray_setting.go"
    text = path.read_text(encoding="utf-8")

    # imports
    if '"fmt"' not in text:
        text = text.replace(
            '"encoding/json"\n',
            '"encoding/json"\n\t"fmt"\n\t"strconv"\n\t"strings"\n',
        )
    if '"github.com/mhsanaei/3x-ui/v2/logger"' not in text:
        text = text.replace(
            '"github.com/mhsanaei/3x-ui/v2/util/common"\n',
            '"github.com/mhsanaei/3x-ui/v2/logger"\n'
            '\t"github.com/mhsanaei/3x-ui/v2/util/common"\n',
        )

    if "OutboundSubscriptionService" not in text:
        text = text.replace(
            "\tXrayService        service.XrayService\n",
            "\tXrayService                 service.XrayService\n"
            "\tOutboundSubscriptionService service.OutboundSubscriptionService\n",
        )

    if "outbound-subs" not in text:
        text = text.replace(
            '\tg.POST("/testOutbound", a.testOutbound)\n',
            '\tg.POST("/testOutbound", a.testOutbound)\n\n'
            '\tg.GET("/outbound-subs", a.listOutboundSubs)\n'
            '\tg.POST("/outbound-subs", a.createOutboundSub)\n'
            '\tg.POST("/outbound-subs/:id/refresh", a.refreshOutboundSub)\n'
            '\tg.POST("/outbound-subs/:id/move", a.moveOutboundSub)\n'
            '\tg.POST("/outbound-subs/:id", a.updateOutboundSub)\n'
            '\tg.DELETE("/outbound-subs/:id", a.deleteOutboundSub)\n'
            '\tg.POST("/outbound-subs/:id/del", a.deleteOutboundSub)\n'
            '\tg.POST("/outbound-subs/parse", a.parseOutboundSubURL)\n',
        )

    # Surface subscription outbounds in getXraySetting response
    if "subscriptionOutbounds" not in text:
        text = text.replace(
            '\txrayResponse := map[string]interface{}{\n'
            '\t\t"xraySetting":     json.RawMessage(xraySetting),\n'
            '\t\t"inboundTags":     json.RawMessage(inboundTags),\n'
            '\t\t"outboundTestUrl": outboundTestUrl,\n'
            "\t}",
            '\txrayResponse := map[string]interface{}{\n'
            '\t\t"xraySetting":     json.RawMessage(xraySetting),\n'
            '\t\t"inboundTags":     json.RawMessage(inboundTags),\n'
            '\t\t"outboundTestUrl": outboundTestUrl,\n'
            "\t}\n"
            "\tif subObs, err := a.OutboundSubscriptionService.AllActiveOutbounds(); err == nil && len(subObs) > 0 {\n"
            '\t\txrayResponse["subscriptionOutbounds"] = subObs\n'
            "\t}\n"
            "\tif subTags, err := a.OutboundSubscriptionService.AllActiveOutboundTags(); err == nil && len(subTags) > 0 {\n"
            '\t\txrayResponse["subscriptionOutboundTags"] = subTags\n'
            "\t}",
        )

    if "func (a *XraySettingController) listOutboundSubs" not in text:
        text = text.rstrip() + "\n" + CONTROLLER_HANDLERS + "\n"

    path.write_text(text, encoding="utf-8")


def patch_vue_outbounds() -> None:
    path = ROOT / "web/html/settings/xray/outbounds.html"
    text = path.read_text(encoding="utf-8")
    if "showOutboundSubs" in text:
        return
    inject = '''
    <a-row>
        <a-col :xs="24" :sm="24" :lg="24">
            <a-button type="default" icon="cloud-download" @click="showOutboundSubs">
                Subscriptions
            </a-button>
            <a-tag v-if="outboundSubs && outboundSubs.length" color="blue" :style="{ marginLeft: '8px' }">
                [[ outboundSubs.length ]] sub(s)
            </a-tag>
        </a-col>
    </a-row>

    <a-modal :title="'Outbound Subscriptions'" v-model="subModalVisible"
        :footer="null" :width="isMobile ? '100%' : 720"
        :class="themeSwitcher.currentTheme">
        <a-form layout="vertical">
            <a-form-item label="Remark">
                <a-input v-model="subForm.remark" placeholder="e.g. providers"></a-input>
            </a-form-item>
            <a-form-item label="Subscription URL" required>
                <a-input v-model="subForm.url" placeholder="https://..."></a-input>
            </a-form-item>
            <a-form-item label="Balancer name">
                <a-input v-model="subForm.balancerTag" placeholder="e.g. sub1"
                    @change="onSubBalancerChange"></a-input>
            </a-form-item>
            <a-form-item label="Tag prefix">
                <a-input v-model="subForm.tagPrefix" placeholder="auto from balancer"></a-input>
            </a-form-item>
            <a-form-item label="Balancer strategy">
                <a-select v-model="subForm.balancerStrategy" :dropdown-class-name="themeSwitcher.currentTheme">
                    <a-select-option value="roundRobin">roundRobin</a-select-option>
                    <a-select-option value="random">random</a-select-option>
                    <a-select-option value="leastPing">leastPing</a-select-option>
                    <a-select-option value="leastLoad">leastLoad</a-select-option>
                </a-select>
            </a-form-item>
            <a-form-item label="Fallback outbound">
                <a-input v-model="subForm.fallbackTag" placeholder="direct (optional)"></a-input>
            </a-form-item>
            <a-form-item label="Update interval (minutes)">
                <a-input-number v-model="subForm.updateIntervalMin" :min="1" :max="10080"></a-input-number>
            </a-form-item>
            <a-form-item>
                <a-checkbox v-model="subForm.enabled">Enabled</a-checkbox>
                <a-checkbox v-model="subForm.prepend" :style="{ marginLeft: '12px' }">Prepend outbounds</a-checkbox>
            </a-form-item>
            <a-space>
                <a-button type="primary" :loading="subSaving" @click="saveOutboundSub">
                    [[ subForm.id ? 'Update' : 'Add' ]]
                </a-button>
                <a-button @click="resetSubForm" v-if="subForm.id">Cancel edit</a-button>
            </a-space>
            <p :style="{ marginTop: '8px', opacity: 0.75 }">
                After save/refresh, configs appear under Outbounds. If Balancer name is set, add a Routing rule with balancerTag = that name, then Restart Xray.
            </p>
        </a-form>
        <a-divider></a-divider>
        <a-table :columns="subColumns" :data-source="outboundSubs" :pagination="false"
            :row-key="r => r.id" size="small" :scroll="isMobile ? {} : { x: 600 }">
            <template slot="actions" slot-scope="text, row">
                <a-button size="small" icon="sync" :loading="subRefreshingId===row.id"
                    @click="refreshOutboundSub(row.id)"></a-button>
                <a-button size="small" icon="edit" @click="editOutboundSub(row)"></a-button>
                <a-popconfirm title="Delete subscription?" @confirm="deleteOutboundSub(row.id)"
                    :overlay-class-name="themeSwitcher.currentTheme"
                    ok-text="Yes" cancel-text="No">
                    <a-button size="small" icon="delete" type="danger"></a-button>
                </a-popconfirm>
            </template>
            <template slot="balancer" slot-scope="text, row">
                <a-tag v-if="row.balancerTag" color="blue">[[ row.balancerTag ]]</a-tag>
                <span v-else>-</span>
            </template>
            <template slot="count" slot-scope="text, row">
                [[ row.outboundCount || 0 ]]
            </template>
        </a-table>
    </a-modal>
'''
    # Insert after first a-space / opening row block
    anchor = '    <a-row>\n        <a-col :xs="12" :sm="12" :lg="12">\n            <a-space direction="horizontal" size="small">\n                <a-button type="primary" icon="plus" @click="addOutbound">'
    if anchor not in text:
        raise RuntimeError("patch_vue_outbounds: anchor not found")
    text = text.replace(anchor, inject + "\n" + anchor, 1)
    path.write_text(text, encoding="utf-8")


def patch_vue_xray_js() -> None:
    path = ROOT / "web/html/xray.html"
    text = path.read_text(encoding="utf-8")
    if "showOutboundSubs" in text:
        return

    # data fields — find data() return object near outboundTestStates
    data_inject = """
      outboundSubs: [],
      subModalVisible: false,
      subSaving: false,
      subRefreshingId: 0,
      subForm: {
        id: null, remark: '', url: '', tagPrefix: '', balancerTag: '',
        balancerStrategy: 'roundRobin', fallbackTag: '', updateIntervalMin: 10,
        enabled: true, prepend: false, allowPrivate: false
      },
      subColumns: [
        { title: '#', dataIndex: 'id', width: 50 },
        { title: 'Remark', dataIndex: 'remark' },
        { title: 'Balancer', key: 'balancer', scopedSlots: { customRender: 'balancer' } },
        { title: 'Count', key: 'count', scopedSlots: { customRender: 'count' }, width: 70 },
        { title: '', key: 'actions', scopedSlots: { customRender: 'actions' }, width: 140 },
      ],
"""
    # Insert after loadingStates or similar in data
    m = re.search(r"outboundTestStates:\s*\{\},?", text)
    if not m:
        m = re.search(r"outboundsTraffic:\s*\[\],?", text)
    if not m:
        raise RuntimeError("patch_vue_xray_js: data anchor not found")
    insert_at = m.end()
    text = text[:insert_at] + "\n" + data_inject + text[insert_at:]

    methods = r'''
      showOutboundSubs() {
        this.subModalVisible = true;
        this.loadOutboundSubs();
      },
      resetSubForm() {
        this.subForm = {
          id: null, remark: '', url: '', tagPrefix: '', balancerTag: '',
          balancerStrategy: 'roundRobin', fallbackTag: '', updateIntervalMin: 10,
          enabled: true, prepend: false, allowPrivate: false
        };
      },
      onSubBalancerChange() {
        const v = (this.subForm.balancerTag || '').trim();
        if (v && !(this.subForm.tagPrefix || '').trim()) {
          this.subForm.tagPrefix = v + '-';
        }
      },
      async loadOutboundSubs() {
        const msg = await HttpUtil.get("/panel/xray/outbound-subs");
        if (msg.success) {
          this.outboundSubs = Array.isArray(msg.obj) ? msg.obj : [];
        }
      },
      editOutboundSub(row) {
        this.subForm = {
          id: row.id,
          remark: row.remark || '',
          url: row.url || '',
          tagPrefix: row.tagPrefix || '',
          balancerTag: row.balancerTag || '',
          balancerStrategy: row.balancerStrategy || 'roundRobin',
          fallbackTag: row.fallbackTag || '',
          updateIntervalMin: Math.max(1, Math.round((row.updateInterval || 600) / 60)),
          enabled: row.enabled !== false,
          prepend: !!row.prepend,
          allowPrivate: !!row.allowPrivate
        };
      },
      async saveOutboundSub() {
        if (!(this.subForm.url || '').trim()) {
          Vue.prototype.$message.warning('URL required');
          return;
        }
        this.subSaving = true;
        const body = {
          remark: this.subForm.remark || '',
          url: this.subForm.url.trim(),
          tagPrefix: this.subForm.tagPrefix || '',
          balancerTag: this.subForm.balancerTag || '',
          balancerStrategy: this.subForm.balancerStrategy || 'roundRobin',
          fallbackTag: this.subForm.fallbackTag || '',
          updateInterval: String(Math.max(60, (this.subForm.updateIntervalMin || 10) * 60)),
          enabled: this.subForm.enabled ? 'true' : 'false',
          prepend: this.subForm.prepend ? 'true' : 'false',
          allowPrivate: this.subForm.allowPrivate ? 'true' : 'false'
        };
        let msg;
        if (this.subForm.id) {
          msg = await HttpUtil.post("/panel/xray/outbound-subs/" + this.subForm.id, body);
        } else {
          msg = await HttpUtil.post("/panel/xray/outbound-subs", body);
        }
        this.subSaving = false;
        if (msg.success) {
          this.resetSubForm();
          await this.loadOutboundSubs();
          await this.getXraySetting();
        }
      },
      async refreshOutboundSub(id) {
        this.subRefreshingId = id;
        const msg = await HttpUtil.post("/panel/xray/outbound-subs/" + id + "/refresh");
        this.subRefreshingId = 0;
        if (msg.success) {
          await this.loadOutboundSubs();
          await this.getXraySetting();
        }
      },
      async deleteOutboundSub(id) {
        const msg = await HttpUtil.post("/panel/xray/outbound-subs/" + id + "/del");
        if (msg.success) {
          await this.loadOutboundSubs();
          await this.getXraySetting();
        }
      },
'''
    text = text.replace("    methods: {\n", "    methods: {\n" + methods, 1)

    # Load subs when settings fetched
    text = text.replace(
        "          this.outboundTestUrl = result.outboundTestUrl || 'https://www.google.com/generate_204';\n"
        "          this.oldOutboundTestUrl = this.outboundTestUrl;\n"
        "          this.saveBtnDisable = true;",
        "          this.outboundTestUrl = result.outboundTestUrl || 'https://www.google.com/generate_204';\n"
        "          this.oldOutboundTestUrl = this.outboundTestUrl;\n"
        "          this.saveBtnDisable = true;\n"
        "          this.loadOutboundSubs();",
    )

    path.write_text(text, encoding="utf-8")


def patch_translations() -> None:
    for code, pairs in (
        ("en_US", {
            "outboundSubTitle": "Outbound Subscriptions",
            "outboundSubBalancer": "Balancer name",
            "outboundSubHint": "After save, configs appear under Outbounds. With balancer name, add Routing rule balancerTag then Restart Xray.",
            "panelUpdate": "Update Panel",
            "panelUpdateCheck": "Check Update",
            "panelUpdateConfirm": "Update panel from vpncore1/3x-ui now? The panel will restart. This never runs automatically.",
            "panelUpdateStarted": "Update started. Wait 1–3 minutes then refresh.",
            "panelUpdateAvailable": "Update available",
            "panelUpdateLatest": "Up to date",
        }),
        ("fa_IR", {
            "outboundSubTitle": "سابسکریپشن اوتباند",
            "outboundSubBalancer": "نام بالانسر",
            "outboundSubHint": "بعد از ذخیره، کانفیگ‌ها در Outbounds ظاهر می‌شوند. اگر بالانسر دارید، در Routing یک rule با balancerTag بسازید و Xray را Restart کنید.",
            "panelUpdate": "آپدیت پنل",
            "panelUpdateCheck": "بررسی آپدیت",
            "panelUpdateConfirm": "پنل از vpncore1/3x-ui آپدیت شود؟ پنل ری‌استارت می‌شود. این کار هرگز خودکار نیست.",
            "panelUpdateStarted": "آپدیت شروع شد. ۱ تا ۳ دقیقه صبر کنید و صفحه را رفرش کنید.",
            "panelUpdateAvailable": "آپدیت موجود است",
            "panelUpdateLatest": "به‌روز است",
        }),
    ):
        path = ROOT / "web/translation" / f"translate.{code}.toml"
        if not path.exists():
            continue
        raw = path.read_text(encoding="utf-8")
        # pages.xray keys
        xray_keys = {k: v for k, v in pairs.items() if k.startswith("outbound")}
        index_keys = {k: v for k, v in pairs.items() if k.startswith("panelUpdate")}
        if xray_keys and "outboundSubTitle" not in raw:
            block = "\n".join(f'{k} = "{v}"' for k, v in xray_keys.items()) + "\n"
            if "[pages.xray]" in raw:
                raw = raw.replace("[pages.xray]", "[pages.xray]\n" + block, 1)
            else:
                raw += "\n[pages.xray]\n" + block
        if index_keys and "panelUpdate" not in raw:
            block = "\n".join(f'{k} = "{v}"' for k, v in index_keys.items()) + "\n"
            if "[pages.index]" in raw:
                raw = raw.replace("[pages.index]", "[pages.index]\n" + block, 1)
            else:
                raw += "\n[pages.index]\n" + block
        path.write_text(raw, encoding="utf-8")


def patch_server_controller() -> None:
    path = ROOT / "web/controller/server.go"
    text = path.read_text(encoding="utf-8")
    if "updatePanel" in text:
        return
    text = text.replace(
        '\tg.POST("/getNewEchCert", a.getNewEchCert)\n',
        '\tg.POST("/getNewEchCert", a.getNewEchCert)\n'
        '\tg.GET("/panelUpdateInfo", a.getPanelUpdateInfo)\n'
        '\tg.POST("/updatePanel", a.updatePanel)\n',
    )
    handlers = r'''

// getPanelUpdateInfo reports whether a newer commit exists on vpncore1/3x-ui (check only).
func (a *ServerController) getPanelUpdateInfo(c *gin.Context) {
	info, err := a.serverService.GetPanelUpdateInfo()
	if err != nil {
		jsonMsgObj(c, "Failed to check panel update", info, err)
		return
	}
	jsonObj(c, info, nil)
}

// updatePanel starts a manual upgrade from vpncore1/3x-ui. Never runs automatically.
func (a *ServerController) updatePanel(c *gin.Context) {
	if err := a.serverService.StartPanelSelfUpdate(); err != nil {
		jsonMsg(c, "Failed to start panel update", err)
		return
	}
	jsonMsg(c, "Panel update started; wait then refresh", nil)
}
'''
    if "func (a *ServerController) getPanelUpdateInfo" not in text:
        text = text.rstrip() + "\n" + handlers + "\n"
    # Ensure jsonMsgObj import usage exists in base — it is used elsewhere in controllers.
    path.write_text(text, encoding="utf-8")


def patch_index_update_button() -> None:
    path = ROOT / "web/html/index.html"
    text = path.read_text(encoding="utf-8")
    if "updatePanelNow" in text:
        return

    # Add Update action on the 3X-UI card
    card_anchor = "                <a-card title='3X-UI' hoverable>\n"
    if card_anchor not in text:
        raise RuntimeError("patch_index_update_button: 3X-UI card not found")
    card_inject = '''                <a-card title='3X-UI' hoverable>
                  <template #actions>
                    <a-space direction="horizontal" @click="checkPanelUpdate" class="jc-center">
                      <a-icon type="cloud-sync"></a-icon>
                      <span v-if="!isMobile">{{ i18n "pages.index.panelUpdateCheck" }}</span>
                    </a-space>
                    <a-space direction="horizontal" @click="updatePanelNow" class="jc-center">
                      <a-icon type="cloud-download"></a-icon>
                      <span v-if="!isMobile">{{ i18n "pages.index.panelUpdate" }}</span>
                      <a-badge v-if="panelUpdate && panelUpdate.updateAvailable" status="processing" />
                    </a-space>
                  </template>
'''
    text = text.replace(card_anchor, card_inject, 1)

    # After version tag, show update status line
    ver_anchor = '                      <span>v{{ .cur_ver }}</span>\n'
    if ver_anchor in text:
        text = text.replace(
            ver_anchor,
            '                      <span>v{{ .cur_ver }}</span>\n'
            '                      <a-tag v-if="panelUpdate && panelUpdate.updateAvailable" color="orange" :style="{ marginLeft: \'6px\' }">\n'
            '                        {{ i18n "pages.index.panelUpdateAvailable" }}\n'
            '                      </a-tag>\n'
            '                      <a-tag v-else-if="panelUpdate && panelUpdate.remoteSha" color="blue" :style="{ marginLeft: \'6px\' }">\n'
            '                        {{ i18n "pages.index.panelUpdateLatest" }}\n'
            '                      </a-tag>\n',
            1,
        )

    # data field
    if "panelUpdate:" not in text:
        text = text.replace(
            "      versionModal,\n",
            "      versionModal,\n"
            "      panelUpdate: null,\n"
            "      panelUpdating: false,\n",
            1,
        )

    methods = '''
      async checkPanelUpdate() {
        const msg = await HttpUtil.get('/panel/api/server/panelUpdateInfo');
        if (msg.success) {
          this.panelUpdate = msg.obj;
        }
      },
      updatePanelNow() {
        this.$confirm({
          title: '{{ i18n "pages.index.panelUpdate" }}',
          content: '{{ i18n "pages.index.panelUpdateConfirm" }}',
          okText: '{{ i18n "confirm"}}',
          cancelText: '{{ i18n "cancel"}}',
          class: themeSwitcher.currentTheme,
          onOk: async () => {
            this.panelUpdating = true;
            this.loading(true, '{{ i18n "pages.index.panelUpdateStarted" }}');
            const msg = await HttpUtil.post('/panel/api/server/updatePanel');
            this.panelUpdating = false;
            this.loading(false);
            if (msg.success) {
              this.$message.success('{{ i18n "pages.index.panelUpdateStarted" }}');
              setTimeout(() => { this.checkPanelUpdate(); }, 15000);
            }
          },
        });
      },
'''
    text = text.replace("      switchV2rayVersion(version) {\n", methods + "      switchV2rayVersion(version) {\n", 1)

    # Load update info once on mount (check only — never auto-upgrade).
    # Note: method bodies already contain this.checkPanelUpdate(), so use a unique marker.
    if "panelUpdateCheckOnMount" not in text:
        anchor = "      // Initial status fetch\n      await this.getStatus();\n"
        if anchor in text:
            text = text.replace(
                anchor,
                anchor + "      this.checkPanelUpdate(); // panelUpdateCheckOnMount\n",
                1,
            )
        else:
            text = text.replace(
                "      await this.getStatus();\n",
                "      await this.getStatus();\n      this.checkPanelUpdate(); // panelUpdateCheckOnMount\n",
                1,
            )

    path.write_text(text, encoding="utf-8")


def reset_git_files() -> None:
    if not (ROOT / ".git").exists():
        return
    rels = [
        "database/model/model.go",
        "database/db.go",
        "web/controller/xray_setting.go",
        "web/controller/server.go",
        "web/service/xray.go",
        "web/web.go",
        "xray/api.go",
        "web/html/settings/xray/outbounds.html",
        "web/html/xray.html",
        "web/html/index.html",
        "web/translation/translate.en_US.toml",
        "web/translation/translate.fa_IR.toml",
    ]
    subprocess.run(["git", "checkout", "--", *rels], cwd=ROOT, check=False)


def main() -> None:
    if not ROOT.exists():
        print(f"Target not found: {ROOT}", file=sys.stderr)
        sys.exit(1)
    # Guard: this patch targets Vue-based v2.x
    if (ROOT / "frontend/src").exists() and not (ROOT / "web/html/xray.html").exists():
        print("ERROR: this panel-patch targets 3x-ui v2.9.0 (Vue UI). Found React frontend.", file=sys.stderr)
        sys.exit(2)
    reset_git_files()
    copy_overlay()
    patch_api_go()
    patch_model()
    patch_db()
    patch_xray_service()
    patch_web_cron()
    patch_controller()
    patch_server_controller()
    patch_vue_outbounds()
    patch_vue_xray_js()
    patch_index_update_button()
    patch_translations()
    print("panel-patch (v2.9.0 + subscriptions + balancer + self-update) applied to", ROOT)


if __name__ == "__main__":
    main()
