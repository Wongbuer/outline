# Outline 生产部署（docs.zengxjlab.org）

目标机：`root@154.36.184.81`  
镜像：`ghcr.io/wongbuer/outline:latest`  
登录：Dex + **GitHub/Gitee**（无本地密码）

## 1. GitHub OAuth App

1. GitHub → Settings → Developer settings → **OAuth Apps** → New  
2. 填写：
   - Application name: `Outline docs`
   - Homepage URL: `https://docs.zengxjlab.org`
   - Authorization callback URL: `https://docs.zengxjlab.org/dex/callback`
3. 创建后拿到 `Client ID` / `Client Secret`，写入服务器 `.env` 的 `GITHUB_CLIENT_*`

## 2. Gitee OAuth App

在 Gitee OAuth 应用中将回调地址配置为：

```text
https://docs.zengxjlab.org/dex/callback
```

将 Client ID / Client Secret 写入服务器 `.env` 的 `GITEE_CLIENT_*`。
Dex 会请求 Gitee 的 `user_info` 与 `emails` 权限；前者读取基本资料，后者用于读取并验证登录邮箱。

## 3. DNS

`docs.zengxjlab.org` A 记录 → `154.36.184.81`  
（Caddy 用 Cloudflare DNS-01 签证书，需 CF token 对 **zengxjlab.org** 有 DNS:Edit）

## 4. 服务器目录

```bash
ssh root@154.36.184.81
mkdir -p /opt/outline && cd /opt/outline
# 从本仓库 deploy/ 拷贝文件，或 git clone 后 cp
```

需要的文件：

- `docker-compose.yml`
- `.env`（从 `.env.example` 复制并填 secret）
- `dex-config.yaml.template` + `render-dex-config.sh`
- `Dockerfile.gitee-userinfo` + `gitee-userinfo.go`
- 生成后的 `dex-config.yaml`

Gitee 适配服务仅运行在 Outline 内网，用于可靠转发 Gitee token 请求并校验登录邮箱状态，不对公网暴露端口。

```bash
cp .env.example .env
# 编辑 .env 填 SECRET_KEY / UTILS_SECRET / POSTGRES_PASSWORD / GITHUB_* / GITEE_* / DEX_CLIENT_SECRET
chmod +x render-dex-config.sh
./render-dex-config.sh
mkdir -p data/outline data/postgres data/redis
chown -R 1001:1001 data/outline
```

`render-dex-config.sh` 会将包含 OAuth Secret 的生成配置设为 `root:${DEX_CONFIG_GID:-1001}`、权限 `0640`，与 Dex 容器的运行用户匹配。

## 5. 网络与 Caddy

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

## 6. 启动

```bash
cd /opt/outline
docker compose pull
docker compose up -d --build
docker compose ps
docker compose logs -f outline
```

访问：https://docs.zengxjlab.org → **Continue with GitHub/Gitee**

## 7. 更新镜像

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
