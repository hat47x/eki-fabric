package admission

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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

type Signed[T any] struct {
	Payload   T      `json:"payload"`
	Signature []byte `json:"signature"`
}

func canonical(v any) ([]byte, error) { return json.Marshal(v) }

func sign[T any](key KeyPair, payload T) (Signed[T], error) {
	b, err := canonical(payload)
	if err != nil {
		return Signed[T]{}, err
	}
	return Signed[T]{Payload: payload, Signature: ed25519.Sign(key.private, b)}, nil
}

func Verify[T any](pub ed25519.PublicKey, s Signed[T]) bool {
	b, err := canonical(s.Payload)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, b, s.Signature)
}

func DigestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type ExecutorArtifact struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
	Size      uint64 `json:"size"`
}

type PublisherStatement struct {
	SubjectDigest string `json:"subject_digest"`
	PublisherID   string `json:"publisher_id"`
}

type Provenance struct {
	SubjectDigest string `json:"subject_digest"`
	BuilderID     string `json:"builder_id"`
	SourceURI     string `json:"source_uri"`
	SourceCommit  string `json:"source_commit"`
}

type ExecutorRequest struct {
	ExecutorID     string
	Artifact       ExecutorArtifact
	Publisher      Signed[PublisherStatement]
	Provenance     Signed[Provenance]
	RequestedClass string
}

type AdmissionPolicy struct {
	ExecutorID       string
	AllowedDigest    string
	AllowedPublisher string
	PublisherPublic  ed25519.PublicKey
	AllowedBuilder   string
	BuilderPublic    ed25519.PublicKey
	AllowedSourceURI string
	AllowedCommit    string
	AllowedClass     string
	PolicyRevision   uint64
	ApprovalTTL      time.Duration
}

type ExecutorApproval struct {
	ExecutorID     string    `json:"executor_id"`
	ArtifactDigest string    `json:"artifact_digest"`
	MediaType      string    `json:"media_type"`
	Size           uint64    `json:"size"`
	RuntimeClass   string    `json:"runtime_class"`
	PublisherID    string    `json:"publisher_id"`
	BuilderID      string    `json:"builder_id"`
	SourceURI      string    `json:"source_uri"`
	SourceCommit   string    `json:"source_commit"`
	PolicyRevision uint64    `json:"policy_revision"`
	Expires        time.Time `json:"expires"`
}

type AdmissionAuthority struct {
	key      KeyPair
	policies map[string]AdmissionPolicy
	revoked  map[string]bool
}

func NewAdmissionAuthority(key KeyPair, policies []AdmissionPolicy) (*AdmissionAuthority, error) {
	a := &AdmissionAuthority{key: key, policies: map[string]AdmissionPolicy{}, revoked: map[string]bool{}}
	for _, p := range policies {
		if p.ExecutorID == "" || p.AllowedDigest == "" || p.PolicyRevision == 0 || p.ApprovalTTL <= 0 {
			return nil, errors.New("executor id, digest, policy revision, and positive approval TTL are required")
		}
		if len(p.PublisherPublic) != ed25519.PublicKeySize || len(p.BuilderPublic) != ed25519.PublicKeySize {
			return nil, errors.New("publisher and builder trust roots are required")
		}
		if _, exists := a.policies[p.ExecutorID]; exists {
			return nil, fmt.Errorf("duplicate executor policy %q", p.ExecutorID)
		}
		a.policies[p.ExecutorID] = p
	}
	return a, nil
}

func (a *AdmissionAuthority) RevokeDigest(digest string) {
	if digest != "" {
		a.revoked[digest] = true
	}
}

func validateArtifactDescriptor(artifact ExecutorArtifact) error {
	if artifact.Digest == "" || artifact.MediaType == "" || artifact.Size == 0 {
		return errors.New("digest, media type, and non-zero size are required")
	}
	return nil
}

func (a *AdmissionAuthority) Admit(now time.Time, req ExecutorRequest) (Signed[ExecutorApproval], error) {
	p, ok := a.policies[req.ExecutorID]
	if !ok {
		return Signed[ExecutorApproval]{}, errors.New("executor is not registered")
	}
	if err := validateArtifactDescriptor(req.Artifact); err != nil {
		return Signed[ExecutorApproval]{}, err
	}
	if a.revoked[req.Artifact.Digest] {
		return Signed[ExecutorApproval]{}, errors.New("executor artifact is revoked")
	}
	if req.Artifact.Digest != p.AllowedDigest {
		return Signed[ExecutorApproval]{}, errors.New("artifact digest is not allowed by executor policy")
	}
	if req.RequestedClass != p.AllowedClass {
		return Signed[ExecutorApproval]{}, errors.New("runtime class is not allowed by executor policy")
	}

	if !Verify(p.PublisherPublic, req.Publisher) {
		return Signed[ExecutorApproval]{}, errors.New("publisher signature is not trusted")
	}
	pub := req.Publisher.Payload
	if pub.SubjectDigest != req.Artifact.Digest || pub.PublisherID != p.AllowedPublisher {
		return Signed[ExecutorApproval]{}, errors.New("publisher statement does not match admitted artifact or publisher")
	}

	if !Verify(p.BuilderPublic, req.Provenance) {
		return Signed[ExecutorApproval]{}, errors.New("provenance signature is not trusted")
	}
	prov := req.Provenance.Payload
	if prov.SubjectDigest != req.Artifact.Digest {
		return Signed[ExecutorApproval]{}, errors.New("provenance subject does not match admitted artifact")
	}
	if prov.BuilderID != p.AllowedBuilder || prov.SourceURI != p.AllowedSourceURI || prov.SourceCommit != p.AllowedCommit {
		return Signed[ExecutorApproval]{}, errors.New("provenance does not satisfy executor policy")
	}

	approval := ExecutorApproval{
		ExecutorID:     req.ExecutorID,
		ArtifactDigest: req.Artifact.Digest,
		MediaType:      req.Artifact.MediaType,
		Size:           req.Artifact.Size,
		RuntimeClass:   req.RequestedClass,
		PublisherID:    pub.PublisherID,
		BuilderID:      prov.BuilderID,
		SourceURI:      prov.SourceURI,
		SourceCommit:   prov.SourceCommit,
		PolicyRevision: p.PolicyRevision,
		Expires:        now.Add(p.ApprovalTTL),
	}
	return sign(a.key, approval)
}

type ExecutorGuard struct {
	admissionPub          ed25519.PublicKey
	currentPolicyRevision map[string]uint64
	revoked               map[string]bool
}

func NewExecutorGuard(admissionPub ed25519.PublicKey, policyRevision map[string]uint64) (*ExecutorGuard, error) {
	if len(admissionPub) != ed25519.PublicKeySize {
		return nil, errors.New("admission trust root is required")
	}
	revisions := make(map[string]uint64, len(policyRevision))
	for id, rev := range policyRevision {
		if id == "" || rev == 0 {
			return nil, errors.New("valid executor id and policy revision are required")
		}
		revisions[id] = rev
	}
	return &ExecutorGuard{admissionPub: append(ed25519.PublicKey(nil), admissionPub...), currentPolicyRevision: revisions, revoked: map[string]bool{}}, nil
}

func (g *ExecutorGuard) RevokeDigest(digest string) {
	if digest != "" {
		g.revoked[digest] = true
	}
}

func (g *ExecutorGuard) VerifyApproval(now time.Time, approval Signed[ExecutorApproval]) error {
	if !Verify(g.admissionPub, approval) {
		return errors.New("executor approval is not signed by the pinned admission authority")
	}
	p := approval.Payload
	if !p.Expires.After(now) {
		return errors.New("executor approval is expired")
	}
	if g.revoked[p.ArtifactDigest] {
		return errors.New("executor artifact is revoked")
	}
	current, ok := g.currentPolicyRevision[p.ExecutorID]
	if !ok || current != p.PolicyRevision {
		return errors.New("executor approval policy revision is stale")
	}
	return nil
}

func (g *ExecutorGuard) VerifyFetchedContent(now time.Time, approval Signed[ExecutorApproval], content []byte) error {
	if err := g.VerifyApproval(now, approval); err != nil {
		return err
	}
	p := approval.Payload
	if uint64(len(content)) != p.Size {
		return errors.New("fetched executor size does not match approved descriptor")
	}
	if DigestBytes(content) != p.ArtifactDigest {
		return errors.New("fetched executor digest does not match approved artifact")
	}
	return nil
}

func SignPublisher(key KeyPair, subjectDigest, publisherID string) (Signed[PublisherStatement], error) {
	if subjectDigest == "" || publisherID == "" {
		return Signed[PublisherStatement]{}, errors.New("subject digest and publisher id are required")
	}
	return sign(key, PublisherStatement{SubjectDigest: subjectDigest, PublisherID: publisherID})
}

func SignProvenance(key KeyPair, p Provenance) (Signed[Provenance], error) {
	if p.SubjectDigest == "" || p.BuilderID == "" || p.SourceURI == "" || p.SourceCommit == "" {
		return Signed[Provenance]{}, errors.New("complete provenance fields are required")
	}
	return sign(key, p)
}
