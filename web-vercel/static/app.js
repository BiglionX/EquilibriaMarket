// Equilibria Admin Console — Vercel Demo Edition
//
// 此版本是 admin-console 的「演示模式」：
//   - 不需要后端 Go 服务，所有数据在前端 mock。
//   - 启动时生成 10 个虚拟用户，随时间自动演化基尼系数。
//   - 「发行货币」按钮在前端直接修改状态、重新计算指标。
//
// 部署到 Vercel：仅静态资源，零后端。
// 真实部署请使用 cmd/admin-console（Go + Gin）。

const REFRESH_INTERVAL_MS = 3000;
const HISTORY_CAP = 30;
const GINI_ALERT_THRESHOLD = 0.4;
const MOCK_USERS = [
  { user_id: "alice", base: 1000 },
  { user_id: "bob", base: 500 },
  { user_id: "carol", base: 300 },
  { user_id: "dave", base: 100 },
  { user_id: "eve", base: 50 },
  { user_id: "frank", base: 50 },
  { user_id: "grace", base: 50 },
  { user_id: "heidi", base: 20 },
  { user_id: "ivan", base: 10 },
  { user_id: "judy", base: 0 },
];

// ===== DOM 工具 =====
const $ = (id) => document.getElementById(id);

// ===== 状态 =====
let giniChart = null;
let history = [];             // { t, gini }
let users = [];               // { user_id, balance }
let tickInterval = null;      // setInterval 句柄
let lastUpdate = Date.now();  // 上次 mint 时间，用于"自然演化"

// ===== Mock 初始化 =====
function initMockData() {
  users = MOCK_USERS.map((u) => ({ user_id: u.user_id, balance: u.base }));
  history = [];
  for (let i = 0; i < 8; i++) {
    history.push({ t: new Date(Date.now() - (8 - i) * 5000), gini: computeGini(users) });
  }
  // 每次启动时立刻打一次基尼，记录
  history.push({ t: new Date(), gini: computeGini(users) });
  lastUpdate = Date.now();
}

// ===== Mock 数据演化（每次 tick 都有可见变化） =====
function evolveMockData() {
  const now = Date.now();
  const elapsed = now - lastUpdate;

  // 1) 持续通胀：每 tick 给非零账户 0~4 的小额 mint（模拟"广告收入"持续进入）
  for (const u of users) {
    if (u.balance > 0) {
      u.balance += Math.random() * 4;
    } else {
      // 零账户偶尔被 mint 进入（5% 概率）
      if (Math.random() < 0.05) {
        u.balance = Math.floor(Math.random() * 50) + 10;
      }
    }
  }
  lastUpdate = now;

  // 2) 模拟 Vickrey 拍卖转账：每 tick 发生 1~3 笔
  //    每次随机选两个不同用户，从"出价高者"转给"出价低者"（缩小贫富差距）
  const txCount = 1 + Math.floor(Math.random() * 3);
  for (let i = 0; i < txCount; i++) {
    const sorted = [...users].sort((a, b) => b.balance - a.balance);
    const from = sorted[Math.floor(Math.random() * Math.min(3, sorted.length))];  // top-3 富者
    const to = sorted[sorted.length - 1 - Math.floor(Math.random() * Math.min(3, sorted.length))];  // bottom-3
    if (!from || !to || from === to) continue;
    const amount = Math.floor(Math.random() * 30) + 5;
    if (from.balance >= amount) {
      from.balance -= amount;
      to.balance += amount;
    }
  }

  // 3) 偶发"鲸鱼出场"（8% 概率）：随机一个用户 +500~5000
  if (Math.random() < 0.08) {
    const idx = Math.floor(Math.random() * users.length);
    users[idx].balance += Math.floor(Math.random() * 4500) + 500;
  }

  // 4) 偶发"小崩盘"（5% 概率）：随机一个用户 -100~500
  if (Math.random() < 0.05) {
    const idx = Math.floor(Math.random() * users.length);
    users[idx].balance = Math.max(0, users[idx].balance - Math.floor(Math.random() * 400) + 100);
  }
}

// ===== 格式化 =====
function fmtInt(n) { if (n == null) return "—"; return Math.round(n).toLocaleString("zh-CN"); }
function fmtFloat(n, digits = 3) { if (n == null) return "—"; return Number(n).toFixed(digits); }
function fmtPct(n) { if (n == null) return "—"; return (n * 100).toFixed(1) + "%"; }
function fmtTime(t) { if (!t) return "—"; return new Date(t).toLocaleTimeString("zh-CN", { hour12: false }); }
function fmtRelative(t) {
  if (!t) return "—";
  const diff = Date.now() - new Date(t).getTime();
  if (diff < 1000) return "刚刚";
  return `${Math.round(diff / 1000)}s 前`;
}
function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

// ===== Mock API（替代真实后端） =====
function mockFetchHealth() {
  return Promise.resolve({
    status: "demo",           // 标记为 demo，区别于真实后端的 "ok"
    service: "equilibria-admin-console (vercel demo)",
    plugin: "centralized (mock)",
    time: new Date().toISOString(),
  });
}

function mockFetchMetrics() {
  evolveMockData();
  const balances = users.reduce((acc, u) => { acc[u.user_id] = u.balance; return acc; }, {});
  const gini = computeGini(users);
  const active = users.filter((u) => u.balance > 0);
  const total = active.reduce((s, u) => s + u.balance, 0);
  const mean = active.length ? total / active.length : 0;
  const sorted = active.map((u) => u.balance).sort((a, b) => a - b);
  const median = sorted.length === 0 ? 0
    : sorted.length % 2 ? sorted[(sorted.length - 1) / 2]
    : (sorted[sorted.length / 2 - 1] + sorted[sorted.length / 2]) / 2;

  // Top 10 集中度
  const topN = Math.min(10, sorted.length);
  const topSum = sorted.slice(-topN).reduce((s, v) => s + v, 0);

  return Promise.resolve({
    gini,
    total_accounts: users.length,
    active_accounts: active.length,
    zero_balance_accounts: users.length - active.length,
    total_supply: total,
    mean,
    median,
    top_concentration_top_n: total > 0 ? topSum / total : 0,
    lorenz_curve: computeLorenz(active.map((u) => u.balance)),
    timestamp: new Date().toISOString(),
  });
}

function mockFetchUsers() {
  return Promise.resolve({
    as_of: new Date().toISOString(),
    count: users.length,
    users: [...users].sort((a, b) => b.balance - a.balance),
  });
}

function mockMint({ user_id, amount }) {
  // 模拟通胀：实际到账 = amount * 0.05（与 centralized 插件一致）
  const actual = Math.max(1, Math.floor(amount * 0.05));
  const existing = users.find((u) => u.user_id === user_id);
  if (existing) {
    existing.balance += actual;
  } else {
    users.push({ user_id, balance: actual });
  }
  return Promise.resolve({
    tx_id: `mock-mint-${Date.now()}-${user_id}`,
    new_balance: existing ? existing.balance : actual,
    accepted: true,
    message: `minted ${actual} (mock, reason: demo)`,
  });
}

// ===== 基尼系数计算（前端版，与 Go 端 metrics 包一致） =====
function computeGini(accounts) {
  const values = accounts.map((u) => Math.max(0, u.balance)).sort((a, b) => a - b);
  const n = values.length;
  if (n === 0) return 0;
  const total = values.reduce((s, v) => s + v, 0);
  if (total === 0) return 0;
  let weighted = 0;
  for (let i = 0; i < n; i++) {
    weighted += (2 * (i + 1) - n - 1) * values[i];
  }
  const gini = weighted / (n * total);
  return Math.max(0, Math.min(1, gini));
}

function computeLorenz(values) {
  if (values.length === 0) return [{ x: 0, y: 0 }, { x: 1, y: 0 }];
  const sorted = [...values].sort((a, b) => a - b);
  const total = sorted.reduce((s, v) => s + v, 0);
  if (total === 0) return [{ x: 0, y: 0 }, { x: 1, y: 0 }];
  const n = sorted.length;
  const points = [{ x: 0, y: 0 }];
  let cum = 0;
  for (let i = 0; i < n; i++) {
    cum += sorted[i];
    points.push({ x: (i + 1) / n, y: cum / total });
  }
  return points;
}

// ===== Chart.js =====
function initChart() {
  const ctx = $("gini-chart").getContext("2d");
  giniChart = new Chart(ctx, {
    type: "bar",
    data: {
      labels: [],
      datasets: [{
        label: "基尼系数",
        data: [],
        backgroundColor: [],
        borderColor: "rgba(94, 234, 212, 0.9)",
        borderWidth: 1,
        borderRadius: 4,
      }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 250 },
      scales: {
        y: { min: 0, max: 1, ticks: { color: "#8a94a6", font: { family: "JetBrains Mono, monospace" } }, grid: { color: "rgba(138, 148, 166, 0.1)" }, title: { display: true, text: "Gini", color: "#8a94a6" } },
        x: { ticks: { color: "#8a94a6", maxRotation: 0, autoSkip: true, maxTicksLimit: 8 }, grid: { display: false } },
      },
      plugins: { legend: { display: false }, tooltip: { backgroundColor: "#1a1f2e", borderColor: "#2c3445", borderWidth: 1, titleColor: "#e4e6eb", bodyColor: "#e4e6eb" } },
    },
    plugins: [{
      id: "alertLine",
      afterDraw(chart) {
        const yScale = chart.scales.y;
        if (!yScale) return;
        const y = yScale.getPixelForValue(GINI_ALERT_THRESHOLD);
        const ctx = chart.ctx;
        const left = chart.chartArea.left;
        const right = chart.chartArea.right;
        ctx.save();
        ctx.setLineDash([6, 4]);
        ctx.strokeStyle = "#facc15";
        ctx.lineWidth = 1.2;
        ctx.beginPath();
        ctx.moveTo(left, y);
        ctx.lineTo(right, y);
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.fillStyle = "#facc15";
        ctx.font = "11px JetBrains Mono, monospace";
        ctx.fillText(`警戒 ${GINI_ALERT_THRESHOLD}`, right - 70, y - 4);
        ctx.restore();
      },
    }],
  });
}

// ===== 渲染 =====
let prevMetrics = null;
function pulseEl(el) {
  el.classList.remove("kpi__value--pulse");
  // 触发重排后再加 class，确保动画重新播放
  void el.offsetWidth;
  el.classList.add("kpi__value--pulse");
}

function renderKpis(m) {
  $("kpi-gini").textContent = fmtFloat(m.gini, 3);
  $("kpi-gini").className = "kpi__value";
  if (m.gini >= GINI_ALERT_THRESHOLD * 1.5) $("kpi-gini").classList.add("kpi__value--critical");
  else if (m.gini >= GINI_ALERT_THRESHOLD) $("kpi-gini").classList.add("kpi__value--high");

  $("kpi-accounts").textContent = fmtInt(m.total_accounts);
  $("kpi-active").textContent = fmtInt(m.active_accounts);
  $("kpi-supply").textContent = fmtInt(m.total_supply);
  $("kpi-mean").textContent = fmtFloat(m.mean, 1);
  $("kpi-median").textContent = fmtFloat(m.median, 1);
  $("kpi-top").textContent = fmtPct(m.top_concentration_top_n);
  $("kpi-updated").textContent = fmtTime(m.timestamp) + " (" + fmtRelative(m.timestamp) + ")";

  // 数据变化时短暂高亮（绿色脉冲），让"动态"看得见
  if (prevMetrics) {
    const changed = ["gini", "total_supply", "mean", "median", "top_concentration_top_n"];
    for (const k of changed) {
      if (Math.abs((m[k] || 0) - (prevMetrics[k] || 0)) > 0.001) {
        const el = $("kpi-" + k.replace("top_concentration_top_n", "top").replace("_supply", "-supply").replace("accounts", "accounts").replace("active", "active"));
        if (el) pulseEl(el);
      }
    }
  }
  prevMetrics = m;
}

function pushHistory(m) {
  history.push({ t: new Date(m.timestamp), gini: m.gini });
  if (history.length > HISTORY_CAP) history.shift();
}

function renderChart() {
  if (!giniChart) return;
  giniChart.data.labels = history.map((h) => fmtTime(h.t));
  giniChart.data.datasets[0].data = history.map((h) => h.gini);
  giniChart.data.datasets[0].backgroundColor = history.map((h) => {
    if (h.gini >= GINI_ALERT_THRESHOLD * 1.5) return "rgba(248, 113, 113, 0.8)";
    if (h.gini >= GINI_ALERT_THRESHOLD) return "rgba(250, 204, 21, 0.8)";
    return "rgba(94, 234, 212, 0.8)";
  });
  giniChart.update("none");
}

function renderUsers(payload) {
  const list = payload.users || [];
  const tbody = $("users-table").querySelector("tbody");
  if (list.length === 0) {
    tbody.innerHTML = `<tr><td colspan="3" class="muted">暂无数据</td></tr>`;
    return;
  }
  // 记录上一次渲染的余额（按 user_id 索引），用于检测"变化"并高亮
  const prevBalances = (renderUsers._prev) || {};
  const total = list.reduce((s, u) => s + u.balance, 0);
  const rows = list.map((u) => {
    const pct = total > 0 ? (u.balance / total * 100).toFixed(1) + "%" : "—";
    const changed = prevBalances[u.user_id] !== undefined &&
                    Math.abs(prevBalances[u.user_id] - u.balance) > 0.5;
    return `<tr class="${changed ? "row--changed" : ""}">
      <td>${escapeHtml(u.user_id)}</td>
      <td>${fmtInt(u.balance)}</td>
      <td>${pct}</td>
    </tr>`;
  }).join("");
  tbody.innerHTML = rows;
  // 保存当前快照
  const next = {};
  for (const u of list) next[u.user_id] = u.balance;
  renderUsers._prev = next;
}

function renderHealth(data) {
  const pill = $("health-pill");
  if (data.status === "demo") {
    pill.textContent = "● Demo 模式";
    pill.className = "pill pill--pending";   // 灰色，区别于真实后端的"绿"
    pill.title = "前端 mock 数据，无后端服务";
  } else if (data.status === "ok") {
    pill.textContent = "● ok";
    pill.className = "pill pill--ok";
  } else {
    pill.textContent = "● 离线";
    pill.className = "pill pill--err";
  }
}

// ===== 主循环 =====
async function tick() {
  try {
    const m = await mockFetchMetrics();
    renderKpis(m);
    pushHistory(m);
    renderChart();
  } catch (e) {
    console.error("metrics failed", e);
  }
  try {
    const list = await mockFetchUsers();
    renderUsers(list);
  } catch (e) {
    console.error("users failed", e);
  }
}

async function checkHealth() {
  const data = await mockFetchHealth();
  renderHealth(data);
}

// ===== 发行表单 =====
function bindMintForm() {
  const form = $("mint-form");
  const result = $("mint-result");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    result.className = "result";
    result.textContent = "提交中…";
    const body = {
      user_id: $("mint-user").value.trim() || "alice",
      amount: parseInt($("mint-amount").value, 10) || 1000,
      reason: $("mint-reason").value.trim() || "demo grant",
    };
    try {
      const data = await mockMint(body);
      result.className = "result result--ok";
      result.textContent = `成功！tx=${data.tx_id.slice(0, 24)}… 新余额=${fmtInt(data.new_balance)}`;
      tick();
    } catch (err) {
      result.className = "result result--err";
      result.textContent = "网络错误：" + err.message;
    }
  });
}

// ===== 启动 =====
window.addEventListener("DOMContentLoaded", () => {
  initMockData();
  initChart();
  bindMintForm();
  checkHealth();
  tick();
  tickInterval = setInterval(() => { checkHealth(); tick(); }, REFRESH_INTERVAL_MS);
});
