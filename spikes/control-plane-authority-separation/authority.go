package authority

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type DataClass int

const (
	DataPublic DataClass = iota
	DataInternal
	DataConfidential
)

type ArtifactRef struct {
	Digest string `json:"digest"`
}

type TaskIR struct {
	TaskID         string        `json:"task_id"`
	ExecutorDigest string        `json:"executor_digest"`
	DataClass      DataClass     `json:"data_class"`
	Inputs         []ArtifactRef `json:"inputs"`
}

type WorkerProfile struct {
	WorkerID          string
	MaxDataClass      DataClass
	ApprovedExecutors map[string]bool
}

type SchedulerProposal struct {
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
}

type AdmissionApproval struct {
	Task TaskIR `json:"task"`
}

type PlacementApproval struct {
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
}

type ArtifactGrant struct {
	TaskID   string    `json:"task_id"`
	WorkerID string    `json:"worker_id"`
	Digest   string    `json:"digest"`
	Expires  time.Time `json:"expires"`
}

type WorkOrder struct {
	TaskID         string          `json:"task_id"`
	WorkerID       string          `json:"worker_id"`
	ExecutorDigest string          `json:"executor_digest"`
	DataClass      DataClass       `json:"data_class"`
	InputGrants    []ArtifactGrant `json:"input_grants"`
}

type Signed[T any] struct {
	Payload   T      `json:"payload"`
	Signature []byte `json:"signature"`
}

type KeyPair struct {
	Public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func NewKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: pub, private: priv}, nil
}

func canonical(v any) ([]byte, error) {
	return json.Marshal(v)
}

func sign[T any](key KeyPair, payload T) (Signed[T], error) {
	b, err := canonical(payload)
	if err != nil {
		return Signed[T]{}, err
	}
	return Signed[T]{Payload: payload, Signature: ed25519.Sign(key.private, b)}, nil
}

func Verify[T any](public ed25519.PublicKey, signed Signed[T]) bool {
	b, err := canonical(signed.Payload)
	if err != nil {
		return false
	}
	return ed25519.Verify(public, b, signed.Signature)
}

type AdmissionAuthority struct{ key KeyPair }

type PlacementGuard struct {
	key          KeyPair
	admissionPub ed25519.PublicKey
}

type ArtifactAuthority struct {
	key          KeyPair
	admissionPub ed25519.PublicKey
	placementPub ed25519.PublicKey
}

type WorkOrderAuthority struct {
	key          KeyPair
	admissionPub ed25519.PublicKey
	placementPub ed25519.PublicKey
	artifactPub  ed25519.PublicKey
}

type PublicAuthorities struct {
	Admission ed25519.PublicKey
	Placement ed25519.PublicKey
	Artifact  ed25519.PublicKey
	WorkOrder ed25519.PublicKey
}

type ControlPlane struct {
	Admission AdmissionAuthority
	Placement PlacementGuard
	Artifact  ArtifactAuthority
	WorkOrder WorkOrderAuthority
	Public    PublicAuthorities
}

func NewControlPlane() (ControlPlane, error) {
	admission, err := NewKeyPair()
	if err != nil {
		return ControlPlane{}, err
	}
	placement, err := NewKeyPair()
	if err != nil {
		return ControlPlane{}, err
	}
	artifact, err := NewKeyPair()
	if err != nil {
		return ControlPlane{}, err
	}
	workorder, err := NewKeyPair()
	if err != nil {
		return ControlPlane{}, err
	}

	return ControlPlane{
		Admission: AdmissionAuthority{key: admission},
		Placement: PlacementGuard{key: placement, admissionPub: admission.Public},
		Artifact: ArtifactAuthority{
			key:          artifact,
			admissionPub: admission.Public,
			placementPub: placement.Public,
		},
		WorkOrder: WorkOrderAuthority{
			key:          workorder,
			admissionPub: admission.Public,
			placementPub: placement.Public,
			artifactPub:  artifact.Public,
		},
		Public: PublicAuthorities{
			Admission: admission.Public,
			Placement: placement.Public,
			Artifact:  artifact.Public,
			WorkOrder: workorder.Public,
		},
	}, nil
}

func validateTask(task TaskIR) error {
	if task.TaskID == "" || task.ExecutorDigest == "" {
		return errors.New("task ID and executor digest are required")
	}
	seen := map[string]struct{}{}
	for _, in := range task.Inputs {
		if in.Digest == "" {
			return errors.New("input digest is required")
		}
		if _, ok := seen[in.Digest]; ok {
			return fmt.Errorf("duplicate input digest %q", in.Digest)
		}
		seen[in.Digest] = struct{}{}
	}
	return nil
}

func (a AdmissionAuthority) Admit(task TaskIR) (Signed[AdmissionApproval], error) {
	if err := validateTask(task); err != nil {
		return Signed[AdmissionApproval]{}, err
	}
	return sign(a.key, AdmissionApproval{Task: task})
}

// Scheduler deliberately has no signing key. A compromised scheduler can propose,
// but cannot create Admission, Placement, Artifact, or WorkOrder authority.
func Propose(taskID, workerID string) (SchedulerProposal, error) {
	if taskID == "" || workerID == "" {
		return SchedulerProposal{}, errors.New("task and worker are required")
	}
	return SchedulerProposal{TaskID: taskID, WorkerID: workerID}, nil
}

func (p PlacementGuard) Approve(admission Signed[AdmissionApproval], proposal SchedulerProposal, worker WorkerProfile) (Signed[PlacementApproval], error) {
	if !Verify(p.admissionPub, admission) {
		return Signed[PlacementApproval]{}, errors.New("invalid admission authority")
	}
	task := admission.Payload.Task
	if proposal.TaskID != task.TaskID {
		return Signed[PlacementApproval]{}, errors.New("proposal task does not match admitted task")
	}
	if proposal.WorkerID != worker.WorkerID {
		return Signed[PlacementApproval]{}, errors.New("proposal worker does not match evaluated worker")
	}
	if worker.MaxDataClass < task.DataClass {
		return Signed[PlacementApproval]{}, errors.New("worker does not satisfy data class")
	}
	if !worker.ApprovedExecutors[task.ExecutorDigest] {
		return Signed[PlacementApproval]{}, errors.New("executor is not approved on worker")
	}
	return sign(p.key, PlacementApproval{TaskID: task.TaskID, WorkerID: worker.WorkerID})
}

func (a ArtifactAuthority) Grant(admission Signed[AdmissionApproval], placement Signed[PlacementApproval], digest string, expires time.Time) (Signed[ArtifactGrant], error) {
	if !Verify(a.admissionPub, admission) {
		return Signed[ArtifactGrant]{}, errors.New("invalid admission authority")
	}
	if !Verify(a.placementPub, placement) {
		return Signed[ArtifactGrant]{}, errors.New("invalid placement authority")
	}

	task := admission.Payload.Task
	pp := placement.Payload
	if pp.TaskID != task.TaskID {
		return Signed[ArtifactGrant]{}, errors.New("placement task mismatch")
	}

	found := false
	for _, in := range task.Inputs {
		if in.Digest == digest {
			found = true
			break
		}
	}
	if !found {
		return Signed[ArtifactGrant]{}, errors.New("artifact is not an admitted task input")
	}
	if expires.IsZero() {
		return Signed[ArtifactGrant]{}, errors.New("artifact grant expiry is required")
	}

	return sign(a.key, ArtifactGrant{
		TaskID: task.TaskID,
		WorkerID: pp.WorkerID,
		Digest: digest,
		Expires: expires,
	})
}

func (w WorkOrderAuthority) Compile(admission Signed[AdmissionApproval], placement Signed[PlacementApproval], grants []Signed[ArtifactGrant], now time.Time) (Signed[WorkOrder], error) {
	if !Verify(w.admissionPub, admission) {
		return Signed[WorkOrder]{}, errors.New("invalid admission authority")
	}
	if !Verify(w.placementPub, placement) {
		return Signed[WorkOrder]{}, errors.New("invalid placement authority")
	}

	task := admission.Payload.Task
	pp := placement.Payload
	if pp.TaskID != task.TaskID {
		return Signed[WorkOrder]{}, errors.New("placement task mismatch")
	}

	byDigest := map[string]ArtifactGrant{}
	for _, signedGrant := range grants {
		if !Verify(w.artifactPub, signedGrant) {
			return Signed[WorkOrder]{}, errors.New("invalid artifact authority")
		}
		g := signedGrant.Payload
		if g.TaskID != task.TaskID || g.WorkerID != pp.WorkerID {
			return Signed[WorkOrder]{}, errors.New("artifact grant scope mismatch")
		}
		if !g.Expires.After(now) {
			return Signed[WorkOrder]{}, errors.New("artifact grant is expired")
		}
		if _, exists := byDigest[g.Digest]; exists {
			return Signed[WorkOrder]{}, errors.New("duplicate artifact grant")
		}
		byDigest[g.Digest] = g
	}

	inputDigests := make([]string, 0, len(task.Inputs))
	for _, in := range task.Inputs {
		if _, ok := byDigest[in.Digest]; !ok {
			return Signed[WorkOrder]{}, fmt.Errorf("missing artifact grant for %q", in.Digest)
		}
		inputDigests = append(inputDigests, in.Digest)
	}
	if len(byDigest) != len(inputDigests) {
		return Signed[WorkOrder]{}, errors.New("unexpected artifact grant")
	}

	sort.Strings(inputDigests)
	ordered := make([]ArtifactGrant, 0, len(inputDigests))
	for _, digest := range inputDigests {
		ordered = append(ordered, byDigest[digest])
	}

	order := WorkOrder{
		TaskID:         task.TaskID,
		WorkerID:       pp.WorkerID,
		ExecutorDigest: task.ExecutorDigest,
		DataClass:      task.DataClass,
		InputGrants:    ordered,
	}
	return sign(w.key, order)
}

func VerifyForWorker(public ed25519.PublicKey, signed Signed[WorkOrder], workerID string, now time.Time) error {
	if !Verify(public, signed) {
		return errors.New("invalid WorkOrder authority")
	}
	if signed.Payload.WorkerID != workerID {
		return errors.New("WorkOrder is for another worker")
	}
	for _, grant := range signed.Payload.InputGrants {
		if grant.WorkerID != workerID || grant.TaskID != signed.Payload.TaskID {
			return errors.New("embedded grant scope mismatch")
		}
		if !grant.Expires.After(now) {
			return errors.New("embedded artifact grant expired")
		}
	}
	return nil
}
