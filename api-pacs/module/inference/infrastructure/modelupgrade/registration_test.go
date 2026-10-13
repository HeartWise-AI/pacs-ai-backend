package modelupgrade

import (
	"errors"
	"testing"
	"time"
)

func TestDeploymentFromCandidatePreservesValidatedIdentity(t *testing.T) {
	candidate := ValidatedCandidate{
		ContainerID:    testCandidateID,
		ImageReference: "heartwise/model:2.0.0",
		ImageDigest:    testDigest,
		LocalImageID:   testImageID,
		ModelInfo:      validCandidateModelInfo(),
	}
	deployedAt := time.Unix(42, 987)

	deployment, err := DeploymentFromCandidate(candidate, "catalog-version-2", deployedAt)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ContainerID != candidate.ContainerID ||
		deployment.CatalogVersionID != "catalog-version-2" ||
		deployment.ArtifactModelID != candidate.ModelInfo.ModelID ||
		deployment.ImageReference != candidate.ImageReference ||
		deployment.ImageDigest != candidate.ImageDigest ||
		deployment.LocalImageID != candidate.LocalImageID ||
		deployment.ModelVersion != candidate.ModelInfo.Version ||
		deployment.DeployedAt != deployedAt.Unix() || deployment.Provenance == nil {
		t.Fatalf("candidate identity was not preserved: %+v", deployment)
	}
	if deployment.Provenance.WeightsSHA256 != candidate.ModelInfo.Provenance.WeightsSHA256 {
		t.Fatalf("candidate provenance was not preserved: %+v", deployment.Provenance)
	}

	*deployment.Provenance.SourceRevision = "0000000000000000000000000000000000000000"
	if *candidate.ModelInfo.Provenance.SourceRevision == *deployment.Provenance.SourceRevision {
		t.Fatal("persisted provenance aliases mutable candidate memory")
	}
}

func TestDeploymentFromCandidateRejectsIncompleteIdentity(t *testing.T) {
	candidate := ValidatedCandidate{
		ContainerID:    testCandidateID,
		ImageReference: "heartwise/model:2.0.0",
		ImageDigest:    testDigest,
		LocalImageID:   testImageID,
		ModelInfo:      validCandidateModelInfo(),
	}
	candidate.ModelInfo.Provenance = nil
	if _, err := DeploymentFromCandidate(candidate, "catalog-version-2", time.Now()); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("incomplete candidate returned %v", err)
	}
	if _, err := DeploymentFromCandidate(ValidatedCandidate{}, "catalog-version-2", time.Now()); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("empty candidate returned %v", err)
	}
	candidate.ModelInfo = validCandidateModelInfo()
	if _, err := DeploymentFromCandidate(candidate, "catalog-version-2", time.Time{}); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("zero deployment time returned %v", err)
	}
	if _, err := DeploymentFromCandidate(candidate, "", time.Now()); !errors.Is(err, ErrCandidateContract) {
		t.Fatalf("missing catalog version returned %v", err)
	}
}
