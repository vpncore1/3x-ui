# 3x-ui Sub Balancer (Private)

پچ [3x-ui](https://github.com/MHSanaei/3x-ui) برای **ساب + بالانسر** داخل پنل — ریپوی **خصوصی** فقط برای `sader21`.

## نصب روی سرور

### ۱) ساخت Token (یک‌بار)

GitHub → **Settings** → **Developer settings** → **Personal access tokens** → **Tokens (classic)** → Generate

- Scope: **`repo`** (دسترسی به ریپوی private)
- توکن را کپی کن

### ۲) نصب تازه

```bash
export GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxx
git clone https://x-access-token:${GITHUB_TOKEN}@github.com/sader21/3x-ui-sub-balancer.git /opt/3x-ui-sub-balancer
bash /opt/3x-ui-sub-balancer/install.sh
```

### ۳) آپگرید (سرور که الان 3x-ui دارد)

```bash
export GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxx
bash /opt/3x-ui-sub-balancer/upgrade.sh
```

یا اگر پوشه را نداری:

```bash
export GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxx
git clone https://x-access-token:${GITHUB_TOKEN}@github.com/sader21/3x-ui-sub-balancer.git /opt/3x-ui-sub-balancer
bash /opt/3x-ui-sub-balancer/upgrade.sh
```

> **نکته:** با ریپوی private دیگر `curl raw.githubusercontent.com` کار نمی‌کند — حتماً `GITHUB_TOKEN` بده.

## استفاده در پنل

1. **Xray → Outbounds → Subscriptions** → Add
2. لینک ساب + **نام بالانسر** + بازه آپدیت
3. **Refresh**
4. **Routing** → Rule با همان `balancerTag`
5. در صورت نیاز **Restart Xray**

## نسخه پایه

Upstream: **3x-ui v3.3.0** — override: `XUI_TAG=v3.3.1 bash upgrade.sh`

## مجوز

سورس پایه: [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui)
