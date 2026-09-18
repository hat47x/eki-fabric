package locality

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type Artifact struct {
	Digest    string
	SizeBytes uint64
}

type Task struct {
	Inputs              []Artifact
	ExpectedOutputBytes uint64
	MaxTransferBytes    uint64
}

type Worker struct {
	ID                     string
	HardConstraintsOK      bool
	RejectReason           string
	ComputeMillis          uint64
	DownloadBytesPerSecond uint64
	UploadBytesPerSecond   uint64
	CachedDigests          map[string]bool
}

type Estimate struct {
	WorkerID            string
	Eligible            bool
	RejectReason        string
	TotalInputBytes     uint64
	LocalInputBytes     uint64
	TransferInputBytes  uint64
	TransferOutputBytes uint64
	TransferBytes       uint64
	TransferMillis      uint64
	ComputeMillis       uint64
	EstimatedMillis     uint64
}

func ceilMillis(bytes, bytesPerSecond uint64) (uint64, error) {
	if bytes == 0 {
		return 0, nil
	}
	if bytesPerSecond == 0 {
		return 0, errors.New("non-zero transfer requires non-zero bandwidth")
	}
	seconds := float64(bytes) / float64(bytesPerSecond)
	return uint64(math.Ceil(seconds * 1000)), nil
}

func EstimateTask(task Task, worker Worker) Estimate {
	e := Estimate{WorkerID: worker.ID, ComputeMillis: worker.ComputeMillis}
	if !worker.HardConstraintsOK {
		e.RejectReason = worker.RejectReason
		if e.RejectReason == "" {
			e.RejectReason = "hard placement constraints are not satisfied"
		}
		return e
	}

	var input uint64
	var local uint64
	for _, a := range task.Inputs {
		input += a.SizeBytes
		if worker.CachedDigests[a.Digest] {
			local += a.SizeBytes
		}
	}
	e.TotalInputBytes = input
	e.LocalInputBytes = local
	e.TransferInputBytes = input - local
	e.TransferOutputBytes = task.ExpectedOutputBytes
	e.TransferBytes = e.TransferInputBytes + e.TransferOutputBytes

	if task.MaxTransferBytes > 0 && e.TransferBytes > task.MaxTransferBytes {
		e.RejectReason = fmt.Sprintf("estimated transfer %d exceeds task budget %d", e.TransferBytes, task.MaxTransferBytes)
		return e
	}

	downMillis, err := ceilMillis(e.TransferInputBytes, worker.DownloadBytesPerSecond)
	if err != nil {
		e.RejectReason = err.Error()
		return e
	}
	upMillis, err := ceilMillis(e.TransferOutputBytes, worker.UploadBytesPerSecond)
	if err != nil {
		e.RejectReason = err.Error()
		return e
	}
	e.TransferMillis = downMillis + upMillis
	e.EstimatedMillis = e.ComputeMillis + e.TransferMillis
	e.Eligible = true
	return e
}

func Rank(task Task, workers []Worker) []Estimate {
	out := make([]Estimate, 0, len(workers))
	for _, w := range workers {
		out = append(out, EstimateTask(task, w))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Eligible != out[j].Eligible {
			return out[i].Eligible
		}
		if out[i].Eligible && out[i].EstimatedMillis != out[j].EstimatedMillis {
			return out[i].EstimatedMillis < out[j].EstimatedMillis
		}
		if out[i].TransferBytes != out[j].TransferBytes {
			return out[i].TransferBytes < out[j].TransferBytes
		}
		return out[i].WorkerID < out[j].WorkerID
	})
	return out
}
