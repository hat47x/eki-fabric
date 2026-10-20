package cbor

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

const maxWorkOrderPayloadBytes = 128 * 1024

var (
	ErrInvalidWorkOrderPayload          = errors.New("invalid P2 work order CBOR payload")
	ErrNonDeterministicWorkOrderPayload = errors.New("P2 work order payload is not RFC 8949 Core Deterministic Encoding")
)

var _ decisiondecoder.WorkOrderCodec = (*Decoder)(nil)

type decisionRefWire struct {
	Kind       string     `cbor:"1,keyasint"`
	DecisionID string     `cbor:"2,keyasint"`
	Digest     digestWire `cbor:"3,keyasint"`
}

type resourceLimitsWire struct {
	CPUMillis          uint64 `cbor:"1,keyasint"`
	MemoryBytes        uint64 `cbor:"2,keyasint"`
	TemporaryDiskBytes uint64 `cbor:"3,keyasint"`
	WallTimeMillis     uint64 `cbor:"4,keyasint"`
	OutputBytesMaximum uint64 `cbor:"5,keyasint"`
	NetworkBytesMax    uint64 `cbor:"6,keyasint"`
}

type gpuLimitWire struct {
	DeviceCount      uint64 `cbor:"1,keyasint"`
	MinimumVRAMBytes uint64 `cbor:"2,keyasint"`
}

type sandboxPolicyWire struct {
	Profile        string        `cbor:"1,keyasint"`
	NetworkProfile *string       `cbor:"2,keyasint,omitempty"`
	GPU            *gpuLimitWire `cbor:"3,keyasint,omitempty"`
}

type workOrderBodyWire struct {
	WorkOrderID        string             `cbor:"1,keyasint"`
	TaskID             string             `cbor:"2,keyasint"`
	TaskIRDigest       digestWire         `cbor:"3,keyasint"`
	AttemptID          string             `cbor:"4,keyasint"`
	WorkerIdentity     string             `cbor:"5,keyasint"`
	PlacementRef       decisionRefWire    `cbor:"6,keyasint"`
	LeaseRef           decisionRefWire    `cbor:"7,keyasint"`
	ExecutorApproval   decisionRefWire    `cbor:"8,keyasint"`
	ExecutorDigest     digestWire         `cbor:"9,keyasint"`
	ReadGrants         []decisionRefWire  `cbor:"10,keyasint"`
	WriteGrants        []decisionRefWire  `cbor:"11,keyasint"`
	Limits             resourceLimitsWire `cbor:"12,keyasint"`
	Sandbox            sandboxPolicyWire  `cbor:"13,keyasint"`
	ExternalEffectMode uint64             `cbor:"14,keyasint"`
	Nonce              []byte             `cbor:"15,keyasint"`
}

type workOrderWire struct {
	SchemaVersion   uint64            `cbor:"1,keyasint"`
	DecisionKind    string            `cbor:"2,keyasint"`
	DecisionID      string            `cbor:"3,keyasint"`
	Authority       authorityWire     `cbor:"4,keyasint"`
	IssuedAtMillis  uint64            `cbor:"5,keyasint"`
	ExpiresAtMillis uint64            `cbor:"6,keyasint"`
	PolicyRevision  string            `cbor:"7,keyasint"`
	Body            workOrderBodyWire `cbor:"8,keyasint"`
}

// EncodeWorkOrder returns the RFC 8949 Core Deterministic Encoding of the
// closed `work-order-v1` map.
func (d *Decoder) EncodeWorkOrder(order decisionpayload.WorkOrder) ([]byte, error) {
	if d == nil || d.encode == nil {
		return nil, fmt.Errorf("%w: encoder is not initialized", ErrInvalidWorkOrderPayload)
	}
	if !order.Valid() {
		return nil, fmt.Errorf("%w: WorkOrder is not a constructed value", ErrInvalidWorkOrderPayload)
	}
	taskIRDigest, err := encodeDigest(order.TaskIRDigest())
	if err != nil {
		return nil, fmt.Errorf("%w: TaskIR digest: %v", ErrInvalidWorkOrderPayload, err)
	}
	executorDigest, err := encodeDigest(order.ExecutorDigest())
	if err != nil {
		return nil, fmt.Errorf("%w: executor digest: %v", ErrInvalidWorkOrderPayload, err)
	}
	readGrants, err := encodeRefs(order.ReadGrants())
	if err != nil {
		return nil, err
	}
	writeGrants, err := encodeRefs(order.WriteGrants())
	if err != nil {
		return nil, err
	}
	sandbox := sandboxPolicyWire{Profile: order.Sandbox().Profile}
	if profile := order.Sandbox().NetworkProfile; profile != "" {
		sandbox.NetworkProfile = &profile
	}
	if gpu := order.Sandbox().GPU; gpu != nil {
		sandbox.GPU = &gpuLimitWire{DeviceCount: uint64(gpu.DeviceCount), MinimumVRAMBytes: gpu.MinimumVRAMBytes}
	}
	limits := order.Limits()
	wire := workOrderWire{
		SchemaVersion: order.SchemaVersion(),
		DecisionKind:  string(order.DecisionKind()),
		DecisionID:    order.DecisionID(),
		Authority: authorityWire{
			Role:      string(order.AuthorityRole()),
			Principal: order.AuthorityPrincipal(),
		},
		IssuedAtMillis:  uint64(order.IssuedAt().UnixMilli()),
		ExpiresAtMillis: uint64(order.ExpiresAt().UnixMilli()),
		PolicyRevision:  order.PolicyRevision(),
		Body: workOrderBodyWire{
			WorkOrderID:      order.WorkOrderID(),
			TaskID:           order.TaskID().String(),
			TaskIRDigest:     taskIRDigest,
			AttemptID:        order.AttemptID().String(),
			WorkerIdentity:   order.WorkerIdentity(),
			PlacementRef:     encodeRef(order.PlacementRef()),
			LeaseRef:         encodeRef(order.LeaseRef()),
			ExecutorApproval: encodeRef(order.ExecutorApprovalRef()),
			ExecutorDigest:   executorDigest,
			ReadGrants:       readGrants,
			WriteGrants:      writeGrants,
			Limits: resourceLimitsWire{
				CPUMillis:          limits.CPUMillis,
				MemoryBytes:        limits.MemoryBytes,
				TemporaryDiskBytes: limits.TemporaryDiskBytes,
				WallTimeMillis:     limits.WallTimeMillis,
				OutputBytesMaximum: limits.OutputBytesMaximum,
				NetworkBytesMax:    limits.NetworkBytesMax,
			},
			Sandbox:            sandbox,
			ExternalEffectMode: order.ExternalEffectMode(),
			Nonce:              order.Nonce(),
		},
	}
	out, err := d.encode.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkOrderPayload, err)
	}
	if len(out) > maxWorkOrderPayloadBytes {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidWorkOrderPayload, maxWorkOrderPayloadBytes)
	}
	return out, nil
}

// DecodeWorkOrder strictly decodes hostile `work-order-v1` payload bytes.
// Success does not prove the COSE signature or that the referenced Placement
// Approval, Lease, and Executor Approval are current.
func (d *Decoder) DecodeWorkOrder(payload []byte) (decisionpayload.WorkOrder, error) {
	if d == nil || d.workOrderDecode == nil || d.encode == nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: decoder is not initialized", ErrInvalidWorkOrderPayload)
	}
	if len(payload) == 0 || len(payload) > maxWorkOrderPayloadBytes {
		return decisionpayload.WorkOrder{}, fmt.Errorf(
			"%w: payload must contain 1..%d bytes",
			ErrInvalidWorkOrderPayload,
			maxWorkOrderPayloadBytes,
		)
	}

	var wire workOrderWire
	if err := d.workOrderDecode.Unmarshal(payload, &wire); err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: %v", ErrInvalidWorkOrderPayload, err)
	}
	canonical, err := d.encode.Marshal(wire)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: canonical re-encode: %v", ErrInvalidWorkOrderPayload, err)
	}
	if !bytes.Equal(canonical, payload) {
		return decisionpayload.WorkOrder{}, ErrNonDeterministicWorkOrderPayload
	}
	if wire.IssuedAtMillis > math.MaxInt64 || wire.ExpiresAtMillis > math.MaxInt64 {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: timestamp exceeds int64 milliseconds", ErrInvalidWorkOrderPayload)
	}
	taskID, err := ids.ParseTaskID(wire.Body.TaskID)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: task ID: %v", ErrInvalidWorkOrderPayload, err)
	}
	attemptID, err := ids.ParseAttemptID(wire.Body.AttemptID)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidWorkOrderPayload, err)
	}
	taskIRDigest, err := parseDigest(wire.Body.TaskIRDigest)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: TaskIR digest: %v", ErrInvalidWorkOrderPayload, err)
	}
	executorDigest, err := parseDigest(wire.Body.ExecutorDigest)
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: executor digest: %v", ErrInvalidWorkOrderPayload, err)
	}
	placementRef, err := parseRef("placement approval", wire.Body.PlacementRef)
	if err != nil {
		return decisionpayload.WorkOrder{}, err
	}
	leaseRef, err := parseRef("lease", wire.Body.LeaseRef)
	if err != nil {
		return decisionpayload.WorkOrder{}, err
	}
	executorRef, err := parseRef("executor approval", wire.Body.ExecutorApproval)
	if err != nil {
		return decisionpayload.WorkOrder{}, err
	}
	readGrants, err := parseRefs("read grant", wire.Body.ReadGrants)
	if err != nil {
		return decisionpayload.WorkOrder{}, err
	}
	writeGrants, err := parseRefs("write grant", wire.Body.WriteGrants)
	if err != nil {
		return decisionpayload.WorkOrder{}, err
	}
	if wire.Body.Sandbox.GPU != nil && wire.Body.Sandbox.GPU.DeviceCount > math.MaxUint32 {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: GPU device count exceeds uint32", ErrInvalidWorkOrderPayload)
	}
	sandbox := decisionpayload.SandboxPolicy{Profile: wire.Body.Sandbox.Profile}
	if wire.Body.Sandbox.NetworkProfile != nil {
		sandbox.NetworkProfile = *wire.Body.Sandbox.NetworkProfile
	}
	if wire.Body.Sandbox.GPU != nil {
		sandbox.GPU = &decisionpayload.GPULimit{
			DeviceCount:      uint32(wire.Body.Sandbox.GPU.DeviceCount),
			MinimumVRAMBytes: wire.Body.Sandbox.GPU.MinimumVRAMBytes,
		}
	}

	decoded, err := decisionpayload.NewWorkOrder(decisionpayload.WorkOrderConfig{
		SchemaVersion:      wire.SchemaVersion,
		DecisionKind:       authority.DecisionKind(wire.DecisionKind),
		DecisionID:         wire.DecisionID,
		AuthorityRole:      authority.Role(wire.Authority.Role),
		AuthorityPrincipal: wire.Authority.Principal,
		IssuedAt:           time.UnixMilli(int64(wire.IssuedAtMillis)).UTC(),
		ExpiresAt:          time.UnixMilli(int64(wire.ExpiresAtMillis)).UTC(),
		PolicyRevision:     wire.PolicyRevision,
		WorkOrderID:        wire.Body.WorkOrderID,
		TaskID:             taskID,
		TaskIRDigest:       taskIRDigest,
		AttemptID:          attemptID,
		WorkerIdentity:     wire.Body.WorkerIdentity,
		PlacementRef:       placementRef,
		LeaseRef:           leaseRef,
		ExecutorApproval:   executorRef,
		ExecutorDigest:     executorDigest,
		ReadGrants:         readGrants,
		WriteGrants:        writeGrants,
		Limits: decisionpayload.ResourceLimits{
			CPUMillis:          wire.Body.Limits.CPUMillis,
			MemoryBytes:        wire.Body.Limits.MemoryBytes,
			TemporaryDiskBytes: wire.Body.Limits.TemporaryDiskBytes,
			WallTimeMillis:     wire.Body.Limits.WallTimeMillis,
			OutputBytesMaximum: wire.Body.Limits.OutputBytesMaximum,
			NetworkBytesMax:    wire.Body.Limits.NetworkBytesMax,
		},
		Sandbox:            sandbox,
		ExternalEffectMode: wire.Body.ExternalEffectMode,
		Nonce:              wire.Body.Nonce,
	})
	if err != nil {
		return decisionpayload.WorkOrder{}, fmt.Errorf("%w: %v", ErrInvalidWorkOrderPayload, err)
	}
	return decoded, nil
}

func encodeDigest(digest contract.Digest) (digestWire, error) {
	if digest.IsZero() {
		return digestWire{}, errors.New("digest is required")
	}
	raw, err := hex.DecodeString(digest.Hex())
	if err != nil || len(raw) != 32 {
		return digestWire{}, errors.New("digest is not a 32 byte SHA-256 value")
	}
	return digestWire{Algorithm: 1, Value: raw}, nil
}

func encodeRef(ref decisionpayload.DecisionRef) decisionRefWire {
	digest, _ := encodeDigest(ref.PayloadDigest())
	return decisionRefWire{Kind: string(ref.Kind()), DecisionID: ref.DecisionID(), Digest: digest}
}

func encodeRefs(refs []decisionpayload.DecisionRef) ([]decisionRefWire, error) {
	out := make([]decisionRefWire, 0, len(refs))
	for _, ref := range refs {
		if _, err := encodeDigest(ref.PayloadDigest()); err != nil {
			return nil, fmt.Errorf("%w: reference digest: %v", ErrInvalidWorkOrderPayload, err)
		}
		out = append(out, encodeRef(ref))
	}
	return out, nil
}

func parseRef(name string, wire decisionRefWire) (decisionpayload.DecisionRef, error) {
	digest, err := parseDigest(wire.Digest)
	if err != nil {
		return decisionpayload.DecisionRef{}, fmt.Errorf("%w: %s digest: %v", ErrInvalidWorkOrderPayload, name, err)
	}
	ref, err := decisionpayload.NewDecisionRef(decisionpayload.DecisionRefConfig{
		Kind:          authority.DecisionKind(wire.Kind),
		DecisionID:    wire.DecisionID,
		PayloadDigest: digest,
	})
	if err != nil {
		return decisionpayload.DecisionRef{}, fmt.Errorf("%w: %s: %v", ErrInvalidWorkOrderPayload, name, err)
	}
	return ref, nil
}

func parseRefs(name string, wires []decisionRefWire) ([]decisionpayload.DecisionRef, error) {
	out := make([]decisionpayload.DecisionRef, 0, len(wires))
	for i, wire := range wires {
		ref, err := parseRef(fmt.Sprintf("%s[%d]", name, i), wire)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}
