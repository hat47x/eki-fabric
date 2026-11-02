// Package teiprojection reads a TEI task request projection artifact.
//
// The artifact is produced outside EKI. This package only parses the envelope
// and the flat task request header; it does not call eki-control or admit tasks.
package teiprojection

import (
	"encoding/json"
	"errors"
	"fmt"
)

const ArtifactSchema = "tai.reference.tei-eki-task-request-projection/v0"

// Artifact is the projection envelope.
type Artifact struct {
	Schema      string      `json:"schema"`
	Contract    Contract    `json:"contract"`
	Source      Source      `json:"source"`
	TaskRequest TaskRequest `json:"taskRequest"`
}

// Contract identifies the projection contract.
type Contract struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Source records where the projection came from.
type Source struct {
	TEICommit string         `json:"teiCommit"`
	Contract  SourceContract `json:"contract"`
}

// SourceContract records the generator contract metadata.
type SourceContract struct {
	ContractID      string `json:"contractId"`
	ContractVersion string `json:"contractVersion"`
	ContractDigest  string `json:"contractDigest"`
	SourceDigest    string `json:"sourceDigest"`
	Cases           int    `json:"cases"`
}

// TaskRequest is the flat projection of a TaskRequest header.
type TaskRequest struct {
	Schema            string `json:"schema"`
	ExecutorID        string `json:"executorId"`
	VersionConstraint string `json:"versionConstraint"`
	Priority          string `json:"priority,omitempty"`
	ClientRequestID   string `json:"clientRequestId,omitempty"`
}

// Parse decodes and checks a task request projection artifact.
func Parse(data []byte) (Artifact, error) {
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode projection: %w", err)
	}
	if artifact.Schema != ArtifactSchema {
		return Artifact{}, fmt.Errorf("unexpected schema %q", artifact.Schema)
	}
	if artifact.Contract.ID == "" || artifact.Contract.Version == "" {
		return Artifact{}, errors.New("contract id and version are required")
	}
	if artifact.Source.TEICommit == "" {
		return Artifact{}, errors.New("source teiCommit is required")
	}
	if artifact.TaskRequest.Schema != "eki.task-request/v1" {
		return Artifact{}, fmt.Errorf("unexpected task request schema %q", artifact.TaskRequest.Schema)
	}
	if artifact.TaskRequest.ExecutorID == "" || artifact.TaskRequest.VersionConstraint == "" {
		return Artifact{}, errors.New("executorId and versionConstraint are required")
	}
	return artifact, nil
}
