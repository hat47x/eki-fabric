package cbor

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/decisiondecoder"
)

const maxLeasePayloadBytes = 64 * 1024

var (
	ErrInvalidLeasePayload          = errors.New("invalid P2 lease CBOR payload")
	ErrNonDeterministicLeasePayload = errors.New("P2 lease payload is not RFC 8949 Core Deterministic Encoding")
)

var _ decisiondecoder.LeaseCodec = (*Decoder)(nil)

type leaseBodyWire struct {
	TaskID          string     `cbor:"1,keyasint"`
	TaskIRDigest    digestWire `cbor:"2,keyasint"`
	AttemptID       string     `cbor:"3,keyasint"`
	WorkerIdentity  string     `cbor:"4,keyasint"`
	LeaseID         string     `cbor:"5,keyasint"`
	FenceEpoch      uint64     `cbor:"6,keyasint"`
	RenewalSequence uint64     `cbor:"7,keyasint"`
}

type leaseWire struct {
	SchemaVersion   uint64        `cbor:"1,keyasint"`
	DecisionKind    string        `cbor:"2,keyasint"`
	DecisionID      string        `cbor:"3,keyasint"`
	Authority       authorityWire `cbor:"4,keyasint"`
	IssuedAtMillis  uint64        `cbor:"5,keyasint"`
	ExpiresAtMillis uint64        `cbor:"6,keyasint"`
	PolicyRevision  string        `cbor:"7,keyasint"`
	Body            leaseBodyWire `cbor:"8,keyasint"`
}

// EncodeLease returns the RFC 8949 Core Deterministic Encoding of the closed
// `lease-v1` map. The same Lease always yields the same bytes.
func (d *Decoder) EncodeLease(lease decisionpayload.Lease) ([]byte, error) {
	if d == nil || d.encode == nil {
		return nil, fmt.Errorf("%w: encoder is not initialized", ErrInvalidLeasePayload)
	}
	if !lease.Valid() {
		return nil, fmt.Errorf("%w: Lease is not a constructed value", ErrInvalidLeasePayload)
	}
	digest, err := hex.DecodeString(lease.TaskIRDigest().Hex())
	if err != nil || len(digest) != 32 {
		return nil, fmt.Errorf("%w: TaskIR digest", ErrInvalidLeasePayload)
	}
	wire := leaseWire{
		SchemaVersion: lease.SchemaVersion(),
		DecisionKind:  string(lease.DecisionKind()),
		DecisionID:    lease.DecisionID(),
		Authority: authorityWire{
			Role:      string(lease.AuthorityRole()),
			Principal: lease.AuthorityPrincipal(),
		},
		IssuedAtMillis:  uint64(lease.IssuedAt().UnixMilli()),
		ExpiresAtMillis: uint64(lease.ExpiresAt().UnixMilli()),
		PolicyRevision:  lease.PolicyRevision(),
		Body: leaseBodyWire{
			TaskID:          lease.TaskID().String(),
			TaskIRDigest:    digestWire{Algorithm: 1, Value: digest},
			AttemptID:       lease.AttemptID().String(),
			WorkerIdentity:  lease.WorkerIdentity(),
			LeaseID:         lease.LeaseID().String(),
			FenceEpoch:      lease.FenceEpoch(),
			RenewalSequence: lease.RenewalSequence(),
		},
	}
	out, err := d.encode.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLeasePayload, err)
	}
	if len(out) > maxLeasePayloadBytes {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidLeasePayload, maxLeasePayloadBytes)
	}
	return out, nil
}

// DecodeLease strictly decodes hostile `lease-v1` payload bytes. Success does
// not prove the COSE signature or current Lease authority.
func (d *Decoder) DecodeLease(payload []byte) (decisionpayload.Lease, error) {
	if d == nil || d.decode == nil || d.encode == nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: decoder is not initialized", ErrInvalidLeasePayload)
	}
	if len(payload) == 0 || len(payload) > maxLeasePayloadBytes {
		return decisionpayload.Lease{}, fmt.Errorf(
			"%w: payload must contain 1..%d bytes",
			ErrInvalidLeasePayload,
			maxLeasePayloadBytes,
		)
	}

	var wire leaseWire
	if err := d.decode.Unmarshal(payload, &wire); err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: %v", ErrInvalidLeasePayload, err)
	}
	canonical, err := d.encode.Marshal(wire)
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: canonical re-encode: %v", ErrInvalidLeasePayload, err)
	}
	if !bytes.Equal(canonical, payload) {
		return decisionpayload.Lease{}, ErrNonDeterministicLeasePayload
	}

	if wire.IssuedAtMillis > math.MaxInt64 || wire.ExpiresAtMillis > math.MaxInt64 {
		return decisionpayload.Lease{}, fmt.Errorf("%w: timestamp exceeds int64 milliseconds", ErrInvalidLeasePayload)
	}
	taskID, err := ids.ParseTaskID(wire.Body.TaskID)
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: task ID: %v", ErrInvalidLeasePayload, err)
	}
	attemptID, err := ids.ParseAttemptID(wire.Body.AttemptID)
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: attempt ID: %v", ErrInvalidLeasePayload, err)
	}
	leaseID, err := ids.ParseLeaseID(wire.Body.LeaseID)
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: lease ID: %v", ErrInvalidLeasePayload, err)
	}
	taskIRDigest, err := parseDigest(wire.Body.TaskIRDigest)
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: TaskIR digest: %v", ErrInvalidLeasePayload, err)
	}

	decoded, err := decisionpayload.NewLease(decisionpayload.LeaseConfig{
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
		LeaseID:            leaseID,
		FenceEpoch:         wire.Body.FenceEpoch,
		RenewalSequence:    wire.Body.RenewalSequence,
	})
	if err != nil {
		return decisionpayload.Lease{}, fmt.Errorf("%w: %v", ErrInvalidLeasePayload, err)
	}
	return decoded, nil
}
