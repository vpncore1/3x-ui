# 3x-ui Sub Balancer

پچ رسمی [3x-ui](https://github.com/MHSanaei/3x-ui) برای **ساب‌اسکریپشن اوتباند + بالانسر** داخل خود پنل — بدون اسکریپت خارجی.

## قابلیت‌ها

- فیلد **نام بالانسر** در فرم Subscriptions (Outbounds → Subscriptions)
- آپدیت خودکار اوتباندها از لینک ساب
- sync بالانسر با لیست اوتباندها (selector = تگ‌های واقعی)
- Routing Rules دستی توسط شما در پنل
- پشتیبانی فارسی در UI

## نصب روی سرور لینوکس (تازه)

```bash
bash <(curl -Ls https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main/install.sh)
```

این دستور:
1. اگر 3x-ui نصب نیست → پنل رسمی را نصب می‌کند
2. سپس پچ را build و جایگزین می‌کند

## آپگرید (سرور که الان 3x-ui دارد)

```bash
bash <(curl -Ls https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main/upgrade.sh)
```

## استفاده در پنل

1. **Xray → Outbounds → Subscriptions** → Add
2. لینک ساب + **نام بالانسر** (مثلاً `sub1`) + بازه آپدیت
3. **Refresh** بزنید
4. **Xray → Routing** → Rule با `balancerTag` همان نام بالانسر
5. در صورت نیاز **Restart Xray**

| فیلد ساب | مثال |
|----------|------|
| نام بالانسر | `sub1` |
| پیشوند تگ | `sub1-` (خودکار) |
| بازه | `2` دقیقه |

> نام بالانسر در ساب و Routing Rule باید **یکسان** باشد.

## نسخه پایه

- Upstream: **3x-ui v3.3.0**
- Override: `XUI_TAG=v3.3.1 ./upgrade.sh`

## ساخت دستی روی سرور

```bash
git clone https://github.com/sader21/3x-ui-sub-balancer.git /opt/3x-ui-sub-balancer
bash /opt/3x-ui-sub-balancer/panel-patch/build_on_server.sh
```

## ساختار ریپو

```
├── install.sh          # نصب کامل
├── upgrade.sh          # آپگرید پچ
└── panel-patch/
    ├── apply_patch.py
    ├── build_on_server.sh
    ├── frontend/
    ├── web/
    └── xray/
```

## مجوز

سورس پایه متعلق به [MHSanaei/3x-ui](https://github.com/MHSanaei/3x-ui) است.  
پچ این ریپو روی همان مجوز upstream است.
