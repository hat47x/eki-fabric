import test from "node:test";
import assert from "node:assert/strict";
import { BrowserParticipation, LeaseBook, Profile } from "./lifecycle.mjs";

test("visible-only profile stops locally when hidden", () => {
  const p = new BrowserParticipation(Profile.VISIBLE_ONLY);
  const start = p.explicitStart();
  assert.equal(p.leaseGranted(start.epoch).action, "run");
  assert.equal(p.localRunning, true);

  const hidden = p.hidden();
  assert.equal(hidden.action, "release-and-stop");
  assert.equal(p.localRunning, false);
});

test("visible return requires a fresh lease instead of reusing the old epoch", () => {
  const p = new BrowserParticipation(Profile.VISIBLE_ONLY);
  const start = p.explicitStart();
  assert.equal(p.leaseGranted(start.epoch).action, "run");
  p.hidden();
  const visible = p.visible();
  assert.equal(visible.action, "acquire-new-lease");
  assert.equal(p.leaseGranted(start.epoch).action, "reject-stale-or-ineligible-lease");
  assert.equal(p.leaseGranted(visible.epoch).action, "run");
});

test("explicit stop survives later visibility changes", () => {
  const p = new BrowserParticipation(Profile.VISIBLE_ONLY);
  const start = p.explicitStart();
  p.leaseGranted(start.epoch);
  p.explicitStop();
  p.hidden();
  const visible = p.visible();
  assert.equal(visible.action, "stay-stopped");
  assert.equal(p.intent, false);
  assert.equal(p.localRunning, false);
});

test("freeze invalidates local execution and requires a fresh lease on resume", () => {
  const p = new BrowserParticipation(Profile.BACKGROUND_OPT_IN);
  const start = p.explicitStart();
  p.leaseGranted(start.epoch);
  const frozen = p.freeze();
  assert.equal(frozen.action, "best-effort-release-and-stop");
  assert.equal(p.localRunning, false);
  const resumed = p.resume(false);
  assert.equal(resumed.action, "acquire-new-lease");
  assert.notEqual(resumed.epoch, start.epoch);
});

test("missing lifecycle notification is absorbed by lease expiry", () => {
  const leases = new LeaseBook(5_000);
  const lease = leases.issue("browser-1", 0);
  assert.equal(leases.heartbeat("browser-1", lease.leaseID, 2_000), true);
  assert.equal(leases.active("browser-1", 6_999), true);
  assert.equal(leases.active("browser-1", 7_001), false);
});

test("stale heartbeat cannot revive a superseded lease", () => {
  const leases = new LeaseBook(5_000);
  const first = leases.issue("browser-1", 0);
  const second = leases.issue("browser-1", 1_000);
  assert.equal(leases.heartbeat("browser-1", first.leaseID, 1_500), false);
  assert.equal(leases.heartbeat("browser-1", second.leaseID, 1_500), true);
});

test("background opt-in may continue while hidden but remains lease-dependent", () => {
  const p = new BrowserParticipation(Profile.BACKGROUND_OPT_IN);
  const start = p.explicitStart();
  p.leaseGranted(start.epoch);
  const hidden = p.hidden();
  assert.equal(hidden.action, "keep-opportunistic");
  assert.equal(p.localRunning, true);
});
