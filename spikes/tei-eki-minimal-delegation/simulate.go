package teideleg

import (
	"fmt"
	"time"
)

// SimulatedEvidence is an explicitly simulated record of EKI's internal
// lifecycle: Leases, Fences and Attempts. EKI's public Client API does not
// expose these fields, so this type can only be filled by a mock. It exists to
// fix the guarantees TEI relies on and to test them without pretending that
// the public contract offers them.
type SimulatedEvidence struct {
	// LatestAttempt and LatestFence are the Attempt EKI currently considers
	// current.
	LatestAttempt  int       `json:"latest_attempt"`
	LatestFence    uint64    `json:"latest_fence"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	Deadline       time.Time `json:"deadline"`

	// ResultAttempt and ResultFence are the Attempt that reported the result.
	ResultAttempt int       `json:"result_attempt"`
	ResultFence   uint64    `json:"result_fence"`
	ResultAt      time.Time `json:"result_at"`

	ResultDigest   string `json:"result_digest"`
	ExpectedDigest string `json:"expected_digest"`

	ArtifactsTotal     int `json:"artifacts_total"`
	ArtifactsAvailable int `json:"artifacts_available"`
	ArtifactMismatch   int `json:"artifact_digest_mismatches"`

	// DuplicateResults counts further reports of the same Attempt/fence.
	DuplicateResults int `json:"duplicate_results"`
}

// JudgeSimulated decides what a simulated lifecycle means for TEI. The rules
// mirror what TEI must never accept:
//
//   - a result from an Attempt older than the current one,
//   - a result that arrived after its Lease expired,
//   - a result whose body is only partly available,
//   - a result digest that differs from the expectation.
//
// Lease expiry, Fence order and Attempt currency are EKI's decisions. This
// function tests what TEI should do if EKI's public answer ever represents
// them; it does not re-implement them.
func JudgeSimulated(ev SimulatedEvidence) Receipt {
	r := Receipt{
		Schema:  ReceiptSchema,
		Attempt: AttemptSummary{ObservedCount: ev.LatestAttempt, Retried: ev.LatestAttempt > 1},
		NotChecked: []string{
			"Lease・Fence・Attempt は EKI の公開契約に現れない。この判定は模擬入力に対する TEI 側の規律を確かめるもので、EKI の実挙動の観測ではない",
			"EKI が古い Attempt の結果を実際に拒否したか（#173・#242 の実装と負例試験に依存する）",
		},
	}

	if ev.ResultAt.IsZero() {
		r.Outcome = OutcomeUndetermined
		r.Reason = "結果の報告がない"
		r.Basis = append(r.Basis, "no result was reported")
		return r
	}
	if ev.ResultAttempt != ev.LatestAttempt || ev.ResultFence < ev.LatestFence {
		r.Outcome = OutcomeRejected
		r.Reason = "古い Attempt の結果であり、現在の Fence と一致しない"
		r.Basis = append(r.Basis,
			fmt.Sprintf("result attempt=%d fence=%d, current attempt=%d fence=%d",
				ev.ResultAttempt, ev.ResultFence, ev.LatestAttempt, ev.LatestFence))
		return r
	}
	r.Basis = append(r.Basis, "the result belongs to the current Attempt and Fence")

	if !ev.LeaseExpiresAt.IsZero() && ev.ResultAt.After(ev.LeaseExpiresAt) {
		r.Outcome = OutcomeRejected
		r.Reason = "Lease の期限後に届いた結果であり、実行資格を失った Attempt のもの"
		r.Basis = append(r.Basis, fmt.Sprintf("result at %s, lease expired at %s",
			ev.ResultAt.UTC().Format(time.RFC3339), ev.LeaseExpiresAt.UTC().Format(time.RFC3339)))
		return r
	}
	r.Basis = append(r.Basis, "the Lease was still live when the result arrived")

	if ev.ArtifactsTotal > 0 && ev.ArtifactsAvailable == 0 {
		r.Outcome = OutcomeUndetermined
		r.Reason = "結果の Artifact が部分的にしか受領できていない"
		r.Basis = append(r.Basis, fmt.Sprintf("artifacts total=%d available=%d",
			ev.ArtifactsTotal, ev.ArtifactsAvailable))
		return r
	}
	if ev.ArtifactMismatch > 0 {
		r.Outcome = OutcomeRejected
		r.Reason = "取得した Artifact の digest が名前に一致しない"
		r.Basis = append(r.Basis, fmt.Sprintf("%d artifacts failed re-hashing", ev.ArtifactMismatch))
		return r
	}

	if ev.ExpectedDigest == "" || ev.ResultDigest != ev.ExpectedDigest {
		r.Outcome = OutcomeRejected
		r.Reason = "結果の digest が期待値と一致しない（キャッシュ・toolchain・実行環境の差であり得る）"
		r.Basis = append(r.Basis, fmt.Sprintf("result=%q expected=%q", ev.ResultDigest, ev.ExpectedDigest))
		return r
	}
	r.Basis = append(r.Basis, "the result digest equals the expectation")

	if ev.DuplicateResults > 0 {
		r.Basis = append(r.Basis, fmt.Sprintf("%d duplicate reports of the same Attempt were suppressed", ev.DuplicateResults))
	}
	if !ev.Deadline.IsZero() && ev.ResultAt.After(ev.Deadline) {
		r.Outcome = OutcomeUndetermined
		r.Reason = "期限を過ぎて届いた結果であり、採用の可否は呼び出し側の判断に残る"
		r.Basis = append(r.Basis, "the result arrived after TEI's wait limit")
		return r
	}

	r.Outcome = OutcomeAccepted
	r.Reason = "現在の Attempt の結果が期待値と一致した"
	return r
}
