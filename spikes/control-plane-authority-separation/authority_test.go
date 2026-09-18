package authority

import (
	"testing"
	"time"
)

func fixture(t *testing.T) (ControlPlane, TaskIR, WorkerProfile, time.Time) {
	t.Helper()

	cp, err := NewControlPlane()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	task := TaskIR{
		TaskID:         "task-1",
		ExecutorDigest: "sha256:executor",
		DataClass:      DataConfidential,
		Inputs:         []ArtifactRef{{Digest: "sha256:input-a"}},
	}
	worker := WorkerProfile{
		WorkerID:     "worker-secure",
		MaxDataClass: DataConfidential,
		ApprovedExecutors: map[string]bool{
			"sha256:executor": true,
		},
	}
	return cp, task, worker, now
}

func TestHappyPathRequiresIndependentApprovals(t *testing.T) {
	cp, task, worker, now := fixture(t)

	admitted, err := cp.Admission.Admit(task)
	if err != nil {
		t.Fatal(err)
	}
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, err := cp.Placement.Approve(admitted, proposal, worker)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := cp.Artifact.Grant(admitted, placement, "sha256:input-a", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	order, err := cp.WorkOrder.Compile(admitted, placement, []Signed[ArtifactGrant]{grant}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyForWorker(cp.Public.WorkOrder, order, worker.WorkerID, now); err != nil {
		t.Fatal(err)
	}
}

func TestCompromisedSchedulerCannotPlaceConfidentialTaskOnWeakWorker(t *testing.T) {
	cp, task, _, _ := fixture(t)
	admitted, _ := cp.Admission.Admit(task)

	weak := WorkerProfile{
		WorkerID:          "worker-public-only",
		MaxDataClass:      DataPublic,
		ApprovedExecutors: map[string]bool{"sha256:executor": true},
	}
	proposal, _ := Propose(task.TaskID, weak.WorkerID)

	if _, err := cp.Placement.Approve(admitted, proposal, weak); err == nil {
		t.Fatal("expected data-class enforcement to reject scheduler proposal")
	}
}

func TestCompromisedSchedulerCannotSelectUnapprovedExecutorHost(t *testing.T) {
	cp, task, _, _ := fixture(t)
	admitted, _ := cp.Admission.Admit(task)

	worker := WorkerProfile{
		WorkerID:          "worker-no-executor",
		MaxDataClass:      DataConfidential,
		ApprovedExecutors: map[string]bool{},
	}
	proposal, _ := Propose(task.TaskID, worker.WorkerID)

	if _, err := cp.Placement.Approve(admitted, proposal, worker); err == nil {
		t.Fatal("expected executor admission enforcement")
	}
}

func TestSchedulerCannotForgeAdmissionToLowerDataClass(t *testing.T) {
	cp, task, worker, _ := fixture(t)

	attacker, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	forged := AdmissionApproval{Task: task}
	forged.Task.DataClass = DataPublic
	signedForged, err := sign(attacker, forged)
	if err != nil {
		t.Fatal(err)
	}
	proposal, _ := Propose(task.TaskID, worker.WorkerID)

	if _, err := cp.Placement.Approve(signedForged, proposal, worker); err == nil {
		t.Fatal("expected forged admission authority rejection")
	}
}

func TestArtifactAuthorityRejectsFakePlacement(t *testing.T) {
	cp, task, worker, now := fixture(t)
	admitted, _ := cp.Admission.Admit(task)

	attacker, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	fakePlacement, err := sign(attacker, PlacementApproval{TaskID: task.TaskID, WorkerID: worker.WorkerID})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cp.Artifact.Grant(admitted, fakePlacement, "sha256:input-a", now.Add(time.Minute)); err == nil {
		t.Fatal("expected fake placement rejection")
	}
}

func TestArtifactAuthorityCannotGrantUnadmittedArtifactThroughAPI(t *testing.T) {
	cp, task, worker, now := fixture(t)
	admitted, _ := cp.Admission.Admit(task)
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, _ := cp.Placement.Approve(admitted, proposal, worker)

	if _, err := cp.Artifact.Grant(admitted, placement, "sha256:not-an-input", now.Add(time.Minute)); err == nil {
		t.Fatal("expected artifact scope rejection")
	}
}

func TestWorkOrderRequiresEveryInputGrant(t *testing.T) {
	cp, task, worker, now := fixture(t)
	task.Inputs = append(task.Inputs, ArtifactRef{Digest: "sha256:input-b"})

	admitted, _ := cp.Admission.Admit(task)
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, _ := cp.Placement.Approve(admitted, proposal, worker)
	oneGrant, _ := cp.Artifact.Grant(admitted, placement, "sha256:input-a", now.Add(time.Minute))

	if _, err := cp.WorkOrder.Compile(admitted, placement, []Signed[ArtifactGrant]{oneGrant}, now); err == nil {
		t.Fatal("expected missing input grant rejection")
	}
}

func TestExpiredArtifactGrantCannotEnterWorkOrder(t *testing.T) {
	cp, task, worker, now := fixture(t)
	admitted, _ := cp.Admission.Admit(task)
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, _ := cp.Placement.Approve(admitted, proposal, worker)
	grant, _ := cp.Artifact.Grant(admitted, placement, "sha256:input-a", now.Add(-time.Second))

	if _, err := cp.WorkOrder.Compile(admitted, placement, []Signed[ArtifactGrant]{grant}, now); err == nil {
		t.Fatal("expected expired grant rejection")
	}
}

func TestSchedulerKeyCannotForgeWorkOrder(t *testing.T) {
	cp, task, worker, now := fixture(t)

	attacker, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := sign(attacker, WorkOrder{
		TaskID: task.TaskID, WorkerID: worker.WorkerID, ExecutorDigest: task.ExecutorDigest, DataClass: task.DataClass,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyForWorker(cp.Public.WorkOrder, fake, worker.WorkerID, now); err == nil {
		t.Fatal("expected invalid WorkOrder authority")
	}
}

func TestWorkerRejectsWorkOrderForAnotherWorker(t *testing.T) {
	cp, task, worker, now := fixture(t)

	admitted, _ := cp.Admission.Admit(task)
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, _ := cp.Placement.Approve(admitted, proposal, worker)
	grant, _ := cp.Artifact.Grant(admitted, placement, "sha256:input-a", now.Add(time.Minute))
	order, _ := cp.WorkOrder.Compile(admitted, placement, []Signed[ArtifactGrant]{grant}, now)

	if err := VerifyForWorker(cp.Public.WorkOrder, order, "worker-other", now); err == nil {
		t.Fatal("expected worker binding rejection")
	}
}

func TestCallerCannotChoosePlacementAdmissionTrustRoot(t *testing.T) {
	cp, task, worker, _ := fixture(t)

	attacker, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	forgedTask := task
	forgedTask.DataClass = DataPublic
	forged, err := sign(attacker, AdmissionApproval{Task: forgedTask})
	if err != nil {
		t.Fatal(err)
	}
	proposal, _ := Propose(task.TaskID, worker.WorkerID)

	if _, err := cp.Placement.Approve(forged, proposal, worker); err == nil {
		t.Fatal("placement guard must use its pinned admission authority")
	}
}

func TestDuplicateArtifactGrantIsRejected(t *testing.T) {
	cp, task, worker, now := fixture(t)

	admitted, _ := cp.Admission.Admit(task)
	proposal, _ := Propose(task.TaskID, worker.WorkerID)
	placement, _ := cp.Placement.Approve(admitted, proposal, worker)
	grant, _ := cp.Artifact.Grant(admitted, placement, "sha256:input-a", now.Add(time.Minute))

	if _, err := cp.WorkOrder.Compile(admitted, placement, []Signed[ArtifactGrant]{grant, grant}, now); err == nil {
		t.Fatal("expected duplicate artifact grant rejection")
	}
}
