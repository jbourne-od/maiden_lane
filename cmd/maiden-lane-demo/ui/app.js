"use strict";

// Everything this page shows is derived from an API response. It does not simulate
// the engine, does not cache verdicts, and never fills in a value it did not
// receive: a demo that guessed would be demonstrating the guess.

let settings = null;      // committed payloads and endpoints, from /demo/settings
let plan = null;          // the compiled plan, from POST /v1/plans
let observation = null;   // the editable driver observations
let previous = null;      // the last run, so identity changes can be pointed out
let exchange = [];        // the literal requests and responses, for the raw panel
let planExchange = null;  // kept separately: the plan is compiled once, at load
let rawSelection = 0;

const el = (id) => document.getElementById(id);

// The engine's closed refusal codes, each with what it actually means for the
// drivers in front of you. The codes come from the service; these sentences are
// this page's explanation of them, which is the whole job of a demo client.
const CODE_MEANINGS = {
  TEAM_ASSIGNMENT_KEY_MISMATCH: {
    title: "These drivers are not on the same assignment",
    detail:
      "Forming a team requires the drivers to share an assignment key. They do not, " +
      "so there is no team to form and no later question to ask. Nothing was sealed, " +
      "because nothing was established.",
  },
  HOS_ANCHOR_MISMATCH: {
    title: "The drivers are not describing the same duty period",
    detail:
      "Each driver reports an anchor identifying the period their hours belong to, " +
      "and the anchors disagree. Adding hours across different periods produces a " +
      "number with no meaning, so the aggregation boundary refused rather than " +
      "emitting one.",
  },
  HOS_DURATION_INVALID: {
    title: "The reported hours cannot be true together",
    detail:
      "Hours must be non-negative, and driving hours cannot exceed elapsed hours. " +
      "One of those does not hold, so the engine refuses instead of aggregating a " +
      "figure that contradicts itself.",
  },
  HOS_TUPLE_INCOMPLETE: {
    title: "A driver's observation is incomplete",
    detail:
      "Aggregation requires each driver's full tuple: anchor, elapsed hours, and " +
      "driving hours. One is absent. A missing input is not a zero, so the engine " +
      "refuses rather than substituting one.",
  },
  HOS_AGGREGATE_INVALID: {
    title: "The aggregate the rule produced does not hold up",
    detail:
      "The inputs were acceptable but the emitted team record failed its own " +
      "postconditions. The engine checks its own output before committing it.",
  },
};

// Why each phase of a run exists, shown alongside it rather than in documentation
// nobody opens during a demo.
const RULE_MEANINGS = {
  "form_team.v1":
    "Groups the drivers sharing an assignment into one team entity and relates them to it.",
  "aggregate_team_hos.v1":
    "Reconciles the drivers' HOS observations into one team tuple, but only if they are consistent.",
};

const CHECKPOINT_MEANINGS = {
  "team_formed.v1": "The team exists and its membership is settled.",
  "team_hos_aggregated.v1": "The team's reconciled hours are settled.",
};

const PROFILE_MEANINGS = {
  "cm.v1": "A consumer that only needs to know the team exists.",
  "optimizer.v1": "A consumer that needs the reconciled hours as well.",
};

// ── input model ─────────────────────────────────────────────────────────────

const FIELDS = [
  { name: "assignment_key", kind: "string", label: "assignment_key" },
  { name: "hos_anchor", kind: "atom", label: "hos_anchor" },
  { name: "hos_elapsed_hours", kind: "int64", label: "hos_elapsed_hours" },
  { name: "hos_driving_hours", kind: "int64", label: "hos_driving_hours" },
];

// Presets are the inputs verified to reach distinct outcomes. Each is a real
// observation, not a canned response.
const PRESETS = [
  {
    id: "consistent",
    label: "Consistent",
    apply: (drivers) => {
      set(drivers[0], { assignment_key: "X", hos_anchor: "T0", hos_elapsed_hours: 10, hos_driving_hours: 8 });
      set(drivers[1], { assignment_key: "X", hos_anchor: "T0", hos_elapsed_hours: 7, hos_driving_hours: 6 });
    },
  },
  {
    id: "anchor",
    label: "Different duty periods",
    apply: (drivers) => {
      PRESETS[0].apply(drivers);
      drivers[1].fields.hos_anchor.value = "T1";
    },
  },
  {
    id: "duration",
    label: "Driving exceeds elapsed",
    apply: (drivers) => {
      PRESETS[0].apply(drivers);
      drivers[0].fields.hos_driving_hours.value = 99;
    },
  },
  {
    id: "negative",
    label: "Negative hours",
    apply: (drivers) => {
      PRESETS[0].apply(drivers);
      drivers[1].fields.hos_elapsed_hours.value = -1;
    },
  },
  {
    id: "incomplete",
    label: "Missing observation",
    apply: (drivers) => {
      PRESETS[0].apply(drivers);
      drivers[1].fields.hos_driving_hours.omitted = true;
    },
  },
  {
    id: "assignment",
    label: "Different assignments",
    apply: (drivers) => {
      PRESETS[0].apply(drivers);
      drivers[1].fields.assignment_key.value = "Y";
    },
  },
];

function set(driver, values) {
  for (const field of FIELDS) {
    driver.fields[field.name].value = values[field.name];
    driver.fields[field.name].omitted = false;
  }
}

// observationFromExample seeds the editable model from the committed payload, so
// the page opens on the same input the test suite pins to the ratified fixture.
function observationFromExample(execution) {
  return {
    planID: execution.planID,
    lineage: execution.initialState.lineage,
    world: execution.world,
    executorIdentity: execution.executorIdentity,
    provenancePolicy: execution.provenancePolicy,
    relations: execution.initialState.relations || [],
    drivers: execution.initialState.entities.map((entity) => ({
      kind: entity.kind,
      sourceKey: entity.canonicalSourceKey,
      fields: Object.fromEntries(
        FIELDS.map((field) => {
          const supplied = entity.fields[field.name];
          return [field.name, {
            value: supplied === undefined ? "" : (supplied.string ?? supplied.atom ?? supplied.int64),
            omitted: supplied === undefined,
          }];
        }),
      ),
    })),
  };
}

// executionRequest rebuilds the wire body from the edited model. An omitted field
// is left out entirely rather than sent empty, because a missing observation and a
// blank one are different claims and the engine treats them differently.
function executionRequest() {
  return {
    planID: plan ? plan.planID : observation.planID,
    initialState: {
      lineage: observation.lineage,
      entities: observation.drivers.map((driver) => {
        const fields = {};
        for (const field of FIELDS) {
          const held = driver.fields[field.name];
          if (held.omitted) continue;
          if (field.kind === "int64") {
            fields[field.name] = { kind: "int64", int64: Number(held.value) };
          } else {
            fields[field.name] = { kind: field.kind, [field.kind]: String(held.value) };
          }
        }
        return { kind: driver.kind, canonicalSourceKey: driver.sourceKey, fields };
      }),
      relations: observation.relations,
    },
    world: observation.world,
    executorIdentity: observation.executorIdentity,
    provenancePolicy: observation.provenancePolicy,
  };
}

// ── API ─────────────────────────────────────────────────────────────────────

async function call(method, path, body) {
  const options = {
    method,
    headers: { "X-Maiden-Lane-Tenant": settings.tenant },
  };
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body, null, 2);
  }
  const response = await fetch(path, options);
  const text = await response.text();
  let parsed = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    parsed = { raw: text };
  }
  return { status: response.status, body: parsed };
}

function record(label, request, response) {
  exchange.push({ label, request, response });
}

// ── rendering ───────────────────────────────────────────────────────────────

function renderMasthead() {
  el("masthead-meta").innerHTML = [
    `tenant ${escapeHTML(settings.tenant)}`,
    `plan ${plan ? shorten(plan.planID) : "not compiled"}`,
  ].map((line) => `<span>${line}</span>`).join("");
  el("footer-source").textContent =
    `payloads from ${settings.sourcePath} — every call on this page is the documented /v1 API`;
}

function renderPresets() {
  el("presets").innerHTML = PRESETS.map(
    (preset) => `<button type="button" data-preset="${preset.id}" aria-pressed="false">${escapeHTML(preset.label)}</button>`,
  ).join("");
  for (const button of el("presets").querySelectorAll("button")) {
    button.addEventListener("click", () => {
      const preset = PRESETS.find((candidate) => candidate.id === button.dataset.preset);
      preset.apply(observation.drivers);
      renderDrivers();
      markPreset(preset.id);
    });
  }
}

function markPreset(id) {
  for (const button of el("presets").querySelectorAll("button")) {
    button.setAttribute("aria-pressed", String(button.dataset.preset === id));
  }
}

function renderDrivers() {
  el("drivers").innerHTML = observation.drivers.map((driver, index) => `
    <div class="driver">
      <div class="driver-name">driver ${escapeHTML(driver.sourceKey)}</div>
      ${FIELDS.map((field) => {
        const held = driver.fields[field.name];
        return `
          <div class="field${held.omitted ? " omitted" : ""}">
            <label for="d${index}-${field.name}">${escapeHTML(field.label)}</label>
            <input id="d${index}-${field.name}"
                   data-driver="${index}" data-field="${field.name}"
                   type="${field.kind === "int64" ? "number" : "text"}"
                   value="${escapeHTML(String(held.value))}"
                   ${held.omitted ? "disabled" : ""}>
            <button type="button" class="field-omit"
                    data-driver="${index}" data-field="${field.name}"
                    aria-pressed="${held.omitted}"
                    title="${held.omitted ? "Supply this observation" : "Omit this observation entirely"}"
              >${held.omitted ? "+" : "×"}</button>
          </div>`;
      }).join("")}
    </div>`).join("");

  for (const input of el("drivers").querySelectorAll("input")) {
    input.addEventListener("input", () => {
      observation.drivers[Number(input.dataset.driver)].fields[input.dataset.field].value = input.value;
      markPreset(null);
    });
  }
  for (const button of el("drivers").querySelectorAll(".field-omit")) {
    button.addEventListener("click", () => {
      const held = observation.drivers[Number(button.dataset.driver)].fields[button.dataset.field];
      held.omitted = !held.omitted;
      renderDrivers();
      markPreset(null);
    });
  }
}

// timelineFor reconstructs what happened from the result document alone.
//
// The service deliberately does not report which rule refused: a failure report is
// a canonical artifact and carries no rule identity. The accepted rule list plus
// the plan's declared order identify the boundary that refused without inventing a
// field the engine cannot populate truthfully -- so that is how this is derived.
function timelineFor(result) {
  const accepted = new Set(result.acceptedRules || []);
  const sealed = new Map((result.checkpoints || []).map((c) => [c.checkpointKey, c]));
  const refusedCode = result.failure ? result.failure.code : null;
  const events = [];
  let reachedRefusal = false;

  for (const rule of plan.rules) {
    if (accepted.has(rule)) {
      events.push({ state: "pass", pill: "committed", name: rule, kind: "transition",
                    why: RULE_MEANINGS[rule] });
    } else if (!reachedRefusal && refusedCode) {
      reachedRefusal = true;
      events.push({ state: "refuse", pill: "refused", name: rule, kind: "transition",
                    why: RULE_MEANINGS[rule] });
    } else {
      events.push({ state: "skipped", pill: "not reached", name: rule, kind: "transition",
                    why: "The run ended before this rule was considered." });
    }

    for (const declared of plan.checkpoints.filter((c) => c.after === rule)) {
      const artifact = sealed.get(declared.key);
      if (artifact) {
        events.push({ state: "seal", pill: "sealed", name: declared.key, kind: "checkpoint",
                      why: CHECKPOINT_MEANINGS[declared.key], digest: artifact.checkpointArtifactID });
      } else {
        events.push({ state: "skipped", pill: "not sealed", name: declared.key, kind: "checkpoint",
                      why: "Its boundary was not reached, so there is no verified prefix to seal." });
      }
    }
  }
  return events;
}

const MARKERS = { pass: "✓", refuse: "×", seal: "◈", skipped: "·" };

function renderTimeline(result) {
  el("timeline").innerHTML = timelineFor(result).map((event) => `
    <li>
      <span class="marker ${event.state}">${MARKERS[event.state]}</span>
      <span>
        <span class="event-name">${escapeHTML(event.name)} <span class="kind">${event.kind}</span></span>
        ${event.why ? `<div class="event-why">${escapeHTML(event.why)}</div>` : ""}
        ${event.digest ? `<div class="event-digest">${escapeHTML(event.digest)}</div>` : ""}
      </span>
      <span class="pill ${event.state}">${escapeHTML(event.pill)}</span>
    </li>`).join("");
}

function renderVerdict(result) {
  const sealedCount = (result.checkpoints || []).length;
  const declaredCount = plan.checkpoints.length;

  if (!result.failure) {
    el("verdict").innerHTML = `
      <div class="verdict pass">
        <div class="verdict-head">
          <span class="verdict-title">Committed</span>
          <span class="verdict-code">spineStatus: ${escapeHTML(result.spineStatus)}</span>
        </div>
        <p>Every rule committed and both checkpoints sealed. The team's reconciled
        hours are a figure the engine can account for, back to the observations it
        came from.</p>
      </div>`;
    return;
  }

  const meaning = CODE_MEANINGS[result.failure.code] || {
    title: "The engine refused this observation",
    detail: "It reported a closed code rather than a message, so the refusal is a value a caller can act on.",
  };

  el("verdict").innerHTML = `
    <div class="verdict refuse">
      <div class="verdict-head">
        <span class="verdict-title">Refused &mdash; ${escapeHTML(meaning.title)}</span>
        <span class="verdict-code">${escapeHTML(result.failure.code)}</span>
      </div>
      <p>${escapeHTML(meaning.detail)}</p>
      <p class="kept">${
        sealedCount === 0
          ? "Nothing was sealed: the refusal came at the first boundary, so no prefix was ever established."
          : `${sealedCount} of ${declaredCount} checkpoints stayed sealed. The work that was justified is kept; ` +
            "only the boundary that could not be justified produced nothing."
      }</p>
      <p>The HTTP response was <strong>200</strong>, not an error. A run that refused
      produced a real answer, and the artifacts verified beforehand are not discarded.</p>
    </div>`;
}

function renderReadiness(result) {
  const assessments = result.assessments || [];
  if (assessments.length === 0) {
    el("readiness").innerHTML =
      `<p class="empty">No checkpoint was sealed, so there is nothing to assess. Readiness is
       a question about a sealed prefix, not about a run.</p>`;
    return;
  }

  const profiles = [...new Set(assessments.map((a) => a.profileKey))].sort();
  const byArtifact = new Map();
  for (const assessment of assessments) {
    if (!byArtifact.has(assessment.checkpointArtifactID)) byArtifact.set(assessment.checkpointArtifactID, new Map());
    byArtifact.get(assessment.checkpointArtifactID).set(assessment.profileKey, assessment);
  }
  const nameOf = new Map((result.checkpoints || []).map((c) => [c.checkpointArtifactID, c.checkpointKey]));

  el("readiness").innerHTML = `
    <div class="grid-scroll">
    <table class="readiness">
      <thead>
        <tr>
          <th>sealed checkpoint</th>
          ${profiles.map((profile) => `<th>${escapeHTML(profile)}<br>
            <span style="text-transform:none;font-weight:400">${escapeHTML(PROFILE_MEANINGS[profile] || "")}</span></th>`).join("")}
        </tr>
      </thead>
      <tbody>
        ${[...byArtifact.entries()].map(([artifact, row]) => `
          <tr>
            <th>${escapeHTML(nameOf.get(artifact) || shorten(artifact))}</th>
            ${profiles.map((profile) => {
              const assessment = row.get(profile);
              if (!assessment) return `<td class="empty">not assessed</td>`;
              const ready = assessment.verdict === "ready";
              const missing = assessment.missingRequirements || [];
              return `<td>
                <span class="${ready ? "verdict-ready" : "verdict-needs"}">${escapeHTML(assessment.verdict)}</span>
                ${missing.length ? `<div class="missing">missing:<ul>${
                  missing.map((code) => `<li>${escapeHTML(code)}</li>`).join("")
                }</ul></div>` : ""}
              </td>`;
            }).join("")}
          </tr>`).join("")}
      </tbody>
    </table>
    </div>`;
}

function renderIdentities(accepted, result) {
  const rows = [
    { label: "planID", note: "the program", value: accepted.planID, compare: "planID" },
    { label: "inputID", note: "the observation", value: result.inputID, compare: "inputID" },
    { label: "semanticRunID", note: "program over observation", value: accepted.semanticRunID, compare: "semanticRunID" },
    { label: "executionID", note: "that run on this backend", value: accepted.executionID, compare: "executionID" },
    { label: "final state digest", note: "what the run produced", value: result.finalStateDigest, compare: "finalStateDigest" },
  ];

  el("identities").innerHTML = `<div class="identities">${rows.map((row) => {
    let change = "";
    if (previous && previous[row.compare] !== undefined && row.value !== undefined) {
      const same = previous[row.compare] === row.value;
      change = `<span class="change ${same ? "same" : "moved"}">${same ? "unchanged" : "changed"}</span>`;
    }
    return `
      <div class="identity">
        <span class="identity-label">${escapeHTML(row.label)}<small>${escapeHTML(row.note)}</small></span>
        <span class="identity-value">${escapeHTML(row.value === undefined ? "—" : row.value)}</span>
        ${change}
      </div>`;
  }).join("")}</div>`;

  previous = {
    planID: accepted.planID, inputID: result.inputID,
    semanticRunID: accepted.semanticRunID, executionID: accepted.executionID,
    finalStateDigest: result.finalStateDigest,
  };
}

function renderTrace(executionID) {
  const query = `{ .maiden_lane.semantic.execution_id = "${executionID}" }`;
  const explore = `${settings.grafana}/explore?left=` +
    encodeURIComponent(JSON.stringify({ datasource: "tempo", queries: [{ query, queryType: "traceql" }] }));
  el("trace-link").innerHTML = `
    <div class="trace">
      This run is one trace, rooted at the worker span, whose children are the
      semantic phases. If the local stack is running
      (<code>make observe-up</code>), <a href="${escapeHTML(explore)}" target="_blank"
      rel="noreferrer">open it in Grafana</a> or paste this into Explore &rarr; Tempo &rarr; TraceQL:
      <pre>${escapeHTML(query)}</pre>
    </div>`;
}

function renderRaw() {
  el("raw-tabs").innerHTML = exchange.map((entry, index) =>
    `<button type="button" data-raw="${index}" aria-pressed="${index === rawSelection}">${escapeHTML(entry.label)}</button>`,
  ).join("");
  for (const button of el("raw-tabs").querySelectorAll("button")) {
    button.addEventListener("click", () => {
      rawSelection = Number(button.dataset.raw);
      renderRaw();
    });
  }
  const entry = exchange[rawSelection];
  if (!entry) return;
  const parts = [];
  if (entry.request) parts.push(`── request ──\n${JSON.stringify(entry.request, null, 2)}`);
  parts.push(`── response ──\n${JSON.stringify(entry.response, null, 2)}`);
  el("raw").innerHTML = `<code>${escapeHTML(parts.join("\n\n"))}</code>`;
}

// ── the run ─────────────────────────────────────────────────────────────────

async function compilePlan() {
  const response = await call("POST", "/v1/plans", settings.plan);
  planExchange = { label: "POST /v1/plans", request: settings.plan, response: response.body };
  if (response.status !== 201) {
    throw new Error(`compiling the plan returned ${response.status}: ${JSON.stringify(response.body)}`);
  }
  plan = response.body;
  renderMasthead();
}

async function runObservation() {
  const button = el("run");
  button.disabled = true;
  // Every run starts a fresh record, with the plan compilation kept at the front so
  // the panel shows the whole conversation rather than only its tail.
  exchange = planExchange ? [planExchange] : [];
  rawSelection = 0;
  el("run-status").textContent = "compiling…";

  try {
    if (!plan) {
      await compilePlan();
      exchange = [planExchange];
    }

    const request = executionRequest();
    el("run-status").textContent = "submitting…";
    const submission = await call("POST", "/v1/executions", request);
    record("POST /v1/executions", request, submission.body);
    if (submission.status !== 202) {
      showProblem(submission);
      return;
    }
    const accepted = submission.body;

    el("run-status").textContent = "running…";
    const finished = await awaitExecution(accepted.executionID);
    if (!finished) return;

    const result = finished.result;
    el("outcome-lede").textContent =
      "Derived entirely from the result document below — which rule committed, " +
      "which boundary refused, and what was sealed before it.";
    renderVerdict(result);
    renderTimeline(result);
    renderReadiness(result);
    renderIdentities(accepted, result);
    renderTrace(accepted.executionID);
    renderRaw();
    el("run-status").textContent = result.failure
      ? `refused: ${result.failure.code}`
      : "committed";
  } catch (error) {
    el("verdict").innerHTML = `<div class="error">${escapeHTML(error.message)}</div>`;
    el("run-status").textContent = "";
  } finally {
    button.disabled = false;
  }
}

// awaitExecution polls until a wall-clock deadline rather than for a fixed number
// of attempts. A count is not a timeout: it silently becomes a much shorter wait on
// a fast machine and a much longer one on a slow backend, and the failure it
// produces -- "no worker running" -- points at the wrong thing.
const POLL_DEADLINE_MS = 30000;
const POLL_INTERVAL_MS = 120;

async function awaitExecution(executionID) {
  const path = `/v1/executions/${encodeURIComponent(executionID)}`;
  const deadline = Date.now() + POLL_DEADLINE_MS;
  while (Date.now() < deadline) {
    const response = await call("GET", path);
    if (response.status !== 200) {
      showProblem(response);
      return null;
    }
    const execution = response.body;
    if (execution.executionStatus === "succeeded" || execution.executionStatus === "failed") {
      record(`GET ${path.slice(0, 22)}…`, null, execution);
      if (!execution.result) {
        // Terminal without a result means the execution could not be attempted at
        // all, which is different from a computation that refused.
        el("verdict").innerHTML = `<div class="error">The execution could not be attempted: ${
          escapeHTML(execution.failureReason || "no reason reported")}</div>`;
        renderRaw();
        return null;
      }
      return execution;
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
  }
  throw new Error(
    `the execution was still ${"pending or running"} after ${POLL_DEADLINE_MS / 1000}s. ` +
    "Submission is asynchronous, so this means no worker is consuming the queue.");
}

function showProblem(response) {
  const problem = response.body || {};
  el("verdict").innerHTML = `
    <div class="error">
      <strong>${escapeHTML(problem.title || `HTTP ${response.status}`)}</strong>
      <div>${escapeHTML(problem.detail || "")}</div>
    </div>`;
  el("timeline").innerHTML = "";
  el("run-status").textContent = "";
  renderRaw();
}

// ── boot ────────────────────────────────────────────────────────────────────

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (character) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[character]);
}

function shorten(digest) {
  return typeof digest === "string" && digest.length > 26 ? `${digest.slice(0, 20)}…` : String(digest);
}

async function boot() {
  try {
    const response = await fetch("/demo/settings");
    if (!response.ok) throw new Error(`the demo server returned ${response.status} for its settings`);
    settings = await response.json();
  } catch (error) {
    document.body.innerHTML =
      `<div class="error" style="margin:32px">Could not load the demo settings: ${escapeHTML(error.message)}</div>`;
    return;
  }

  observation = observationFromExample(settings.execution);
  renderMasthead();
  renderPresets();

  // ?preset=<id> selects a starting observation and ?run=1 runs it immediately, so
  // a browser tab can be opened directly on the state someone wants to show rather
  // than clicked into it while an audience waits.
  const parameters = new URLSearchParams(window.location.search);
  const requested = PRESETS.find((preset) => preset.id === parameters.get("preset"));
  if (requested) requested.apply(observation.drivers);

  renderDrivers();
  markPreset(requested ? requested.id : "consistent");
  el("run").addEventListener("click", runObservation);

  // The plan is compiled on load so the page opens already showing the program's
  // identity, and so a broken connection to the service is reported here rather
  // than after someone presses the button in front of an audience.
  try {
    await compilePlan();
    el("run-status").textContent = "plan compiled — ready";
  } catch (error) {
    el("verdict").innerHTML = `<div class="error">${escapeHTML(error.message)}</div>`;
    return;
  }

  if (parameters.get("run") === "1") await runObservation();
}

boot();
