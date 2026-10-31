package cbor

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

const maxPlacementApprovalPayloadBytes = 64 * 1024

var (
	ErrInvalidPlacementApprovalPayload = errors.New("invalid P2 placement approval CBOR payload")
	ErrNonDeterministicPayload         = errors.New("P2 placement approval payload is not RFC 8949 Core Deterministic Encoding")
)

type Decoder struct {
	decode          fxcbor.DecMode
	workOrderDecode fxcbor.DecMode
	encode          fxcbor.EncMode
}

var _ decisiondecoder.PlacementApprovalDecoder = (*Decoder)(nil)

func New() (*Decoder, error) {
	decode, err := fxcbor.DecOptions{
		DupMapKey:         fxcbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:   8,
		MaxArrayElements:  16,
		MaxMapPairs:       16,
		IndefLength:       fxcbor.IndefLengthForbidden,
		TagsMd:            fxcbor.TagsForbidden,
		ExtraReturnErrors: fxcbor.ExtraDecErrorUnknownField,
	}.DecMode()
	if err != nil {
		return nil, fmt.Errorf("create strict P2 decoder: %w", err)
	}
	// A WorkOrder carries up to 64 read grants in one body array, so it needs a
	// wider array limit than the other P2 payloads. The nesting and map limits
	// stay the same.
	workOrderDecode, err := fxcbor.DecOptions{
		DupMapKey:         fxcbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:   8,
		MaxArrayElements:  80,
		MaxMapPairs:       16,
		IndefLength:       fxcbor.IndefLengthForbidden,
		TagsMd:            fxcbor.TagsForbidden,
		ExtraReturnErrors: fxcbor.ExtraDecErrorUnknownField,
	}.DecMode()
	if err != nil {
		return nil, fmt.Errorf("create strict P2 WorkOrder decoder: %w", err)
	}
	encode, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		return nil, fmt.Errorf("create deterministic P2 encoder: %w", err)
	}
	return &Decoder{decode: decode, workOrderDecode: workOrderDecode, encode: encode}, nil
}

type digestWire struct {
	Algorithm uint64 `cbor:"1,keyasint"`
	Value     []byte `cbor:"2,keyasint"`
}

type authorityWire struct {
	Role      string `cbor:"1,keyasint"`
	Principal string `cbor:"2,keyasint"`
}

type evidenceWire struct {
	Kind     string     `cbor:"1,keyasint"`
	Identity string     `cbor:"2,keyasint"`
	Digest   digestWire `cbor:"3,keyasint"`
}

type placementBodyWire struct {
	TaskID         string          `cbor:"1,keyasint"`
	TaskIRDigest   digestWire      `cbor:"2,keyasint"`
	AttemptID      string          `cbor:"3,keyasint"`
	WorkerIdentity string          `cbor:"4,keyasint"`
	Evidence       *[]evidenceWire `cbor:"5,keyasint,omitempty"`
}

type placementApprovalWire struct {
	SchemaVersion   uint64            `cbor:"1,keyasint"`
	DecisionKind    string            `cbor:"2,keyasint"`
	DecisionID      string            `cbor:"3,keyasint"`
	Authority       authorityWire     `cbor:"4,keyasint"`
	IssuedAtMillis  uint64            `cbor:"5,keyasint"`
	ExpiresAtMillis uint64            `cbor:"6,keyasint"`
	PolicyRevision  string            `cbor:"7,keyasint"`
	Body            placementBodyWire `cbor:"8,keyasint"`
}

// EncodePlacementApproval returns the RFC 8949 Core Deterministic Encoding of
// the closed `placement-approval-v1` map. The same Approval always yields the
// same bytes. The payload digest is not a wire field: a producer derives it
// from the returned bytes.
func (d *Decoder) EncodePlacementApproval(approval decisionpayload.PlacementApproval) ([]byte, error) {
	if d == nil || d.encode == nil {
		return nil, fmt.Errorf("%w: encoder is not initialized", ErrInvalidPlacementApprovalPayload)
	}
	if !approval.Encodable() {
		return nil, fmt.Errorf("%w: Placement Approval is not a constructed value", ErrInvalidPlacementApprovalPayload)
	}
	taskIRDigest, err := hex.DecodeString(approval.TaskIRDigest().Hex())
	if err != nil || len(taskIRDigest) != 32 {
		return nil, fmt.Errorf("%w: TaskIR digest", ErrInvalidPlacementApprovalPayload)
	}
	var evidence *[]evidenceWire
	if refs := approval.Evidence(); len(refs) > 0 {
		wireEvidence := make([]evidenceWire, 0, len(refs))
		for _, ref := range refs {
			refDigest, err := hex.DecodeString(ref.Digest().Hex())
			if err != nil || len(refDigest) != 32 {
				return nil, fmt.Errorf("%w: evidence %q digest", ErrInvalidPlacementApprovalPayload, ref.Identity())
			}
			wireEvidence = append(wireEvidence, evidenceWire{
				Kind:     ref.Kind(),
				Identity: ref.Identity(),
				Digest:   digestWire{Algorithm: 1, Value: refDigest},
			})
		}
		evidence = &wireEvidence
	}
	wire := placementApprovalWire{
		SchemaVersion: approval.SchemaVersion(),
		DecisionKind:  string(approval.DecisionKind()),
		DecisionID:    approval.DecisionID(),
		Authority: authorityWire{
			Role:      string(approval.AuthorityRole()),
			Principal: approval.AuthorityPrincipal(),
		},
		IssuedAtMillis:  uint64(approval.IssuedAt().UnixMilli()),
		ExpiresAtMillis: uint64(approval.ExpiresAt().UnixMilli()),
		PolicyRevision:  approval.PolicyRevision(),
		Body: placementBodyWire{
			TaskID:         approval.TaskID().String(),
			TaskIRDigest:   digestWire{Algorithm: 1, Value: taskIRDigest},
			AttemptID:      approval.AttemptID().String(),
			WorkerIdentity: approval.WorkerIdentity(),
			Evidence:       evidence,
		},
	}
	out, err := d.encode.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	if len(out) > maxPlacementApprovalPayloadBytes {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidPlacementApprovalPayload, maxPlacementApprovalPayloadBytes)
	}
	return out, nil
}

var _ decisiondecoder.PlacementApprovalCodec = (*Decoder)(nil)

func (d *Decoder) DecodePlacementApproval(payload []byte) (decisionpayload.PlacementApproval, error) {
	if d == nil || d.decode == nil || d.encode == nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: decoder is not initialized", ErrInvalidPlacementApprovalPayload)
	}
	if len(payload) == 0 || len(payload) > maxPlacementApprovalPayloadBytes {
		return decisionpayload.PlacementApproval{}, fmt.Errorf(
			"%w: payload must contain 1..%d bytes",
			ErrInvalidPlacementApprovalPayload,
			maxPlacementApprovalPayloadBytes,
		)
	}

	var wire placementApprovalWire
	if err := d.decode.Unmarshal(payload, &wire); err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	canonical, err := d.encode.Marshal(wire)
	if err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: canonical re-encode: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	if !bytes.Equal(canonical, payload) {
		return decisionpayload.PlacementApproval{}, ErrNonDeterministicPayload
	}

	if wire.IssuedAtMillis > math.MaxInt64 || wire.ExpiresAtMillis > math.MaxInt64 {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: timestamp exceeds int64 milliseconds", ErrInvalidPlacementApprovalPayload)
	}
	taskID, err := ids.ParseTaskID(wire.Body.TaskID)
	if err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: task ID: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	attemptID, err := ids.ParseAttemptID(wire.Body.AttemptID)
	if err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	taskIRDigest, err := parseDigest(wire.Body.TaskIRDigest)
	if err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: TaskIR digest: %v", ErrInvalidPlacementApprovalPayload, err)
	}

	var evidence []decisionpayload.EvidenceRefConfig
	if wire.Body.Evidence != nil {
		evidence = make([]decisionpayload.EvidenceRefConfig, 0, len(*wire.Body.Evidence))
		for i, item := range *wire.Body.Evidence {
			digest, err := parseDigest(item.Digest)
			if err != nil {
				return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: evidence[%d] digest: %v", ErrInvalidPlacementApprovalPayload, i, err)
			}
			evidence = append(evidence, decisionpayload.EvidenceRefConfig{
				Kind:     item.Kind,
				Identity: item.Identity,
				Digest:   digest,
			})
		}
	}

	decoded, err := decisionpayload.NewPlacementApproval(decisionpayload.PlacementApprovalConfig{
		SchemaVersion:      wire.SchemaVersion,
		DecisionKind:       authority.DecisionKind(wire.DecisionKind),
		DecisionID:         wire.DecisionID,
		AuthorityRole:      authority.Role(wire.Authority.Role),
		AuthorityPrincipal: wire.Authority.Principal,
		IssuedAt:           time.UnixMilli(int64(wire.IssuedAtMillis)).UTC(),
		ExpiresAt:          time.UnixMilli(int64(wire.ExpiresAtMillis)).UTC(),
		PolicyRevision:     wire.PolicyRevision,
		TaskID:             taskID,
		TaskIRDigest:       taskIRDigest,
		AttemptID:          attemptID,
		WorkerIdentity:     wire.Body.WorkerIdentity,
		Evidence:           evidence,
		PayloadDigest:      contract.SHA256(payload),
	})
	if err != nil {
		return decisionpayload.PlacementApproval{}, fmt.Errorf("%w: %v", ErrInvalidPlacementApprovalPayload, err)
	}
	return decoded, nil
}

func parseDigest(value digestWire) (contract.Digest, error) {
	if value.Algorithm != 1 {
		return contract.Digest{}, fmt.Errorf("digest algorithm %d is not sha256(1)", value.Algorithm)
	}
	if len(value.Value) != 32 {
		return contract.Digest{}, fmt.Errorf("digest contains %d bytes, want 32", len(value.Value))
	}
	return contract.ParseSHA256(hex.EncodeToString(value.Value))
}
