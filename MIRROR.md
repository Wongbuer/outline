# Wongbuer/outline 镜像说明

双分支镜像模型（与 yohaku 一致）：

| 分支 | 用途 |
|------|------|
| `upstream-main` | 上游 `outline/outline` main 的纯净镜像，**勿手改**；由 `.github/workflows/sync-upstream.yml` 每天同步覆盖 |
| `main` | 我们的部署/定制分支；二开只在这里做 |

## 更新上游

```bash
git fetch origin
git checkout main
git merge origin/upstream-main
git push origin main
```

push 到 `main` 会触发 `ghcr.yml` 构建并推送：

- `ghcr.io/wongbuer/outline:latest`
- `ghcr.io/wongbuer/outline:<short-sha>`

## 部署

见仓库 `deploy/` 目录，目标机：`root@154.36.184.81`，域名：`docs.zengxjlab.org`。

登录：Dex + **仅 GitHub**（无本地账号密码）。

## 分支约定

- 上游同步 PR / merge 只动 `upstream-main` → `main` 的合并
- 功能二开：`feature/*` 从 `main` 拉出，合回 `main`
- 不要在 `upstream-main` 上提交定制
