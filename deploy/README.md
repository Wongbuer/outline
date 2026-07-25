# Outline 生产部署（docs.zengxjlab.org）

目标机：`root@154.36.184.81`  
镜像：`ghcr.io/wongbuer/outline:latest`  
登录：Dex + **仅 GitHub**（无本地密码）

## 1. GitHub OAuth App

1. GitHub → Settings → Developer settings → **OAuth Apps** → New  
2. 填写：
   - Application name: `Outline docs`
   - Homepage URL: `https://docs.zengxjlab.org`
   - Authorization callback URL: `https://docs.zengxjlab.org/dex/callback`
3. 创建后拿到 `Client ID` / `Client Secret`，写入服务器 `.env` 的 `GITHUB_CLIENT_*`

## 2. DNS

`docs.zengxjlab.org` A 记录 → `154.36.184.81`  
（Caddy 用 Cloudflare DNS-01 签证书，需 CF token 对 **zengxjlab.org** 有 DNS:Edit）

## 3. 服务器目录

```bash
ssh root@154.36.184.81
mkdir -p /opt/outline && cd /opt/outline
# 从本仓库 deploy/ 拷贝文件，或 git clone 后 cp
```

需要的文件：

- `docker-compose.yml`
- `.env`（从 `.env.example` 复制并填 secret）
- `dex-config.yaml.template` + `render-dex-config.sh`
- 生成后的 `dex-config.yaml`

```bash
cp .env.example .env
# 编辑 .env 填 SECRET_KEY / UTILS_SECRET / POSTGRES_PASSWORD / GITHUB_* / DEX_CLIENT_SECRET
chmod +x render-dex-config.sh
./render-dex-config.sh
mkdir -p data/outline data/postgres data/redis
chown -R 1001:1001 data/outline
```

## 4. 网络与 Caddy

Outline / Dex 加入外部网络 `edge`（与现有 caddy 同网）。

```bash
# 若 edge 不存在
docker network create edge

# 把 Caddyfile.snippet 追加进 /opt/caddy/Caddyfile
# 并确保 caddy 容器连到 outline 网络，或 outline/dex 在 edge 上（compose 已 join edge）
cd /opt/caddy && docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
# 或 docker compose restart caddy
```

Caddy 需能解析容器名 `outline`、`outline-dex`（它们在 `edge` 网络上）。

## 5. 启动

```bash
cd /opt/outline
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f outline
```

访问：https://docs.zengxjlab.org → **Continue with GitHub**

## 6. 更新镜像

```bash
cd /opt/outline
docker compose pull outline
docker compose up -d outline
```

## 资源注意

共用机约 3.8G 内存。Outline 栈建议限制：

- outline ~512M–1G
- postgres ~256M
- redis ~64M
- dex ~64M

可按需在 compose 加 `deploy.resources.limits`。
