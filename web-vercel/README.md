# Equilibria Admin Console — Vercel Demo

> **Demo 模式**：纯前端 mock，无后端。
> 部署到 Vercel 5 分钟得到一个可分享的 URL。
> 真实部署请使用 [`cmd/admin-console`](../cmd/admin-console/)（Go + Gin）。

## 这是什么？

把 [`cmd/admin-console`](../cmd/admin-console/) 的 web 部分（HTML + CSS + JS）提取出来，改造成**纯前端演示**：

| 原版（Go + Gin） | 本演示（纯前端） |
| --- | --- |
| 真实治理插件（centralized） | 10 个内置 mock 用户 |
| `/api/mint` 调用 plugin.MintCurrency | `mockMint()` 直接修改内存对象 |
| 真实基尼系数 | 前端用与 Go 端 `pkg/metrics` 一致的算法 |
| 需要 Go 1.25+ | 0 后端，任何 CDN 都能跑 |

**用途**：
- 给投资人/同事看 UI demo
- 演示"基尼系数随发行变化"的视觉效果
- 嵌入 README / 文档做 live preview

## 本地预览

```bash
# 方案 1：Python（最简单）
cd web-vercel
python -m http.server 8000
# 浏览器打开 http://localhost:8000

# 方案 2：Node.js
cd web-vercel
npx http-server -p 8000

# 方案 3：Vercel CLI（与生产环境完全一致）
cd web-vercel
vercel dev
```

## 部署到 Vercel

### 方式 1：Vercel CLI（推荐，1 分钟）

```bash
# 安装（如未安装）
npm i -g vercel

# 登录
vercel login

# 部署
cd web-vercel
vercel              # 首次会问几个问题，一路回车默认即可
vercel --prod       # 部署到生产环境
```

完成后会得到一个 `https://equilibria-admin-console-demo.vercel.app` 之类的 URL。

### 方式 2：GitHub 集成（自动部署）

1. 把 `web-vercel/` 推到 GitHub
2. Vercel 控制台 → "New Project" → 选这个仓库
3. **Root Directory** 设为 `web-vercel`
4. **Framework Preset** 选 "Other"
5. 点 "Deploy"

之后每次 `git push` 都会自动重新部署。

### 方式 3：拖拽部署（最快）

1. 访问 https://vercel.com/new
2. 把 `web-vercel/` 整个文件夹拖进网页
3. 等 30 秒拿到 URL

## 文件结构

```
web-vercel/
├── index.html          # 入口 HTML
├── static/
│   ├── style.css       # 暗色主题
│   └── app.js          # mock 版前端（基尼系数、Chart.js、表单）
├── vercel.json         # 部署配置（缓存 + 安全 headers + CSP）
└── README.md           # 本文件
```

## 与原版的差异

| 模块 | 原版 | Demo |
| --- | --- | --- |
| 健康检查 | `GET /api/health` | 前端常量，pill 显示"Demo 模式"（灰色） |
| 指标 | `GET /api/metrics`（来自 plugin） | `mockFetchMetrics()`，与 Go 端 `pkg/metrics/gini.go` 算法一致 |
| 用户列表 | `GET /api/users` | `mockFetchUsers()`，10 个内置账户 |
| 发行 | `POST /api/mint` | `mockMint()`，使用相同通胀率 5% |
| 数据演化 | 无 | 30s 自动随机给某用户加币（模拟"持续发行"） |
| 偶发事件 | 无 | 3% 概率"鲸鱼出场"、2% 概率"小崩盘" |

## 切换到真实后端

要连真实 Go 服务（带 plugin），只需把 `static/app.js` 里的 `mock*` 函数改回 `fetch('/api/...')`，**与原版 1:1 对应**。然后部署 `cmd/admin-console` 到 Fly.io / Railway。

```js
// 替换示例
async function checkHealth() {
  const r = await fetch("/api/health");
  const data = await r.json();
  renderHealth(data);
}
```

注意：跨域访问需要后端开启 CORS（admin-console 默认未开启 CORS，因为是同源部署）。
