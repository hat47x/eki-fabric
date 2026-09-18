import { BrowserParticipation, Profile } from "./lifecycle.mjs";

const state = new BrowserParticipation(Profile.VISIBLE_ONLY);
let worker = null;
let lastHeartbeatAt = null;

const log = document.querySelector("#log");
const status = document.querySelector("#status");

function write(message) {
  const stamp = new Date().toISOString();
  log.textContent += `${stamp} ${message}\n`;
  log.scrollTop = log.scrollHeight;
}

function render() {
  status.textContent = JSON.stringify({
    intent: state.intent,
    pageState: state.pageState,
    localRunning: state.localRunning,
    epoch: state.epoch,
    visibility: document.visibilityState,
    wasDiscarded: document.wasDiscarded === true,
  }, null, 2);
}

function stopWorker(reason) {
  if (!worker) return;
  worker.terminate();
  worker = null;
  lastHeartbeatAt = null;
  write(`worker terminated: ${reason}`);
}

function grantLocalLease(epoch) {
  const decision = state.leaseGranted(epoch);
  write(`lease decision: ${decision.action}, epoch=${decision.epoch}`);
  if (decision.action !== "run") return;
  stopWorker("replace before new lease");
  worker = new Worker("./worker.js", { type: "classic" });
  worker.onmessage = (event) => {
    if (event.data?.type !== "heartbeat") return;
    const now = performance.now();
    if (lastHeartbeatAt !== null) {
      write(`heartbeat gap=${Math.round(now - lastHeartbeatAt)}ms seq=${event.data.sequence}`);
    }
    lastHeartbeatAt = now;
  };
  worker.postMessage({ type: "start" });
}

function applyDecision(decision, reason) {
  write(`${reason}: ${decision.action}, epoch=${decision.epoch}`);
  if (decision.action.includes("stop")) stopWorker(reason);
  if (decision.action === "acquire-new-lease") grantLocalLease(decision.epoch);
  render();
}

document.querySelector("#start").addEventListener("click", () => {
  applyDecision(state.explicitStart(), "explicit start");
});

document.querySelector("#stop").addEventListener("click", () => {
  applyDecision(state.explicitStop(), "explicit stop");
});

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") {
    applyDecision(state.hidden(), "visibility hidden");
  } else {
    applyDecision(state.visible(), "visibility visible");
  }
});

document.addEventListener("freeze", () => {
  applyDecision(state.freeze(), "freeze");
});

document.addEventListener("resume", () => {
  applyDecision(state.resume(document.visibilityState === "hidden"), "resume");
});

window.addEventListener("pagehide", () => {
  applyDecision(state.pagehide(), "pagehide");
});

window.addEventListener("pageshow", () => {
  if (state.pageState === "pagehide" && state.intent) {
    applyDecision(state.resume(document.visibilityState === "hidden"), "pageshow");
  }
});

write(`loaded; document.wasDiscarded=${document.wasDiscarded === true}`);
render();
