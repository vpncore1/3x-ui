# پچ پنل — ساب + بالانسر داخل 3x-ui

این پوشه سورس پنل 3x-ui را patch می‌کند تا **بدون اسکریپت خارجی**:

- فیلدهای **نام بالانسر** (`sub1`)، **اینباند مقصد**، **استراتژی** و **fallback** در همان فرم **Subscriptions** (Outbounds → Subscriptions) باشد
- بعد از refresh ساب، اوتباندها **لحظه‌ای** با gRPC اعمال شوند (بدون ریستارت Xray)
- اوتباندها در بالانسر sync شوند (Routing Rules دستی — توسط خودتان در پنل)

## فیلدهای جدید در فرم ساب

| فیلد | مثال | توضیح |
|------|------|--------|
| نام بالانسر | `sub1` | تگ ثابت balancer |
| پیشوند تگ | `sub1-` | خودکار از نام بالانسر |
| بازه | 30 دقیقه | همان interval قبلی |
| استراتژی | roundRobin | random / leastPing / leastLoad |

## Build روی سرور

```bash
git clone https://github.com/sader21/3x-ui-sub-balancer.git /opt/3x-ui-sub-balancer
bash /opt/3x-ui-sub-balancer/panel-patch/build_on_server.sh
```

یا آپگرید یک‌خطی:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main/upgrade.sh)
```

## مسیر در پنل

**Xray Configs → Outbounds → Subscriptions**

بعد از deploy، فرم ساب فیلدهای بالانسر را نشان می‌دهد. با پر کردن **نام بالانسر** + **اینباند**، دیگر پیام «restart Xray» برای آن ساب معنا ندارد.
