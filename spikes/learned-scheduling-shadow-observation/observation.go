package shadowsched

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

const Schema = "eki.learned-scheduling-shadow-observation/v0"

var validSplits = map[string]bool{"train": true, "valid": true, "test": true, "unassigned": true}
var validOutcomeStatuses = map[string]bool{"completed": true, "preempted": true, "failed": true, "censored": true}

type ResearchBoundary struct {
	ShadowOnly              bool `json:"shadow_only"`
	PlacementInfluenced     bool `json:"placement_influenced"`
	AuthorizationInfluenced bool `json:"authorization_influenced"`
}

type BaselineEstimate struct {
	Eligible            bool   `json:"eligible"`
	RejectReason        string `json:"reject_reason"`
	TotalInputBytes     uint64 `json:"total_input_bytes"`
	LocalInputBytes     uint64 `json:"local_input_bytes"`
	TransferInputBytes  uint64 `json:"transfer_input_bytes"`
	TransferOutputBytes uint64 `json:"transfer_output_bytes"`
	TransferBytes       uint64 `json:"transfer_bytes"`
	TransferMillis      uint64 `json:"transfer_millis"`
	ComputeMillis       uint64 `json:"compute_millis"`
	EstimatedMillis     uint64 `json:"estimated_millis"`
}

type Outcome struct {
	Status         string  `json:"status"`
	ObservedMillis uint64  `json:"observed_millis"`
	ActualMillis   *uint64 `json:"actual_completion_millis"`
	DeadlineMillis *uint64 `json:"deadline_millis"`
}

type Split struct {
	Name             string `json:"split"`
	ScenarioFamily   string `json:"scenario_family"`
	WorkerPopulation string `json:"worker_population"`
	TimeRegime       string `json:"time_regime"`
}

type Observation struct {
	Schema               string           `json:"schema"`
	ObservationID        string           `json:"observation_id"`
	FeatureSchemaVersion string           `json:"feature_schema_version"`
	ScenarioFamily       string           `json:"scenario_family"`
	WorkerPopulation     string           `json:"worker_population"`
	TimeRegime           string           `json:"time_regime"`
	TaskClass            string           `json:"task_class"`
	WorkerClass          string           `json:"worker_class"`
	PolicyEligible       bool             `json:"policy_eligible"`
	PolicyRejectReason   string           `json:"policy_reject_reason"`
	Baseline             BaselineEstimate `json:"deterministic_baseline"`
	Outcome              Outcome          `json:"outcome"`
	Split                Split            `json:"split"`
	Boundary             ResearchBoundary `json:"research_boundary"`
}

func Validate(o Observation) error {
	if o.Schema != Schema {
		return fmt.Errorf("schema must be %q", Schema)
	}
	for name, value := range map[string]string{
		"observation_id":         o.ObservationID,
		"feature_schema_version": o.FeatureSchemaVersion,
		"scenario_family":        o.ScenarioFamily,
		"worker_population":      o.WorkerPopulation,
		"time_regime":            o.TimeRegime,
		"task_class":             o.TaskClass,
		"worker_class":           o.WorkerClass,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if !o.Boundary.ShadowOnly || o.Boundary.PlacementInfluenced || o.Boundary.AuthorizationInfluenced {
		return errors.New("research boundary must remain shadow-only with no placement or authorization influence")
	}
	if o.PolicyEligible != o.Baseline.Eligible {
		return errors.New("policy eligibility must match deterministic baseline eligibility")
	}
	if o.PolicyEligible {
		if o.PolicyRejectReason != "" || o.Baseline.RejectReason != "" {
			return errors.New("eligible observation must not carry rejection reasons")
		}
	} else {
		if strings.TrimSpace(o.PolicyRejectReason) == "" || strings.TrimSpace(o.Baseline.RejectReason) == "" {
			return errors.New("ineligible observation requires policy and baseline rejection reasons")
		}
	}
	if o.Baseline.LocalInputBytes > o.Baseline.TotalInputBytes {
		return errors.New("local_input_bytes exceeds total_input_bytes")
	}
	if o.Baseline.TransferInputBytes != o.Baseline.TotalInputBytes-o.Baseline.LocalInputBytes {
		return errors.New("transfer_input_bytes is inconsistent with total/local input bytes")
	}
	if o.Baseline.TransferBytes != o.Baseline.TransferInputBytes+o.Baseline.TransferOutputBytes {
		return errors.New("transfer_bytes is inconsistent with input/output transfer bytes")
	}
	if o.Baseline.Eligible && o.Baseline.EstimatedMillis != o.Baseline.ComputeMillis+o.Baseline.TransferMillis {
		return errors.New("estimated_millis is inconsistent with compute+transfer")
	}
	if !validOutcomeStatuses[o.Outcome.Status] {
		return fmt.Errorf("invalid outcome status %q", o.Outcome.Status)
	}
	if o.Outcome.Status == "completed" {
		if o.Outcome.ActualMillis == nil {
			return errors.New("completed outcome requires actual_completion_millis")
		}
		if *o.Outcome.ActualMillis != o.Outcome.ObservedMillis {
			return errors.New("completed actual_completion_millis must equal observed_millis")
		}
	} else if o.Outcome.ActualMillis != nil {
		return errors.New("non-completed outcome must not claim actual_completion_millis")
	}
	if !validSplits[o.Split.Name] {
		return fmt.Errorf("invalid split %q", o.Split.Name)
	}
	if o.Split.ScenarioFamily != o.ScenarioFamily || o.Split.WorkerPopulation != o.WorkerPopulation || o.Split.TimeRegime != o.TimeRegime {
		return errors.New("split grouping must match observation provenance")
	}
	return nil
}

func DecodeStrict(raw []byte) (Observation, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Observation
	if err := dec.Decode(&o); err != nil {
		return Observation{}, err
	}
	if err := ensureEOF(dec); err != nil {
		return Observation{}, err
	}
	if err := Validate(o); err != nil {
		return Observation{}, err
	}
	return o, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func DurationResidualMillis(o Observation) (float64, bool) {
	if Validate(o) != nil || !o.PolicyEligible || o.Outcome.Status != "completed" || o.Outcome.ActualMillis == nil {
		return 0, false
	}
	return float64(*o.Outcome.ActualMillis) - float64(o.Baseline.EstimatedMillis), true
}

func CompletionBeforeDeadline(o Observation) (bool, bool) {
	if Validate(o) != nil || o.Outcome.DeadlineMillis == nil {
		return false, false
	}
	if o.Outcome.Status != "completed" || o.Outcome.ActualMillis == nil {
		return false, true
	}
	return *o.Outcome.ActualMillis <= *o.Outcome.DeadlineMillis, true
}

func ValidateNoSplitLeakage(values []Observation) error {
	seen := map[string]string{}
	for _, o := range values {
		if err := Validate(o); err != nil {
			return err
		}
		if o.Split.Name == "unassigned" {
			continue
		}
		groups := map[string]string{
			"scenario_family":   o.Split.ScenarioFamily,
			"worker_population": o.Split.WorkerPopulation,
			"time_regime":       o.Split.TimeRegime,
		}
		for kind, value := range groups {
			key := kind + ":" + value
			if previous, ok := seen[key]; ok && previous != o.Split.Name {
				return fmt.Errorf("split leakage for %s: %s vs %s", key, previous, o.Split.Name)
			}
			seen[key] = o.Split.Name
		}
	}
	return nil
}

func RelativeResidual(o Observation) (float64, bool) {
	residual, ok := DurationResidualMillis(o)
	if !ok || o.Baseline.EstimatedMillis == 0 {
		return 0, false
	}
	value := residual / float64(o.Baseline.EstimatedMillis)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
