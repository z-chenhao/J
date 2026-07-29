"use strict";

const API = "/jspace/api";
const TOKEN_KEY = "jspace-viewer-token";

const state = {
  token: sessionStorage.getItem(TOKEN_KEY) || "",
  trace: null,
  summaries: [],
  turn: 0,
  selected: { position: 0, layer: 24 },
  geometry: null,
  streamAbort: null,
};

const ui = {
  accessButton: document.getElementById("access-button"),
  accessDialog: document.getElementById("access-dialog"),
  accessForm: document.getElementById("access-form"),
  accessToken: document.getElementById("access-token"),
  accessError: document.getElementById("access-error"),
  demoButton: document.getElementById("demo-button"),
  connectionDot: document.getElementById("connection-dot"),
  connectionLabel: document.getElementById("connection-label"),
  runSelect: document.getElementById("run-select"),
  runStatus: document.getElementById("run-status"),
  runTime: document.getElementById("run-time"),
  factRun: document.getElementById("fact-run"),
  factModel: document.getElementById("fact-model"),
  factKind: document.getElementById("fact-kind"),
  previousTurn: document.getElementById("previous-turn"),
  nextTurn: document.getElementById("next-turn"),
  turnLabel: document.getElementById("turn-label"),
  canvas: document.getElementById("jspace-canvas"),
  canvasWrap: document.getElementById("canvas-wrap"),
  canvasEmpty: document.getElementById("canvas-empty"),
  tokenAxis: document.getElementById("token-axis"),
  selectedLayer: document.getElementById("selected-layer"),
  selectedPosition: document.getElementById("selected-position"),
  selectedToken: document.getElementById("selected-token"),
  conceptList: document.getElementById("concept-list"),
  inspectorNote: document.getElementById("inspector-note"),
  timeline: document.getElementById("timeline"),
  liveBadge: document.getElementById("live-badge"),
  provenance: document.getElementById("provenance"),
};

function request(path, token = state.token) {
  const headers = token ? { Authorization: `Bearer ${token}` } : {};
  return fetch(`${API}${path}`, { headers, cache: "no-store" });
}

async function loadDemo() {
  const response = await request("/demo", "");
  if (!response.ok) throw new Error("Unable to load demo");
  state.trace = await response.json();
  state.turn = 0;
  state.selected = { position: 0, layer: 24 };
  render();
}

async function authenticate(token) {
  const response = await request("/status", token);
  if (!response.ok) throw new Error(response.status === 401 ? "Invalid viewer token" : "Observation service unavailable");
  state.token = token;
  sessionStorage.setItem(TOKEN_KEY, token);
  setConnection("online", "Connected · read-only");
  await refreshRuns();
  startStream();
}

async function refreshRuns(preferredID = "") {
  const response = await request("/runs");
  if (!response.ok) {
    if (response.status === 401) disconnect();
    throw new Error("Unable to load observations");
  }
  state.summaries = await response.json();
  renderRunOptions();
  const target = preferredID ||
    (ui.runSelect.value !== "demo" ? ui.runSelect.value : state.summaries[0]?.id);
  if (target && target !== "demo") {
    await loadRun(target);
  } else if (!state.summaries.length) {
    await loadDemo();
  }
}

async function loadRun(id) {
  if (id === "demo") {
    await loadDemo();
    return;
  }
  const response = await request(`/runs/${encodeURIComponent(id)}`);
  if (!response.ok) throw new Error("Unable to load this observation");
  state.trace = await response.json();
  state.turn = Math.max(0, (state.trace.turns?.length || 1) - 1);
  state.selected = { position: 0, layer: workspaceLayer(state.trace) };
  render();
  ui.runSelect.value = id;
}

function renderRunOptions() {
  const current = ui.runSelect.value;
  ui.runSelect.replaceChildren();
  const demo = document.createElement("option");
  demo.value = "demo";
  demo.textContent = "Interface demo · not measured";
  ui.runSelect.append(demo);
  for (const summary of state.summaries) {
    const option = document.createElement("option");
    option.value = summary.id;
    option.textContent = `${summary.label} · ${summary.status}`;
    ui.runSelect.append(option);
  }
  if ([...ui.runSelect.options].some((option) => option.value === current)) {
    ui.runSelect.value = current;
  }
}

function disconnect() {
  state.token = "";
  sessionStorage.removeItem(TOKEN_KEY);
  state.streamAbort?.abort();
  setConnection("error", "Viewer token required");
}

function setConnection(kind, label) {
  ui.connectionDot.className = `status-dot ${kind || ""}`.trim();
  ui.connectionLabel.textContent = label;
  ui.accessButton.textContent = kind === "online" ? "Change access" : "Open observations";
}

async function startStream() {
  state.streamAbort?.abort();
  const controller = new AbortController();
  state.streamAbort = controller;
  try {
    const response = await fetch(`${API}/stream`, {
      headers: { Authorization: `Bearer ${state.token}` },
      cache: "no-store",
      signal: controller.signal,
    });
    if (!response.ok || !response.body) return;
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (!controller.signal.aborted) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const events = buffer.split("\n\n");
      buffer = events.pop() || "";
      for (const event of events) {
        const dataLine = event.split("\n").find((line) => line.startsWith("data: "));
        if (!dataLine) continue;
        state.summaries = JSON.parse(dataLine.slice(6));
        renderRunOptions();
        const active = state.trace?.id;
        if (active && active !== "demo" && state.summaries.some((run) => run.id === active)) {
          await loadRun(active);
        }
      }
    }
    if (!controller.signal.aborted && state.token) {
      window.setTimeout(startStream, 1500);
    }
  } catch (error) {
    if (error.name !== "AbortError") {
      setConnection("error", "Reconnecting");
      if (state.token) window.setTimeout(startStream, 2000);
    }
  }
}

function render() {
  if (!state.trace) return;
  const trace = state.trace;
  const turns = trace.turns || [];
  state.turn = clamp(state.turn, 0, Math.max(0, turns.length - 1));
  const turn = turns[state.turn];

  ui.factRun.textContent = trace.id === "demo" ? "DEMO" : trace.id;
  ui.factModel.textContent = compactModel(trace.agent?.model || "UNKNOWN");
  ui.factKind.textContent = humanKind(trace.measurement?.kind);
  ui.runStatus.textContent = (trace.status || "unknown").toUpperCase();
  ui.runTime.textContent = formatTime(trace.updatedAt);
  ui.liveBadge.textContent = ["running", "probing"].includes(trace.status) ? "IN PROGRESS" : "RECORDED";
  ui.turnLabel.textContent = turns.length
    ? `TURN ${pad(state.turn + 1)} / ${pad(turns.length)}`
    : "NO J-LENS TURN";
  ui.previousTurn.disabled = state.turn <= 0;
  ui.nextTurn.disabled = state.turn >= turns.length - 1;
  ui.inspectorNote.textContent = trace.measurement?.kind === "illustrative"
    ? "Interface sample only. Not a model measurement."
    : "Ranked token directions after Jacobian transport.";

  renderField(turn);
  renderTimeline(trace.events || []);
  renderProvenance(trace);
}

function renderField(turn) {
  const positions = turn?.selectedPositions || [];
  ui.canvasEmpty.hidden = positions.length > 0;
  ui.tokenAxis.replaceChildren();
  ui.tokenAxis.style.gridTemplateColumns = `repeat(${Math.max(1, positions.length)}, minmax(0, 1fr))`;
  for (const position of positions) {
    const label = document.createElement("span");
    label.textContent = visibleToken(position.token);
    label.title = position.token;
    ui.tokenAxis.append(label);
  }
  if (!positions.length) {
    drawField([]);
    renderInspector(null, null);
    return;
  }

  const maxLayer = Math.max(...positions.flatMap((position) => position.layers.map((layer) => layer.layer)));
  state.selected.position = clamp(state.selected.position, 0, positions.length - 1);
  state.selected.layer = clamp(state.selected.layer, 0, maxLayer);
  drawField(positions);
  const position = positions[state.selected.position];
  const layer = nearestLayer(position.layers, state.selected.layer);
  renderInspector(position, layer);
}

function drawField(positions) {
  const canvas = ui.canvas;
  const rect = ui.canvasWrap.getBoundingClientRect();
  const cssWidth = Math.max(280, Math.floor(rect.width));
  const cssHeight = window.innerWidth <= 620 ? 340 : 390;
  const scale = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = cssWidth * scale;
  canvas.height = cssHeight * scale;
  canvas.style.width = `${cssWidth}px`;
  canvas.style.height = `${cssHeight}px`;
  const context = canvas.getContext("2d");
  context.setTransform(scale, 0, 0, scale, 0, 0);
  context.clearRect(0, 0, cssWidth, cssHeight);

  if (!positions.length) {
    state.geometry = null;
    return;
  }

  const left = window.innerWidth <= 620 ? 34 : 48;
  const top = 8;
  const bottom = 10;
  const width = cssWidth - left - 2;
  const height = cssHeight - top - bottom;
  const maxLayer = Math.max(...positions.flatMap((position) => position.layers.map((layer) => layer.layer)));
  const rows = maxLayer + 1;
  const cellWidth = width / positions.length;
  const cellHeight = height / rows;
  const computed = getComputedStyle(document.documentElement);
  const signal = computed.getPropertyValue("--signal").trim();
  const rule = computed.getPropertyValue("--rule").trim();
  const dim = computed.getPropertyValue("--dim").trim();
  const ink = computed.getPropertyValue("--ink").trim();
  const scores = positions
    .flatMap((position) => position.layers)
    .map((layer) => Number(layer?.top?.[0]?.score))
    .filter(Number.isFinite);
  const scoreRange = {
    min: Math.min(...scores),
    max: Math.max(...scores),
  };

  context.font = "9px ui-monospace, monospace";
  context.textAlign = "right";
  context.textBaseline = "middle";
  context.fillStyle = dim;
  for (let layer = 0; layer <= maxLayer; layer += 4) {
    const y = top + height - (layer + .5) * cellHeight;
    context.fillText(`L${String(layer).padStart(2, "0")}`, left - 7, y);
    context.strokeStyle = layer % 8 === 0 ? rule : "rgba(255,255,255,.06)";
    context.lineWidth = 1;
    context.beginPath();
    context.moveTo(left, Math.round(y) + .5);
    context.lineTo(cssWidth, Math.round(y) + .5);
    context.stroke();
  }

  for (let positionIndex = 0; positionIndex < positions.length; positionIndex++) {
    const position = positions[positionIndex];
    const byLayer = new Map(position.layers.map((layer) => [layer.layer, layer]));
    for (let layerIndex = 0; layerIndex < rows; layerIndex++) {
      const layer = byLayer.get(layerIndex);
      const strength = strengthOf(layer, scoreRange);
      const x = left + positionIndex * cellWidth;
      const y = top + height - (layerIndex + 1) * cellHeight;
      if (layer) {
        context.fillStyle = colorWithAlpha(signal, .10 + strength * .82);
        context.fillRect(x + 1, y + 1, Math.max(1, cellWidth - 2), Math.max(1, cellHeight - 2));
      } else {
        context.fillStyle = "rgba(255,255,255,.018)";
        context.fillRect(x + 1, y + 1, Math.max(1, cellWidth - 2), Math.max(1, cellHeight - 2));
      }
    }
  }

  const selectedX = left + state.selected.position * cellWidth;
  const selectedY = top + height - (state.selected.layer + 1) * cellHeight;
  context.strokeStyle = ink;
  context.lineWidth = 1.5;
  context.strokeRect(
    selectedX + .75,
    selectedY + .75,
    Math.max(1, cellWidth - 1.5),
    Math.max(1, cellHeight - 1.5),
  );

  state.geometry = { left, top, width, height, rows, cellWidth, cellHeight, positions };
}

function renderInspector(position, layer) {
  ui.conceptList.replaceChildren();
  if (!position || !layer) {
    ui.selectedLayer.textContent = "—";
    ui.selectedPosition.textContent = "—";
    ui.selectedToken.textContent = "No readout";
    return;
  }
  ui.selectedLayer.textContent = `L${String(layer.layer).padStart(2, "0")}`;
  ui.selectedPosition.textContent = `P${signedPosition(position.index)}`;
  ui.selectedToken.textContent = `“${visibleToken(position.token)}”`;
  for (const concept of layer.top || []) {
    const item = document.createElement("li");
    const rank = document.createElement("span");
    rank.className = "concept-rank";
    rank.textContent = `#${concept.rank}`;
    const token = document.createElement("span");
    token.className = "concept-token";
    token.textContent = visibleToken(concept.token);
    const score = document.createElement("span");
    score.className = "concept-score";
    score.textContent = Number.isFinite(concept.score) ? concept.score.toFixed(3) : "—";
    item.append(rank, token, score);
    ui.conceptList.append(item);
  }
}

function renderTimeline(events) {
  ui.timeline.replaceChildren();
  if (!events.length) {
    const empty = document.createElement("p");
    empty.className = "timeline-detail";
    empty.textContent = "No Agent events.";
    ui.timeline.append(empty);
    return;
  }
  for (const event of events) {
    const item = document.createElement("div");
    item.className = `timeline-event${event.isError ? " error" : ""}`;
    item.setAttribute("role", "listitem");
    const offset = document.createElement("span");
    offset.className = "timeline-offset";
    offset.textContent = formatDuration(event.offsetMs);
    const type = document.createElement("span");
    type.className = "timeline-type";
    type.textContent = [event.type, event.subagent, event.tool].filter(Boolean).join(" · ");
    const detail = document.createElement("span");
    detail.className = "timeline-detail";
    detail.textContent = event.durationMs != null
      ? formatDuration(event.durationMs)
      : event.stopReason || (event.outputBytes ? `${event.outputBytes} B` : "");
    item.append(offset, type, detail);
    ui.timeline.append(item);
  }
}

function renderProvenance(trace) {
  const measurement = trace.measurement || {};
  const rows = [
    ["Mode", humanKind(measurement.kind)],
    ["Checkpoint", measurement.modelCheckpoint || "—"],
    ["Quantization", measurement.runtimeQuantization || "—"],
    ["J-lens", measurement.lensRepository || "—"],
    ["Shape", `${measurement.layers || "—"} layers × ${measurement.residualWidth || "—"} residual`],
    ["Context", measurement.contextFidelity || "—"],
  ];
  ui.provenance.replaceChildren();
  for (const [name, value] of rows) {
    const row = document.createElement("div");
    const term = document.createElement("dt");
    term.textContent = name;
    const description = document.createElement("dd");
    description.textContent = value;
    row.append(term, description);
    ui.provenance.append(row);
  }
}

function nearestLayer(layers, wanted) {
  return layers.reduce((best, layer) => (
    !best || Math.abs(layer.layer - wanted) < Math.abs(best.layer - wanted) ? layer : best
  ), null);
}

function strengthOf(layer, range) {
  const score = Number(layer?.top?.[0]?.score);
  if (!Number.isFinite(score)) return 0;
  if (!Number.isFinite(range.min) || range.max <= range.min) return .55;
  return clamp((score - range.min) / (range.max - range.min), .06, 1);
}

function workspaceLayer(trace) {
  const layers = trace?.measurement?.layers || 40;
  return Math.round(layers * .6);
}

function compactModel(model) {
  return model.replace("Qwen3.6-", "QWEN 3.6 · ").replace("-oQ4e-mtp", " · OQ4E MTP").toUpperCase();
}

function humanKind(kind) {
  const values = {
    posthoc_replay: "J-LENS POST-HOC REPLAY",
    illustrative: "ILLUSTRATIVE · NOT MEASURED",
    unavailable: "MEASUREMENT UNAVAILABLE",
    pending: "PENDING",
  };
  return values[kind] || String(kind || "UNKNOWN").toUpperCase();
}

function visibleToken(token) {
  const value = String(token ?? "").replaceAll("Ġ", "·").replaceAll("▁", "·").replaceAll("\n", "↵");
  return value || "∅";
}

function signedPosition(index) {
  const number = Number(index);
  return number >= 0 ? `+${number}` : String(number).replace("-", "−");
}

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date);
}

function formatDuration(milliseconds) {
  const value = Number(milliseconds) || 0;
  if (value < 1000) return `${value}ms`;
  return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)}s`;
}

function colorWithAlpha(hex, alpha) {
  const value = hex.replace("#", "");
  const number = Number.parseInt(value.length === 3
    ? value.split("").map((part) => part + part).join("")
    : value, 16);
  const red = (number >> 16) & 255;
  const green = (number >> 8) & 255;
  const blue = number & 255;
  return `rgba(${red},${green},${blue},${alpha})`;
}

function pad(value) {
  return String(value).padStart(2, "0");
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value));
}

ui.accessButton.addEventListener("click", () => {
  ui.accessError.textContent = "";
  ui.accessToken.value = state.token;
  ui.accessDialog.showModal();
  ui.accessToken.focus();
});

ui.accessForm.addEventListener("submit", async (event) => {
  if (event.submitter?.value !== "submit") return;
  event.preventDefault();
  ui.accessError.textContent = "";
  try {
    await authenticate(ui.accessToken.value.trim());
    ui.accessDialog.close();
  } catch (error) {
    ui.accessError.textContent = error.message;
  }
});

ui.demoButton.addEventListener("click", async () => {
  ui.accessDialog.close();
  await loadDemo();
});

ui.runSelect.addEventListener("change", async () => {
  try {
    await loadRun(ui.runSelect.value);
  } catch (error) {
    setConnection("error", error.message);
  }
});

ui.previousTurn.addEventListener("click", () => {
  state.turn = Math.max(0, state.turn - 1);
  state.selected = { position: 0, layer: workspaceLayer(state.trace) };
  render();
});

ui.nextTurn.addEventListener("click", () => {
  state.turn = Math.min((state.trace?.turns?.length || 1) - 1, state.turn + 1);
  state.selected = { position: 0, layer: workspaceLayer(state.trace) };
  render();
});

ui.canvas.addEventListener("pointerdown", (event) => {
  const geometry = state.geometry;
  if (!geometry) return;
  const rect = ui.canvas.getBoundingClientRect();
  const x = event.clientX - rect.left;
  const y = event.clientY - rect.top;
  if (x < geometry.left || y < geometry.top || x > geometry.left + geometry.width ||
      y > geometry.top + geometry.height) return;
  state.selected.position = clamp(
    Math.floor((x - geometry.left) / geometry.cellWidth),
    0,
    geometry.positions.length - 1,
  );
  state.selected.layer = clamp(
    geometry.rows - 1 - Math.floor((y - geometry.top) / geometry.cellHeight),
    0,
    geometry.rows - 1,
  );
  renderField(state.trace.turns[state.turn]);
});

let resizeTimer = 0;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = window.setTimeout(() => {
    if (state.trace) renderField(state.trace.turns?.[state.turn]);
  }, 120);
});

(async function bootstrap() {
  try {
    await loadDemo();
    if (state.token) {
      await authenticate(state.token);
    } else {
      const local = await request("/status", "");
      if (local.ok) {
        setConnection("online", "Local · read-only");
        await refreshRuns();
        startStream();
      } else {
        setConnection("", "Public demo");
      }
    }
  } catch (error) {
    setConnection("error", error.message);
  }
})();
