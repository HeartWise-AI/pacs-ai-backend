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
		GPUUUID:          candidate.GPUUUID,
		ContainerID:      strings.TrimSpace(candidate.ContainerID),
		CatalogVersionID: strings.TrimSpace(catalogVersionID),
		ArtifactModelID:  strings.TrimSpace(candidate.ModelInfo.ModelID),
		ImageReference:   strings.TrimSpace(candidate.ImageReference),
		ImageDigest:      strings.TrimSpace(candidate.ImageDigest),
		LocalImageID:     strings.TrimSpace(candidate.LocalImageID),
		ModelVersion:     strings.TrimSpace(candidate.ModelInfo.Version),
		ModelName:        strings.TrimSpace(candidate.ModelInfo.ModelName),
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

// DeploymentFromMovedCandidate records a container recreated from the image the
// previous container ran. The catalog reference of the registration is kept as
// it was; the image identity is the one that actually runs, which for a locally
// built image has no registry digest.
func DeploymentFromMovedCandidate(
	candidate ValidatedCandidate,
	registered entity.InferenceModel,
	running RunningImage,
	catalogVersionID string,
	deployedAt time.Time,
) (entity.InferenceModelDeployment, error) {
	previous := entity.InferenceModelDeployment{}
	if registered.Deployment != nil {
		previous = *registered.Deployment
	}
	if strings.TrimSpace(candidate.ContainerID) == "" || strings.TrimSpace(running.ImageID) == "" ||
		candidate.LocalImageID != running.ImageID || strings.TrimSpace(candidate.ModelInfo.ModelID) == "" ||
		strings.TrimSpace(candidate.ModelInfo.Version) == "" {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: moved candidate identity is incomplete", ErrCandidateContract)
	}
	if deployedAt.IsZero() {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: deployment time is required", ErrCandidateContract)
	}
	deployment := entity.InferenceModelDeployment{
		GPUUUID:          candidate.GPUUUID,
		ContainerID:      strings.TrimSpace(candidate.ContainerID),
		CatalogVersionID: strings.TrimSpace(previous.CatalogVersionID),
		ArtifactModelID:  strings.TrimSpace(candidate.ModelInfo.ModelID),
		ImageReference:   strings.TrimSpace(running.Reference),
		ImageDigest:      strings.TrimSpace(running.Digest),
		LocalImageID:     strings.TrimSpace(running.ImageID),
		ModelVersion:     strings.TrimSpace(candidate.ModelInfo.Version),
		ModelName:        strings.TrimSpace(candidate.ModelInfo.ModelName),
		Provenance:       previous.Provenance,
		DeployedAt:       deployedAt.Unix(),
	}
	if deployment.CatalogVersionID == "" {
		deployment.CatalogVersionID = strings.TrimSpace(catalogVersionID)
	}
	// The registration already describes this very image: keep its wording.
	if strings.TrimSpace(previous.LocalImageID) == running.ImageID {
		if reference := strings.TrimSpace(previous.ImageReference); reference != "" {
			deployment.ImageReference = reference
		}
		if deployment.ImageDigest == "" {
			deployment.ImageDigest = strings.TrimSpace(previous.ImageDigest)
		}
	}
	if provenance := candidate.ModelInfo.Provenance; provenance != nil && provenance.Validate() == nil && provenance.SourceRevision != nil {
		revision := *provenance.SourceRevision
		deployment.Provenance = &entity.InferenceModelProvenance{
			SourceRepository: provenance.SourceRepository,
			SourceRevision:   &revision,
			ModelRepository:  provenance.ModelRepository,
			ModelRevision:    provenance.ModelRevision,
			WeightsPath:      provenance.WeightsPath,
			WeightsSHA256:    provenance.WeightsSHA256,
		}
	}
	if deployment.CatalogVersionID == "" || deployment.ImageReference == "" || deployment.Provenance == nil {
		return entity.InferenceModelDeployment{}, fmt.Errorf("%w: moved candidate identity is incomplete", ErrCandidateContract)
	}
	return deployment, nil
}
