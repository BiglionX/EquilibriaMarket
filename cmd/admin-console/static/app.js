// Equilibria Admin Console 前端逻辑
//
// 功能：
//   1. 健康检查 → 更新右上角 pill
//   2. 拉取 /api/metrics → 刷新 KPI + Chart.js 柱状图
//   3. 拉取 /api/users → 刷新用户余额表
//   4. 提交发行表单 → POST /api/mint → 立即刷新

const REFRESH_INTERVAL_MS = 3000;        // 拉取指标频率
const HISTORY_CAP = 30;                  // 柱状图保留的最近采样点
const GINI_ALERT_THRESHOLD = 0.4;        // 经验警戒线

// ===== DOM 工具 =====
const $ = (id) => document.getElementById(id);

// ===== 状态 =====
let giniChart = null;
let history = []; // { t: Date, gini: number }

// ===== 格式化 =====
function fmtInt(n) {
  if (n == null) return "—";
  return Math.round(n).toLocaleString("zh-CN");
}
function fmtFloat(n, digits = 3) {
  if (n == null) return "—";
  return Number(n).toFixed(digits);
}
function fmtPct(n) {
  if (n == null) return "—";
  return (n * 100).toFixed(1) + "%";
}
function fmtTime(t) {
  if (!t) return "—";
  return new Date(t).toLocaleTimeString("zh-CN", { hour12: false });
}
function fmtRelative(t) {
  if (!t) return "—";
  const diff = Date.now() - new Date(t).getTime();
  if (diff < 0) return "刚刚";
  if (diff < 1000) return "刚刚";
  return `${Math.round(diff / 1000)}s 前`;
}

// ===== Chart.js 初始化 =====
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
        y: {
          min: 0,
          max: 1,
          ticks: { color: "#8a94a6", font: { family: "JetBrains Mono, monospace" } },
          grid: { color: "rgba(138, 148, 166, 0.1)" },
          title: { display: true, text: "Gini", color: "#8a94a6" },
        },
        x: {
          ticks: { color: "#8a94a6", maxRotation: 0, autoSkip: true, maxTicksLimit: 8 },
          grid: { display: false },
        },
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: "#1a1f2e",
          borderColor: "#2c3445",
          borderWidth: 1,
          titleColor: "#e4e6eb",
          bodyColor: "#e4e6eb",
        },
      },
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

// ===== 拉取 /api/health =====
async function checkHealth() {
  const pill = $("health-pill");
  try {
    const r = await fetch("/api/health");
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const data = await r.json();
    pill.textContent = "● " + (data.status || "ok");
    pill.className = "pill pill--ok";
  } catch (e) {
    pill.textContent = "● 离线";
    pill.className = "pill pill--err";
  }
}

// ===== 拉取 /api/metrics =====
async function fetchMetrics() {
  const r = await fetch("/api/metrics");
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

// ===== 拉取 /api/users =====
async function fetchUsers() {
  const r = await fetch("/api/users");
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
  return r.json();
}

// ===== 刷新 KPI =====
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
}

// ===== 刷新柱状图 =====
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

// ===== 刷新用户表 =====
function renderUsers(list) {
  const tbody = $("users-table").querySelector("tbody");
  if (!list || list.length === 0) {
    tbody.innerHTML = `<tr><td colspan="3" class="muted">暂无数据</td></tr>`;
    return;
  }
  const total = list.reduce((s, u) => s + u.balance, 0);
  const rows = list.map((u) => {
    const pct = total > 0 ? (u.balance / total * 100).toFixed(1) + "%" : "—";
    return `<tr>
      <td>${escapeHtml(u.user_id)}</td>
      <td>${fmtInt(u.balance)}</td>
      <td>${pct}</td>
    </tr>`;
  }).join("");
  tbody.innerHTML = rows;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}

// ===== 主循环 =====
async function tick() {
  try {
    const m = await fetchMetrics();
    renderKpis(m);
    pushHistory(m);
    renderChart();
  } catch (e) {
    console.error("metrics fetch failed", e);
  }
  try {
    const data = await fetchUsers();
    // /api/users 返回 {as_of, count, users: [...]}
    renderUsers(data.users || []);
  } catch (e) {
    console.error("users fetch failed", e);
  }
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
      user_id: $("mint-user").value.trim(),
      amount: parseInt($("mint-amount").value, 10),
      reason: $("mint-reason").value.trim() || "admin grant",
    };
    try {
      const r = await fetch("/api/mint", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await r.json();
      if (!r.ok || !data.accepted) {
        result.className = "result result--err";
        result.textContent = "失败：" + (data.message || `HTTP ${r.status}`);
        return;
      }
      result.className = "result result--ok";
      result.textContent = `成功！tx=${data.tx_id.slice(0, 24)}… 新余额=${fmtInt(data.new_balance)}`;
      // 立即刷新一次
      tick();
    } catch (err) {
      result.className = "result result--err";
      result.textContent = "网络错误：" + err.message;
    }
  });
}

// ===== 启动 =====
window.addEventListener("DOMContentLoaded", () => {
  initChart();
  bindMintForm();
  checkHealth();
  tick();
  setInterval(() => { checkHealth(); tick(); }, REFRESH_INTERVAL_MS);
});
