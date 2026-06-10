# راهنمای Push به گیت‌هاب (امن)

## مهم — امنیت

- **هرگز پسورد گیت‌هاب را در چت نفرستید**
- اگر پسورد را جایی فرستاده‌اید، فوراً از GitHub → Settings → Password آن را **عوض کنید**
- برای push از **Personal Access Token (PAT)** یا **SSH** استفاده کنید، نه پسورد

## مرحله ۱ — ساخت ریپو در گیت‌هاب

1. برو به https://github.com/new
2. نام: `3x-ui-sub-balancer`
3. Public
4. بدون README (خودمان داریم)
5. Create repository

## مرحله ۲ — ساخت Token

1. GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
2. Generate new token
3. Scope: `repo`
4. توکن را کپی کن (فقط یک‌بار نشان داده می‌شود)

## مرحله ۳ — Push از ویندوز

در PowerShell داخل پوشه پروژه:

```powershell
cd "C:\Users\ZRS\Desktop\my project\x-ui upgrade"
git init
git add .
git commit -m "Initial release: 3x-ui subscription balancer patch"
git branch -M main
git remote add origin https://github.com/sader21/3x-ui-sub-balancer.git
git push -u origin main
```

وقتی username خواست: `sader21`  
وقتی password خواست: **همان PAT** (نه پسورد اکانت)

## مرحله ۴ — نصب روی سرور

```bash
bash <(curl -Ls https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main/install.sh)
```

یا فقط آپگرید:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/sader21/3x-ui-sub-balancer/main/upgrade.sh)
```
