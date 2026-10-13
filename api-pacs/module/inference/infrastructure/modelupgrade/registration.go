package modelupgrade

import (
	"fmt"
	"strings"
	"time"

	"api-pacs/module/inference/domain/entity"
)

// DeploymentFromCandidate creates the durable registration identity from the
// exact candidate that passed image and runtime validation.
func DeploymentFromCandidate(
	candidate ValidatedCandidate,
	catalogVersionID string,
	deployedAt time.Time,
) (entity.InferenceModelDeployment, error) {
	provenance := candidate.ModelInfo.Provenance
	if strings.TrimSpace(candidate.ContainerID) == "" ||
		strings.TrimSpace(catalogVersionID) == "" ||
		strings.TrimSpace(candidate.ModelInfo.ModelID) == "" ||
		strings.TrimSpace(candidate.ImageReference) == "" ||
		strings.TrimSpace(candidate.ImageDigest) == "" ||
		strings.TrimSpace(candidate.LocalImageID) == "" ||
		strings.TrimSpace(candidate.ModelInfo.Version) == "" || provenance == nil || provenance.SourceRevision == nil {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: validated candidate identity is incomplete", ErrCandidateContract)
	}
	if deployedAt.IsZero() {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: deployment time is required", ErrCandidateContract)
	}
	if err := provenance.Validate(); err != nil {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: provenance: %v", ErrCandidateContract, err)
	}

	var sourceRevision *string
	if provenance.SourceRevision != nil {
		value := *provenance.SourceRevision
		sourceRevision = &value
	}
	return entity.InferenceModelDeployment{
		ContainerID:      strings.TrimSpace(candidate.ContainerID),
		CatalogVersionID: strings.TrimSpace(catalogVersionID),
		ArtifactModelID:  strings.TrimSpace(candidate.ModelInfo.ModelID),
		ImageReference:   strings.TrimSpace(candidate.ImageReference),
		ImageDigest:      strings.TrimSpace(candidate.ImageDigest),
		LocalImageID:     strings.TrimSpace(candidate.LocalImageID),
		ModelVersion:     strings.TrimSpace(candidate.ModelInfo.Version),
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: provenance.SourceRepository,
			SourceRevision:   sourceRevision,
			ModelRepository:  provenance.ModelRepository,
			ModelRevision:    provenance.ModelRevision,
			WeightsPath:      provenance.WeightsPath,
			WeightsSHA256:    provenance.WeightsSHA256,
		},
		DeployedAt: deployedAt.Unix(),
	}, nil
}
