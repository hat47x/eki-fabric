package admission

import (
	"testing"
	"time"
)

type fixtureData struct {
	now       time.Time
	artifact  []byte
	digest    string
	publisher KeyPair
	builder   KeyPair
	admission KeyPair
	authority *AdmissionAuthority
	guard     *ExecutorGuard
	policy    AdmissionPolicy
}

func fixture(t *testing.T) fixtureData {
	t.Helper()
	publisher, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	adm, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	artifact := []byte("approved executor bytes v1")
	digest := DigestBytes(artifact)
	p := AdmissionPolicy{
		ExecutorID:       "dev.example.transform@1",
		AllowedDigest:    digest,
		AllowedPublisher: "release-bot@example",
		PublisherPublic:  publisher.Public,
		AllowedBuilder:   "builder.example/release",
		BuilderPublic:    builder.Public,
		AllowedSourceURI: "https://example.invalid/repo.git",
		AllowedCommit:    "0123456789abcdef",
		AllowedClass:     "wasm-component",
		PolicyRevision:   7,
		ApprovalTTL:      time.Hour,
	}
	authority, err := NewAdmissionAuthority(adm, []AdmissionPolicy{p})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewExecutorGuard(adm.Public, map[string]uint64{p.ExecutorID: p.PolicyRevision})
	if err != nil {
		t.Fatal(err)
	}
	return fixtureData{
		now: time.Unix(1_700_000_000, 0), artifact: artifact, digest: digest,
		publisher: publisher, builder: builder, admission: adm,
		authority: authority, guard: guard, policy: p,
	}
}

func request(t *testing.T, f fixtureData, artifact []byte) ExecutorRequest {
	t.Helper()
	digest := DigestBytes(artifact)
	pub, err := SignPublisher(f.publisher, digest, f.policy.AllowedPublisher)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := SignProvenance(f.builder, Provenance{
		SubjectDigest: digest,
		BuilderID:     f.policy.AllowedBuilder,
		SourceURI:     f.policy.AllowedSourceURI,
		SourceCommit:  f.policy.AllowedCommit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ExecutorRequest{
		ExecutorID:     f.policy.ExecutorID,
		Artifact:       ExecutorArtifact{Digest: digest, MediaType: "application/wasm", Size: uint64(len(artifact))},
		Publisher:      pub,
		Provenance:     prov,
		RequestedClass: f.policy.AllowedClass,
	}
}

func TestHappyPathBindsApprovalToContentDigest(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	approval, err := f.authority.Admit(f.now, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.guard.VerifyFetchedContent(f.now, approval, f.artifact); err != nil {
		t.Fatal(err)
	}
}

func TestMutableAliasSubstitutionFailsAtFetchedDigest(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	approval, err := f.authority.Admit(f.now, req)
	if err != nil {
		t.Fatal(err)
	}

	replaced := []byte("malicious bytes now behind executor:v1")
	if err := f.guard.VerifyFetchedContent(f.now, approval, replaced); err == nil {
		t.Fatal("mutable alias substitution must not change approved executor content")
	}
}

func TestSignedButWrongSubjectIsRejected(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	wrong := DigestBytes([]byte("other artifact"))
	pub, _ := SignPublisher(f.publisher, wrong, f.policy.AllowedPublisher)
	req.Publisher = pub
	if _, err := f.authority.Admit(f.now, req); err == nil {
		t.Fatal("signature over another subject digest must not authorize this artifact")
	}
}

func TestUntrustedPublisherIsRejected(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	attacker, _ := NewKeyPair()
	req.Publisher, _ = SignPublisher(attacker, f.digest, f.policy.AllowedPublisher)
	if _, err := f.authority.Admit(f.now, req); err == nil {
		t.Fatal("untrusted publisher signature must be rejected")
	}
}

func TestBuilderIdentityAndSourceArePolicyBound(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	req.Provenance, _ = SignProvenance(f.builder, Provenance{
		SubjectDigest: f.digest,
		BuilderID:     "builder.example/untrusted",
		SourceURI:     f.policy.AllowedSourceURI,
		SourceCommit:  f.policy.AllowedCommit,
	})
	if _, err := f.authority.Admit(f.now, req); err == nil {
		t.Fatal("unexpected builder must be rejected")
	}

	req = request(t, f, f.artifact)
	req.Provenance, _ = SignProvenance(f.builder, Provenance{
		SubjectDigest: f.digest,
		BuilderID:     f.policy.AllowedBuilder,
		SourceURI:     "https://attacker.invalid/repo.git",
		SourceCommit:  f.policy.AllowedCommit,
	})
	if _, err := f.authority.Admit(f.now, req); err == nil {
		t.Fatal("unexpected source must be rejected")
	}
}

func TestSignatureAndProvenanceAreNotAdmissionByThemselves(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	if !Verify(f.publisher.Public, req.Publisher) || !Verify(f.builder.Public, req.Provenance) {
		t.Fatal("fixture evidence should be cryptographically valid")
	}

	fakeAdmission, _ := NewKeyPair()
	approval, _ := sign(fakeAdmission, ExecutorApproval{
		ExecutorID:     f.policy.ExecutorID,
		ArtifactDigest: f.digest,
		MediaType:      "application/wasm",
		Size:           uint64(len(f.artifact)),
		RuntimeClass:   f.policy.AllowedClass,
		PolicyRevision: f.policy.PolicyRevision,
		Expires:        f.now.Add(time.Hour),
	})
	if err := f.guard.VerifyFetchedContent(f.now, approval, f.artifact); err == nil {
		t.Fatal("publisher/provenance evidence cannot replace executor admission authority")
	}
}

func TestRevocationAfterAdmissionStopsExecution(t *testing.T) {
	f := fixture(t)
	approval, err := f.authority.Admit(f.now, request(t, f, f.artifact))
	if err != nil {
		t.Fatal(err)
	}
	f.guard.RevokeDigest(f.digest)
	if err := f.guard.VerifyFetchedContent(f.now, approval, f.artifact); err == nil {
		t.Fatal("revoked digest must not execute even with earlier approval")
	}
}

func TestStalePolicyRevisionStopsExecution(t *testing.T) {
	f := fixture(t)
	approval, err := f.authority.Admit(f.now, request(t, f, f.artifact))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewExecutorGuard(f.admission.Public, map[string]uint64{f.policy.ExecutorID: f.policy.PolicyRevision + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.VerifyFetchedContent(f.now, approval, f.artifact); err == nil {
		t.Fatal("approval from stale policy revision must be rejected")
	}
}

func TestExpiredApprovalStopsExecution(t *testing.T) {
	f := fixture(t)
	approval, err := f.authority.Admit(f.now, request(t, f, f.artifact))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.guard.VerifyFetchedContent(f.now.Add(2*time.Hour), approval, f.artifact); err == nil {
		t.Fatal("expired approval must be rejected")
	}
}

func TestClientCannotRequestDifferentRuntimeClass(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	req.RequestedClass = "native-unrestricted"
	if _, err := f.authority.Admit(f.now, req); err == nil {
		t.Fatal("client must not widen executor runtime class")
	}
}

func TestDescriptorSizeMismatchIsRejectedAtExecution(t *testing.T) {
	f := fixture(t)
	req := request(t, f, f.artifact)
	req.Artifact.Size++
	approval, err := f.authority.Admit(f.now, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.guard.VerifyFetchedContent(f.now, approval, f.artifact); err == nil {
		t.Fatal("approved descriptor size must match fetched bytes")
	}
}
