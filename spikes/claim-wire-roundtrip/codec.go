// Package claimwire is an isolated feasibility experiment, not a transport.
package claimwire

import (
	"context"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bufbuild/protocompile"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed candidate.proto
var schema string

const maxMessageBytes = 64 << 10

var errInvalid = errors.New("invalid experimental claim message")

type codec struct{ file protoreflect.FileDescriptor }

func newCodec(ctx context.Context) (*codec, error) {
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
		Accessor: protocompile.SourceAccessorFromMap(map[string]string{"candidate.proto": schema}),
	})}
	files, err := compiler.Compile(ctx, "candidate.proto")
	if err != nil {
		return nil, err
	}
	return &codec{file: files[0]}, nil
}

func (c *codec) message(name protoreflect.Name) *dynamicpb.Message {
	return dynamicpb.NewMessage(c.file.Messages().ByName(name))
}

func field(m protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return m.Descriptor().Fields().ByName(name)
}

func put(m protoreflect.Message, name protoreflect.Name, v protoreflect.Value) {
	m.Set(field(m, name), v)
}

func get(m protoreflect.Message, name protoreflect.Name) protoreflect.Value {
	return m.Get(field(m, name))
}

func raw(d contract.Digest) []byte {
	if d.IsZero() {
		return nil
	}
	b, _ := hex.DecodeString(d.Hex())
	return b
}

func digest(b []byte, optional bool) (contract.Digest, error) {
	if optional && len(b) == 0 {
		return contract.Digest{}, nil
	}
	if len(b) != 32 {
		return contract.Digest{}, errInvalid
	}
	return contract.ParseSHA256(hex.EncodeToString(b))
}

var statuses = []resultclaim.Status{"", resultclaim.StatusCompleted, resultclaim.StatusFailed,
	resultclaim.StatusReclaimed, resultclaim.StatusDeadlineExceeded, resultclaim.StatusCancelled,
	resultclaim.StatusOutputExceeded, resultclaim.StatusRunnerStuck}
var decisions = []string{"", "new", "duplicate", "stale", "late"}
var phases = []string{"", string(attemptstate.AttemptPhaseCreated), string(attemptstate.AttemptPhasePlacing),
	string(attemptstate.AttemptPhaseAssigned), string(attemptstate.AttemptPhaseDispatched),
	string(attemptstate.AttemptPhaseClaimed), string(attemptstate.AttemptPhaseVerifying), string(attemptstate.AttemptPhaseClosed)}

func enumIndex[T ~string](values []T, v T) protoreflect.EnumNumber {
	for i := 1; i < len(values); i++ {
		if values[i] == v {
			return protoreflect.EnumNumber(i)
		}
	}
	return 0
}

// This experiment deliberately supports the timestamp range represented by
// existing SQLite nanosecond storage, not every timestamp accepted by Core.
func checkedTime(t time.Time) error {
	if t.IsZero() || !time.Unix(0, t.UnixNano()).Equal(t) || timestamppb.New(t).CheckValid() != nil {
		return errInvalid
	}
	return nil
}

func (c *codec) encodeClaim(p workerplane.Principal, claim resultclaim.Claim) ([]byte, error) {
	if !claim.Valid() || p.WorkerIdentity == "" || claim.WorkerIdentity() != p.WorkerIdentity {
		return nil, errInvalid
	}
	if checkedTime(claim.StartedAt()) != nil || checkedTime(claim.EndedAt()) != nil {
		return nil, errInvalid
	}
	m := c.message("Claim")
	put(m, "experimental_profile", protoreflect.ValueOfUint32(1))
	for name, value := range map[protoreflect.Name]string{
		"task_id": claim.TaskID().String(), "attempt_id": claim.AttemptID().String(),
		"lease_id": claim.LeaseID().String(), "work_order_decision_id": claim.WorkOrderDecision(),
	} {
		put(m, name, protoreflect.ValueOfString(value))
	}
	for name, value := range map[protoreflect.Name][]byte{
		"task_ir_sha256": raw(claim.TaskIRDigest()), "executor_sha256": raw(claim.ExecutorDigest()),
		"nonce": claim.Nonce(), "output_sha256": raw(claim.OutputDigest()), "claim_sha256": raw(claim.Digest()),
	} {
		put(m, name, protoreflect.ValueOfBytes(value))
	}
	put(m, "fence_epoch", protoreflect.ValueOfUint64(claim.FenceEpoch()))
	put(m, "status", protoreflect.ValueOfEnum(enumIndex(statuses, claim.Status())))
	put(m, "exit_code", protoreflect.ValueOfInt64(int64(claim.ExitCode())))
	put(m, "output_bytes", protoreflect.ValueOfUint64(claim.OutputBytes()))
	put(m, "started_at", protoreflect.ValueOfMessage(timestamppb.New(claim.StartedAt()).ProtoReflect()))
	put(m, "ended_at", protoreflect.ValueOfMessage(timestamppb.New(claim.EndedAt()).ProtoReflect()))
	wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		return nil, err
	}
	_, err = c.decodeClaim(p, wire)
	return wire, err
}

func (c *codec) parse(name protoreflect.Name, wire []byte) (*dynamicpb.Message, error) {
	if len(wire) == 0 || len(wire) > maxMessageBytes {
		return nil, errInvalid
	}
	m := c.message(name)
	if err := (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(wire, m); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalid, err)
	}
	if get(m, "experimental_profile").Uint() != 1 || !knownFields(m) {
		return nil, errInvalid
	}
	return m, nil
}

func knownFields(m protoreflect.Message) bool {
	if len(m.GetUnknown()) != 0 {
		return false
	}
	valid := true
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Message() != nil && !knownFields(v.Message()) {
			valid = false
		}
		return valid
	})
	return valid
}

func readTime(m protoreflect.Message, name protoreflect.Name) (time.Time, error) {
	if !m.Has(field(m, name)) {
		return time.Time{}, errInvalid
	}
	v := get(m, name).Message()
	ts := &timestamppb.Timestamp{Seconds: get(v, "seconds").Int(), Nanos: int32(get(v, "nanos").Int())}
	if err := ts.CheckValid(); err != nil {
		return time.Time{}, errInvalid
	}
	t := ts.AsTime()
	return t, checkedTime(t)
}

func (c *codec) decodeClaim(p workerplane.Principal, wire []byte) (resultclaim.Claim, error) {
	m, err := c.parse("Claim", wire)
	if err != nil || p.WorkerIdentity == "" {
		return resultclaim.Claim{}, errInvalid
	}
	for _, name := range []protoreflect.Name{"exit_code", "output_bytes"} {
		if !m.Has(field(m, name)) {
			return resultclaim.Claim{}, errInvalid
		}
	}
	task, e1 := ids.ParseTaskID(get(m, "task_id").String())
	attempt, e2 := ids.ParseAttemptID(get(m, "attempt_id").String())
	lease, e3 := ids.ParseLeaseID(get(m, "lease_id").String())
	taskIR, e4 := digest(get(m, "task_ir_sha256").Bytes(), false)
	executor, e5 := digest(get(m, "executor_sha256").Bytes(), false)
	output, e6 := digest(get(m, "output_sha256").Bytes(), true)
	started, e7 := readTime(m, "started_at")
	ended, e8 := readTime(m, "ended_at")
	expected, e9 := digest(get(m, "claim_sha256").Bytes(), false)
	if errors.Join(e1, e2, e3, e4, e5, e6, e7, e8, e9) != nil {
		return resultclaim.Claim{}, errInvalid
	}
	status := int(get(m, "status").Enum())
	exit := get(m, "exit_code").Int()
	if status <= 0 || status >= len(statuses) || int64(int(exit)) != exit {
		return resultclaim.Claim{}, errInvalid
	}
	claim, err := resultclaim.NewClaim(resultclaim.ClaimConfig{
		TaskID: task, TaskIRDigest: taskIR, AttemptID: attempt, WorkerIdentity: p.WorkerIdentity,
		LeaseID: lease, FenceEpoch: get(m, "fence_epoch").Uint(),
		WorkOrderDecision: get(m, "work_order_decision_id").String(), ExecutorDigest: executor,
		Nonce: get(m, "nonce").Bytes(), Status: statuses[status], ExitCode: int(exit),
		OutputBytes: get(m, "output_bytes").Uint(), OutputDigest: output, StartedAt: started, EndedAt: ended,
	})
	if err != nil || claim.Digest() != expected {
		return resultclaim.Claim{}, errInvalid
	}
	return claim, nil
}

func (c *codec) encodeReceipt(ack workerplane.ClaimAck, claim resultclaim.Claim) ([]byte, error) {
	m := c.message("Receipt")
	put(m, "experimental_profile", protoreflect.ValueOfUint32(1))
	put(m, "decision", protoreflect.ValueOfEnum(enumIndex(decisions, ack.Decision)))
	put(m, "attempt_phase", protoreflect.ValueOfEnum(enumIndex(phases, ack.AttemptPhase)))
	put(m, "verification_required", protoreflect.ValueOfBool(ack.VerificationRequired))
	put(m, "claim_sha256", protoreflect.ValueOfBytes(raw(claim.Digest())))
	wire, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	_, err = c.decodeReceipt(wire, claim)
	return wire, err
}

func (c *codec) decodeReceipt(wire []byte, expected resultclaim.Claim) (workerplane.ClaimAck, error) {
	m, err := c.parse("Receipt", wire)
	if err != nil || !expected.Valid() {
		return workerplane.ClaimAck{}, errInvalid
	}
	d, p := int(get(m, "decision").Enum()), int(get(m, "attempt_phase").Enum())
	bound, err := digest(get(m, "claim_sha256").Bytes(), false)
	if err != nil || bound != expected.Digest() || d <= 0 || d >= len(decisions) || p <= 0 || p >= len(phases) ||
		!m.Has(field(m, "verification_required")) || get(m, "verification_required").Bool() != (phases[p] == string(attemptstate.AttemptPhaseVerifying)) {
		return workerplane.ClaimAck{}, errInvalid
	}
	return workerplane.ClaimAck{Decision: decisions[d], AttemptPhase: phases[p], VerificationRequired: get(m, "verification_required").Bool()}, nil
}
