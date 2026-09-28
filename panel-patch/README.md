# پچ پنل — ساب + بالانسر روی **3x-ui v2.9.0**

این پوشه روی پایهٔ رسمی [v2.9.0](https://github.com/MHSanaei/3x-ui/releases/tag/v2.9.0) این‌ها را اضافه می‌کند:

1. **Outbound Subscriptions** (بک‌پورت از v3.3.0) — UI در همان تب Outbounds
2. **نام بالانسر / استراتژی / fallback** روی هر ساب
3. **Sync لحظه‌ای gRPC** برای اوتباندها و بالانسر (بدون اسکریپت خارجی)
4. Routing Rules همچنان **دستی** در پنل

> نسخهٔ استوک `install.sh v2.9.0` این قابلیت‌ها را ندارد؛ باید باینری پچ‌شده نصب شود.

## نصب / بیلد روی سرور

```bash
# کلون ریپوی پچ، سپس:
bash panel-patch/build_on_server.sh
```

یا با تگ صریح:

```bash
XUI_TAG=v2.9.0 bash panel-patch/build_on_server.sh
```

اسکریپت هستهٔ xray را آپدیت نمی‌کند (`KEEP_CORE=1`).

## مسیر در پنل (UI قدیمی Vue)

**Xray Settings → Outbounds → دکمه Subscriptions**

1. URL ساب + نام بالانسر (مثلاً `sub1`)
2. Add / Refresh
3. Routing → Rule با همان `balancerTag`
4. در صورت نیاز Restart Xray

## تفاوت با آپستریم

| | `install.sh … v2.9.0` | این پچ |
|--|--|--|
| پایه | v2.9.0 | v2.9.0 |
| Subscriptions اوتباند | ❌ | ✅ |
| بالانسر runtime | ❌ | ✅ |
| UI | Vue | Vue + مودال ساب |
