# 3x-ui + Outbound Subscriptions + Balancer

Public fork patch on **3x-ui v2.9.0** with:

- Outbound **Subscriptions** (backported)
- Named **balancer** + live gRPC sync
- Manual Routing Rules in the panel
- Xray core is **not** updated by this installer

## Install (one command)

```bash
bash <(curl -Ls https://raw.githubusercontent.com/vpncore1/3x-ui/main/install.sh)
```

Custom panel port:

```bash
PANEL_PORT=1872 bash <(curl -Ls https://raw.githubusercontent.com/vpncore1/3x-ui/main/install.sh)
```

## Usage

1. Panel → **Xray** → **Outbounds** → **Subscriptions**
2. Add subscription URL + balancer name (e.g. `sub1`)
3. Refresh
4. **Routing** → Rule with the same `balancerTag`

## Base version

Upstream: [MHSanaei/3x-ui v2.9.0](https://github.com/MHSanaei/3x-ui/releases/tag/v2.9.0)
