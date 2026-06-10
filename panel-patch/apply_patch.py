#!/usr/bin/env python3
"""Apply panel-patch overlay onto a 3x-ui source tree."""

from __future__ import annotations

import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/opt/3x-ui-build")
PATCH = Path(__file__).resolve().parent


def copy_overlay() -> None:
    for rel in ("xray/outbound_runtime.go",):
        src = PATCH / rel
        dst = ROOT / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    for rel in (
        "web/service/outbound_subscription_runtime.go",
        "web/service/outbound_subscription_balancer.go",
    ):
        src = PATCH / rel
        dst = ROOT / rel
        shutil.copy2(src, dst)
    frontend_src = PATCH / "frontend" / "OutboundsTab.tsx"
    if frontend_src.exists():
        dst = ROOT / "frontend/src/pages/xray/outbounds/OutboundsTab.tsx"
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(frontend_src, dst)


def patch_api_go() -> None:
    path = ROOT / "xray/api.go"
    text = path.read_text(encoding="utf-8")
    if "RoutingServiceClient" not in text:
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
    if "BalancerTag" in text:
        return
    extra = (
        '\tBalancerTag       string `json:"balancerTag" form:"balancerTag"`\n'
        '\tTargetInboundTag  string `json:"targetInboundTag" form:"targetInboundTag"`\n'
        '\tBalancerStrategy  string `json:"balancerStrategy" form:"balancerStrategy" gorm:"default:roundRobin"`\n'
        '\tFallbackTag       string `json:"fallbackTag" form:"fallbackTag"`\n'
    )
    text, n = re.subn(
        r'(\tTagPrefix\s+string `json:"tagPrefix" form:"tagPrefix"`\n)',
        r"\1" + extra,
        text,
        count=1,
    )
    if n != 1:
        raise RuntimeError("patch_model: TagPrefix anchor not found")
    path.write_text(text, encoding="utf-8")


def patch_outbound_subscription_service() -> None:
    path = ROOT / "web/service/outbound_subscription.go"
    text = path.read_text(encoding="utf-8")

    old_create = (
        "func (s *OutboundSubscriptionService) Create(remark, rawURL, tagPrefix string, enabled bool, updateInterval int, allowPrivate, prepend bool) (*model.OutboundSubscription, error) {"
    )
    new_create = (
        "func (s *OutboundSubscriptionService) Create(remark, rawURL, tagPrefix string, enabled bool, updateInterval int, allowPrivate, prepend bool, balancerTag, targetInboundTag, balancerStrategy, fallbackTag string) (*model.OutboundSubscription, error) {"
    )
    text = text.replace(old_create, new_create)

    old_update = (
        "func (s *OutboundSubscriptionService) Update(id int, remark, rawURL, tagPrefix string, enabled bool, updateInterval int, allowPrivate, prepend bool) error {"
    )
    new_update = (
        "func (s *OutboundSubscriptionService) Update(id int, remark, rawURL, tagPrefix string, enabled bool, updateInterval int, allowPrivate, prepend bool, balancerTag, targetInboundTag, balancerStrategy, fallbackTag string) error {"
    )
    text = text.replace(old_update, new_update)

    if "applyBalancerFields" not in text:
        helper = '''

func applyBalancerFields(sub *model.OutboundSubscription, balancerTag, targetInboundTag, balancerStrategy, fallbackTag, tagPrefix string) {
	sub.BalancerTag = strings.TrimSpace(balancerTag)
	sub.TargetInboundTag = strings.TrimSpace(targetInboundTag)
	sub.BalancerStrategy = strings.TrimSpace(balancerStrategy)
	if sub.BalancerStrategy == "" {
		sub.BalancerStrategy = "roundRobin"
	}
	sub.FallbackTag = strings.TrimSpace(fallbackTag)
	prefix := strings.TrimSpace(tagPrefix)
	if prefix == "" && sub.BalancerTag != "" {
		prefix = sub.BalancerTag + "-"
	}
	sub.TagPrefix = prefix
}
'''
        text = text.replace(
            "func (s *OutboundSubscriptionService) recordError(sub *model.OutboundSubscription, err error) {",
            helper + "\nfunc (s *OutboundSubscriptionService) recordError(sub *model.OutboundSubscription, err error) {",
        )

    # Create body: after prefix assignment, apply balancer fields
    text = text.replace(
        "\tsub := &model.OutboundSubscription{\n"
        "\t\tRemark:         strings.TrimSpace(remark),\n"
        "\t\tUrl:            cleanURL,\n"
        "\t\tEnabled:        enabled,\n"
        "\t\tAllowPrivate:   allowPrivate,\n"
        "\t\tPrepend:        prepend,\n"
        "\t\tPriority:       int(count),\n"
        "\t\tTagPrefix:      prefix,\n"
        "\t\tUpdateInterval: updateInterval,\n"
        "\t}",
        "\tsub := &model.OutboundSubscription{\n"
        "\t\tRemark:         strings.TrimSpace(remark),\n"
        "\t\tUrl:            cleanURL,\n"
        "\t\tEnabled:        enabled,\n"
        "\t\tAllowPrivate:   allowPrivate,\n"
        "\t\tPrepend:        prepend,\n"
        "\t\tPriority:       int(count),\n"
        "\t\tTagPrefix:      prefix,\n"
        "\t\tUpdateInterval: updateInterval,\n"
        "\t}\n"
        "\tapplyBalancerFields(sub, balancerTag, targetInboundTag, balancerStrategy, fallbackTag, prefix)",
    )

    text = text.replace(
        "\tsub.TagPrefix = prefix\n"
        "\tsub.UpdateInterval = updateInterval\n"
        "\treturn database.GetDB().Save(sub).Error",
        "\tapplyBalancerFields(sub, balancerTag, targetInboundTag, balancerStrategy, fallbackTag, prefix)\n"
        "\tsub.UpdateInterval = updateInterval\n"
        "\treturn database.GetDB().Save(sub).Error",
    )

    path.write_text(text, encoding="utf-8")


def reset_git_files() -> None:
    if not (ROOT / ".git").exists():
        return
    rels = [
        "database/model/model.go",
        "web/controller/xray_setting.go",
        "web/service/outbound_subscription.go",
        "web/job/outbound_subscription_job.go",
        "xray/api.go",
        "frontend/src/pages/xray/outbounds/OutboundsTab.tsx",
        "web/translation/en-US.json",
        "web/translation/fa-IR.json",
    ]
    subprocess.run(["git", "checkout", "--", *rels], cwd=ROOT, check=False)


def patch_controller() -> None:
    path = ROOT / "web/controller/xray_setting.go"
    text = path.read_text(encoding="utf-8")

    helper = '''
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

func (a *XraySettingController) afterOutboundSubChange(subID int, removed bool) {
	if removed {
		sub, err := a.OutboundSubscriptionService.Get(subID)
		if err == nil && service.UsesRuntimeSync(sub) {
			_ = a.OutboundSubscriptionService.RemoveRuntimeOutbounds(sub, a.XrayService.GetXrayAPIPort())
			return
		}
		a.XrayService.SetToNeedRestart()
		return
	}
	sub, err := a.OutboundSubscriptionService.Get(subID)
	if err != nil {
		a.XrayService.SetToNeedRestart()
		return
	}
	if !service.UsesRuntimeSync(sub) {
		a.XrayService.SetToNeedRestart()
		return
	}
	if err := a.OutboundSubscriptionService.ApplyRuntimeSync(sub, a.XrayService.GetXrayAPIPort()); err != nil {
		logger.Warningf("outbound sub %d runtime sync failed: %v", subID, err)
		a.XrayService.SetToNeedRestart()
	}
}
'''
    if "parseBalancerSubFields" not in text:
        if '"github.com/mhsanaei/3x-ui/v3/logger"' not in text:
            text = text.replace(
                '"github.com/mhsanaei/3x-ui/v3/util/common"\n',
                '"github.com/mhsanaei/3x-ui/v3/logger"\n'
                '\t"github.com/mhsanaei/3x-ui/v3/util/common"\n',
            )
        if '"strings"' not in text:
            text = text.replace('"strconv"\n', '"strconv"\n\t"strings"\n')
        text = text.replace(
            "func parseIntSafe(s string) (int, error) {",
            helper + "\nfunc parseIntSafe(s string) (int, error) {",
        )

    text = text.replace(
        '\tsub, err := a.OutboundSubscriptionService.Create(remark, rawURL, prefix, enabled, interval, allowPrivate, prepend)',
        '\tbt, tit, bs, fb := parseBalancerSubFields(c)\n'
        '\tsub, err := a.OutboundSubscriptionService.Create(remark, rawURL, prefix, enabled, interval, allowPrivate, prepend, bt, tit, bs, fb)',
    )
    text = text.replace(
        '\tif err := a.OutboundSubscriptionService.Update(subID, remark, rawURL, prefix, enabled, interval, allowPrivate, prepend); err != nil {',
        '\tbt, tit, bs, fb := parseBalancerSubFields(c)\n'
        '\tif err := a.OutboundSubscriptionService.Update(subID, remark, rawURL, prefix, enabled, interval, allowPrivate, prepend, bt, tit, bs, fb); err != nil {',
    )
    text = text.replace(
        '\t\tjsonMsg(c, "Failed to update outbound subscription", err)\n'
        '\t\treturn\n'
        '\t}\n'
        '\tjsonObj(c, "", nil)',
        '\t\tjsonMsg(c, "Failed to update outbound subscription", err)\n'
        '\t\treturn\n'
        '\t}\n'
        '\ta.afterOutboundSubChange(subID, false)\n'
        '\tjsonObj(c, "", nil)',
    )
    delete_old = '''func (a *XraySettingController) deleteOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	if err := a.OutboundSubscriptionService.Delete(subID); err != nil {
		jsonMsg(c, "Failed to delete outbound subscription", err)
		return
	}
	// Signal that xray should drop this subscription's outbounds on next reload.
	a.XrayService.SetToNeedRestart()
	jsonObj(c, "", nil)
}'''
    delete_new = '''func (a *XraySettingController) deleteOutboundSub(c *gin.Context) {
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
	if subBeforeDel != nil && service.UsesRuntimeSync(subBeforeDel) {
		_ = a.OutboundSubscriptionService.RemoveRuntimeOutbounds(subBeforeDel, a.XrayService.GetXrayAPIPort())
	} else {
		a.XrayService.SetToNeedRestart()
	}
	jsonObj(c, "", nil)
}'''
    if delete_old in text:
        text = text.replace(delete_old, delete_new)

    refresh_old = '''func (a *XraySettingController) refreshOutboundSub(c *gin.Context) {
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
	// Signal that xray should pick up the new outbounds on next restart/reload
	a.XrayService.SetToNeedRestart()
	jsonObj(c, obs, nil)
}'''
    refresh_new = '''func (a *XraySettingController) refreshOutboundSub(c *gin.Context) {
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
	a.afterOutboundSubChange(subID, false)
	jsonObj(c, obs, nil)
}'''
    if refresh_old in text:
        text = text.replace(refresh_old, refresh_new)

    move_old = '''func (a *XraySettingController) moveOutboundSub(c *gin.Context) {
	id := c.Param("id")
	var subID int
	if _, err := fmt.Sscanf(id, "%d", &subID); err != nil {
		jsonMsg(c, "Invalid id", err)
		return
	}
	up := c.PostForm("dir") == "up"
	if err := a.OutboundSubscriptionService.Move(subID, up); err != nil {
		jsonMsg(c, "Failed to reorder outbound subscription", err)
		return
	}
	// Order affects the merged outbounds, so xray needs a reload.
	a.XrayService.SetToNeedRestart()
	jsonObj(c, "", nil)
}'''
    move_new = '''func (a *XraySettingController) moveOutboundSub(c *gin.Context) {
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
}'''
    if move_old in text:
        text = text.replace(move_old, move_new)

    path.write_text(text, encoding="utf-8")


def patch_job() -> None:
    path = ROOT / "web/job/outbound_subscription_job.go"
    text = path.read_text(encoding="utf-8")

    old_runtime_block = (
        "\t\tport := j.xraySvc.GetXrayAPIPort()\n"
        "\t\tj.subService.SyncAllRuntime(port)\n"
        "\t\tvar legacy int64\n"
        "\t\tdatabase.GetDB().Model(&model.OutboundSubscription{}).Where(\"enabled = ? AND (balancer_tag = '' OR balancer_tag IS NULL)\", true).Count(&legacy)\n"
        "\t\tif legacy > 0 {\n"
        "\t\t\tj.xraySvc.SetToNeedRestart()\n"
        "\t\t}\n"
    )
    new_runtime_block = (
        "\t\tport := j.xraySvc.GetXrayAPIPort()\n"
        "\t\tneedRestart := j.subService.SyncAllRuntime(port)\n"
        "\t\tvar legacy int64\n"
        "\t\tdatabase.GetDB().Model(&model.OutboundSubscription{}).Where(\"enabled = ? AND (balancer_tag = '' OR balancer_tag IS NULL)\", true).Count(&legacy)\n"
        "\t\tif needRestart || legacy > 0 {\n"
        "\t\t\tj.xraySvc.SetToNeedRestart()\n"
        "\t\t}\n"
    )
    if old_runtime_block in text:
        text = text.replace(old_runtime_block, new_runtime_block)
        path.write_text(text, encoding="utf-8")
        return

    if "needRestart := j.subService.SyncAllRuntime" in text:
        return

    if '"github.com/mhsanaei/3x-ui/v3/database"' not in text:
        text = text.replace(
            '"github.com/mhsanaei/3x-ui/v3/logger"\n',
            '"github.com/mhsanaei/3x-ui/v3/database"\n'
            '\t"github.com/mhsanaei/3x-ui/v3/database/model"\n'
            '\t"github.com/mhsanaei/3x-ui/v3/logger"\n',
        )
    text = text.replace(
        "\tif count > 0 {\n"
        "\t\tlogger.Infof(\"Refreshed %d outbound subscription(s)\", count)\n"
        "\t\t// Ask the xray manager to restart/reload on the next 30s check.\n"
        "\t\tj.xraySvc.SetToNeedRestart()\n",
        "\tif count > 0 {\n"
        "\t\tlogger.Infof(\"Refreshed %d outbound subscription(s)\", count)\n"
        + new_runtime_block,
    )
    path.write_text(text, encoding="utf-8")


def patch_outbounds_tab() -> None:
    path = ROOT / "frontend/src/pages/xray/outbounds/OutboundsTab.tsx"
    text = path.read_text(encoding="utf-8")

    if "balancerTag" in text:
        return

    text = text.replace(
        "  tagPrefix?: string;\n  updateInterval?: number;",
        "  tagPrefix?: string;\n  balancerTag?: string;\n  targetInboundTag?: string;\n  balancerStrategy?: string;\n  fallbackTag?: string;\n  updateInterval?: number;",
    )
    text = text.replace(
        "  inboundTags: string[];",
        "  inboundTags: string[];",
    )
    text = text.replace(
        "  inboundTags: _inboundTags,",
        "  inboundTags,",
    )
    text = text.replace(
        '  const [newSub, setNewSub] = useState({ remark: \'\', url: \'\', tagPrefix: \'\', updateInterval: 600, enabled: true, allowPrivate: false, prepend: false });',
        '  const [newSub, setNewSub] = useState({ remark: \'\', url: \'\', tagPrefix: \'\', balancerTag: \'\', targetInboundTag: \'\', balancerStrategy: \'roundRobin\', fallbackTag: \'\', updateInterval: 600, enabled: true, allowPrivate: false, prepend: false });',
    )
    text = text.replace(
        "    tagPrefix: src.tagPrefix ?? '',\n    updateInterval: src.updateInterval ?? 600,",
        "    tagPrefix: src.tagPrefix ?? '',\n    balancerTag: src.balancerTag ?? '',\n    targetInboundTag: src.targetInboundTag ?? '',\n    balancerStrategy: src.balancerStrategy ?? 'roundRobin',\n    fallbackTag: src.fallbackTag ?? '',\n    updateInterval: src.updateInterval ?? 600,",
    )
    text = text.replace(
        '    setNewSub({ remark: \'\', url: \'\', tagPrefix: \'\', updateInterval: 600, enabled: true, allowPrivate: false, prepend: false });',
        '    setNewSub({ remark: \'\', url: \'\', tagPrefix: \'\', balancerTag: \'\', targetInboundTag: \'\', balancerStrategy: \'roundRobin\', fallbackTag: \'\', updateInterval: 600, enabled: true, allowPrivate: false, prepend: false });',
    )
    text = text.replace(
        "      tagPrefix: sub.tagPrefix ?? '',\n      updateInterval: sub.updateInterval ?? 600,",
        "      tagPrefix: sub.tagPrefix ?? '',\n      balancerTag: sub.balancerTag ?? '',\n      targetInboundTag: sub.targetInboundTag ?? '',\n      balancerStrategy: sub.balancerStrategy ?? 'roundRobin',\n      fallbackTag: sub.fallbackTag ?? '',\n      updateInterval: sub.updateInterval ?? 600,",
    )
    text = text.replace(
        "  async function saveSub() {\n    if (!newSub.url.trim()) {",
        "  async function saveSub() {\n    if (newSub.balancerTag.trim() && !newSub.targetInboundTag.trim()) {\n      messageApi.warning(t('pages.xray.outboundSub.toastInboundRequired'));\n      return;\n    }\n    if (!newSub.url.trim()) {",
    )

    form_block = '''              <Input value={newSub.tagPrefix} onChange={(e) => setNewSub({ ...newSub, tagPrefix: e.target.value })} placeholder={t('pages.xray.outboundSub.tagPrefixPlaceholder')} />
              <Form.Item label={t('pages.xray.outboundSub.balancerTag')} style={{ marginBottom: 8 }}>
                <Input
                  value={newSub.balancerTag}
                  onChange={(e) => {
                    const v = e.target.value;
                    setNewSub((prev) => ({
                      ...prev,
                      balancerTag: v,
                      tagPrefix: prev.tagPrefix || (v.trim() ? `${v.trim()}-` : ''),
                    }));
                  }}
                  placeholder={t('pages.xray.outboundSub.balancerTagPlaceholder')}
                />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.targetInbound')} style={{ marginBottom: 8 }}>
                <Select
                  allowClear
                  showSearch
                  optionFilterProp="label"
                  value={newSub.targetInboundTag || undefined}
                  placeholder={t('pages.xray.outboundSub.targetInboundPlaceholder')}
                  options={inboundTags.map((tag) => ({ value: tag, label: tag }))}
                  onChange={(v) => setNewSub({ ...newSub, targetInboundTag: v ?? '' })}
                />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.balancerStrategy')} style={{ marginBottom: 8 }}>
                <Select
                  value={newSub.balancerStrategy}
                  options={[
                    { value: 'roundRobin', label: 'roundRobin' },
                    { value: 'random', label: 'random' },
                    { value: 'leastPing', label: 'leastPing' },
                    { value: 'leastLoad', label: 'leastLoad' },
                  ]}
                  onChange={(v) => setNewSub({ ...newSub, balancerStrategy: v })}
                />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.fallbackTag')} style={{ marginBottom: 8 }}>
                <Input value={newSub.fallbackTag} onChange={(e) => setNewSub({ ...newSub, fallbackTag: e.target.value })} placeholder={t('pages.xray.outboundSub.fallbackTagPlaceholder')} />
              </Form.Item>'''

    text = text.replace(
        '              <Input value={newSub.tagPrefix} onChange={(e) => setNewSub({ ...newSub, tagPrefix: e.target.value })} placeholder={t(\'pages.xray.outboundSub.tagPrefixPlaceholder\')} />',
        form_block,
    )

    text = text.replace(
        "{t('pages.xray.outboundSub.restartHint')}",
        "{newSub.balancerTag.trim() ? t('pages.xray.outboundSub.runtimeHint') : t('pages.xray.outboundSub.restartHint')}",
    )

    path.write_text(text, encoding="utf-8")


def patch_translations() -> None:
    for code, extra in (
        ("en-US", {
            "balancerTag": "Balancer name",
            "balancerTagPlaceholder": "e.g. sub1",
            "targetInbound": "Route inbound",
            "targetInboundPlaceholder": "Select inbound for this balancer",
            "balancerStrategy": "Balancer strategy",
            "fallbackTag": "Fallback outbound (optional)",
            "fallbackTagPlaceholder": "direct",
            "runtimeHint": "With a balancer name, outbounds sync into the balancer on schedule — no Xray restart. Set Routing Rules yourself in the panel.",
            "colBalancer": "Balancer",
        }),
        ("fa-IR", {
            "balancerTag": "نام بالانسر",
            "balancerTagPlaceholder": "مثلاً sub1",
            "targetInbound": "اینباند مقصد",
            "targetInboundPlaceholder": "اینباند ورودی کاربران را انتخاب کنید",
            "balancerStrategy": "استراتژی بالانسر",
            "fallbackTag": "اوتباند fallback (اختیاری)",
            "fallbackTagPlaceholder": "direct",
            "runtimeHint": "با نام بالانسر، اوتباندها طبق بازه زمانی داخل بالانسر sync می‌شوند — بدون ریستارت. Routing Rules را خودتان در پنل تنظیم کنید.",
            "colBalancer": "بالانسر",
        }),
    ):
        path = ROOT / "web/translation" / f"{code}.json"
        raw = path.read_text(encoding="utf-8")
        for key, val in extra.items():
            if f'"{key}"' in raw:
                continue
            raw = raw.replace(
                '"restartHint":',
                f'"{key}": "{val}",\n      "restartHint":',
                1,
            )
        path.write_text(raw, encoding="utf-8")


def patch_parse_preview() -> None:
    path = ROOT / "web/controller/xray_setting.go"
    text = path.read_text(encoding="utf-8")
    text = text.replace(
        'tmp, err := svc.Create("preview", rawURL, "", false, 600, allowPrivate, false)',
        'tmp, err := svc.Create("preview", rawURL, "", false, 600, allowPrivate, false, "", "", "", "")',
    )
    path.write_text(text, encoding="utf-8")


def main() -> None:
    if not ROOT.exists():
        print(f"Target not found: {ROOT}", file=sys.stderr)
        sys.exit(1)
    reset_git_files()
    copy_overlay()
    patch_api_go()
    patch_model()
    patch_outbound_subscription_service()
    patch_controller()
    patch_job()
    patch_parse_preview()
    patch_translations()
    print("panel-patch applied to", ROOT)


if __name__ == "__main__":
    main()
