package locality

import "testing"

const GiB = uint64(1024 * 1024 * 1024)
const MiB = uint64(1024 * 1024)

func TestCachedSlowerWorkerBeatsFasterRemoteWorker(t *testing.T) {
	task := Task{Inputs: []Artifact{{Digest: "model", SizeBytes: 8 * GiB}}}
	workers := []Worker{
		{
			ID:                     "local-slower",
			HardConstraintsOK:      true,
			ComputeMillis:          60_000,
			DownloadBytesPerSecond: 100 * MiB,
			UploadBytesPerSecond:   100 * MiB,
			CachedDigests:          map[string]bool{"model": true},
		},
		{
			ID:                     "remote-faster",
			HardConstraintsOK:      true,
			ComputeMillis:          20_000,
			DownloadBytesPerSecond: 100 * MiB,
			UploadBytesPerSecond:   100 * MiB,
			CachedDigests:          map[string]bool{},
		},
	}
	ranked := Rank(task, workers)
	if ranked[0].WorkerID != "local-slower" {
		t.Fatalf("expected locality to win, got %+v", ranked)
	}
	if ranked[1].TransferInputBytes != 8*GiB {
		t.Fatalf("remote transfer=%d", ranked[1].TransferInputBytes)
	}
}

func TestFasterRemoteWorkerWinsWhenArtifactIsSmall(t *testing.T) {
	task := Task{Inputs: []Artifact{{Digest: "small", SizeBytes: 10 * MiB}}}
	workers := []Worker{
		{
			ID:                     "local-slower",
			HardConstraintsOK:      true,
			ComputeMillis:          60_000,
			DownloadBytesPerSecond: 100 * MiB,
			UploadBytesPerSecond:   100 * MiB,
			CachedDigests:          map[string]bool{"small": true},
		},
		{
			ID:                     "remote-faster",
			HardConstraintsOK:      true,
			ComputeMillis:          20_000,
			DownloadBytesPerSecond: 100 * MiB,
			UploadBytesPerSecond:   100 * MiB,
			CachedDigests:          map[string]bool{},
		},
	}
	ranked := Rank(task, workers)
	if ranked[0].WorkerID != "remote-faster" {
		t.Fatalf("expected compute speed to win, got %+v", ranked)
	}
}

func TestSecurityConstraintAlwaysBeatsLocality(t *testing.T) {
	task := Task{Inputs: []Artifact{{Digest: "secret-model", SizeBytes: 8 * GiB}}}
	workers := []Worker{
		{
			ID:                "cached-but-not-authorized",
			HardConstraintsOK: false,
			RejectReason:      "data class is not allowed on this worker",
			ComputeMillis:     1_000,
			CachedDigests:     map[string]bool{"secret-model": true},
		},
		{
			ID:                     "authorized",
			HardConstraintsOK:      true,
			ComputeMillis:          30_000,
			DownloadBytesPerSecond: 200 * MiB,
			UploadBytesPerSecond:   200 * MiB,
			CachedDigests:          map[string]bool{},
		},
	}
	ranked := Rank(task, workers)
	if ranked[0].WorkerID != "authorized" || !ranked[0].Eligible {
		t.Fatalf("hard constraint must dominate locality: %+v", ranked)
	}
}

func TestTransferBudgetCanRejectRemotePlacement(t *testing.T) {
	task := Task{
		Inputs:           []Artifact{{Digest: "model", SizeBytes: 8 * GiB}},
		MaxTransferBytes: 1 * GiB,
	}
	e := EstimateTask(task, Worker{
		ID:                     "remote",
		HardConstraintsOK:      true,
		ComputeMillis:          1_000,
		DownloadBytesPerSecond: 1 * GiB,
		UploadBytesPerSecond:   1 * GiB,
		CachedDigests:          map[string]bool{},
	})
	if e.Eligible {
		t.Fatalf("expected network budget rejection: %+v", e)
	}
}

func TestExpectedOutputAlsoCountsAsNetworkCost(t *testing.T) {
	task := Task{ExpectedOutputBytes: 500 * MiB}
	e := EstimateTask(task, Worker{
		ID:                     "worker",
		HardConstraintsOK:      true,
		ComputeMillis:          5_000,
		DownloadBytesPerSecond: 100 * MiB,
		UploadBytesPerSecond:   50 * MiB,
		CachedDigests:          map[string]bool{},
	})
	if !e.Eligible {
		t.Fatalf("unexpected rejection: %+v", e)
	}
	if e.TransferMillis != 10_000 {
		t.Fatalf("expected 10s output upload, got %dms", e.TransferMillis)
	}
	if e.EstimatedMillis != 15_000 {
		t.Fatalf("expected 15s total, got %dms", e.EstimatedMillis)
	}
}

func TestNoBandwidthRejectsRequiredTransfer(t *testing.T) {
	task := Task{Inputs: []Artifact{{Digest: "x", SizeBytes: 1}}}
	e := EstimateTask(task, Worker{ID: "offline", HardConstraintsOK: true})
	if e.Eligible {
		t.Fatalf("expected rejection: %+v", e)
	}
}
