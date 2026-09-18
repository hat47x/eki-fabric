export const Profile = Object.freeze({
  VISIBLE_ONLY: "visible-only",
  BACKGROUND_OPT_IN: "background-opt-in",
});

export class BrowserParticipation {
  constructor(profile = Profile.VISIBLE_ONLY) {
    this.profile = profile;
    this.intent = false;
    this.pageState = "visible";
    this.localRunning = false;
    this.epoch = 0;
  }

  shouldRunHere() {
    if (!this.intent) return false;
    if (this.pageState === "frozen" || this.pageState === "pagehide") return false;
    if (this.profile === Profile.VISIBLE_ONLY && this.pageState !== "visible") return false;
    return true;
  }

  explicitStart() {
    this.intent = true;
    this.epoch++;
    return { action: "acquire-new-lease", epoch: this.epoch };
  }

  explicitStop() {
    this.intent = false;
    this.localRunning = false;
    this.epoch++;
    return { action: "release-and-stop", epoch: this.epoch };
  }

  leaseGranted(epoch) {
    if (epoch !== this.epoch || !this.shouldRunHere()) {
      return { action: "reject-stale-or-ineligible-lease", epoch: this.epoch };
    }
    this.localRunning = true;
    return { action: "run", epoch: this.epoch };
  }

  hidden() {
    this.pageState = "hidden";
    if (this.profile === Profile.VISIBLE_ONLY) {
      this.localRunning = false;
      this.epoch++;
      return { action: "release-and-stop", epoch: this.epoch };
    }
    return { action: "keep-opportunistic", epoch: this.epoch };
  }

  visible() {
    this.pageState = "visible";
    if (!this.intent) return { action: "stay-stopped", epoch: this.epoch };
    if (this.localRunning) return { action: "keep-running", epoch: this.epoch };
    this.epoch++;
    return { action: "acquire-new-lease", epoch: this.epoch };
  }

  freeze() {
    this.pageState = "frozen";
    this.localRunning = false;
    this.epoch++;
    return { action: "best-effort-release-and-stop", epoch: this.epoch };
  }

  pagehide() {
    this.pageState = "pagehide";
    this.localRunning = false;
    this.epoch++;
    return { action: "best-effort-release-and-stop", epoch: this.epoch };
  }

  resume(hidden = false) {
    this.pageState = hidden ? "hidden" : "visible";
    if (!this.intent) return { action: "stay-stopped", epoch: this.epoch };
    if (!this.shouldRunHere()) return { action: "stay-suspended", epoch: this.epoch };
    this.epoch++;
    return { action: "acquire-new-lease", epoch: this.epoch };
  }
}

export class LeaseBook {
  constructor(ttlMillis) {
    if (!Number.isFinite(ttlMillis) || ttlMillis <= 0) {
      throw new Error("ttlMillis must be positive and finite");
    }
    this.ttlMillis = ttlMillis;
    this.sequence = 0;
    this.current = new Map();
  }

  issue(workerID, nowMillis) {
    const leaseID = `${workerID}:${++this.sequence}`;
    const lease = {
      workerID,
      leaseID,
      expiresAt: nowMillis + this.ttlMillis,
    };
    this.current.set(workerID, lease);
    return { ...lease };
  }

  heartbeat(workerID, leaseID, nowMillis) {
    const current = this.current.get(workerID);
    if (!current || current.leaseID !== leaseID) return false;
    if (nowMillis > current.expiresAt) {
      this.current.delete(workerID);
      return false;
    }
    current.expiresAt = nowMillis + this.ttlMillis;
    return true;
  }

  active(workerID, nowMillis) {
    const current = this.current.get(workerID);
    if (!current) return false;
    if (nowMillis > current.expiresAt) {
      this.current.delete(workerID);
      return false;
    }
    return true;
  }

  release(workerID, leaseID) {
    const current = this.current.get(workerID);
    if (!current || current.leaseID !== leaseID) return false;
    this.current.delete(workerID);
    return true;
  }
}
