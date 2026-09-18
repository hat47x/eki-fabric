let running = false;
let timer = null;
let sequence = 0;

function heartbeat() {
  if (!running) return;
  postMessage({ type: "heartbeat", sequence: ++sequence, now: performance.now() });
  timer = setTimeout(heartbeat, 500);
}

onmessage = (event) => {
  if (event.data?.type === "start") {
    running = true;
    heartbeat();
  } else if (event.data?.type === "stop") {
    running = false;
    if (timer !== null) clearTimeout(timer);
    timer = null;
    postMessage({ type: "stopped" });
  }
};
