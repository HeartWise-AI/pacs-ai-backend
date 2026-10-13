package entity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

type InferenceModelVersionStatus string

const (
	InferenceModelVersionAvailable InferenceModelVersionStatus = "AVAILABLE"
	InferenceModelVersionRevoked   InferenceModelVersionStatus = "REVOKED"
)

var (
	modelArtifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	modelVersionPattern    = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
	)
	modelGitRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	modelRepositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	modelSHA256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// InferenceModelVersion is one immutable, approved model artifact. Availability
// may be revoked later without changing the identity or provenance fields.
type InferenceModelVersion struct {
	ID             string                      `firestore:"id,omitempty" json:"id"`
	ModelID        string                      `firestore:"model_id" json:"modelId"`
	Version        string                      `firestore:"version" json:"version"`
	ImageReference string                      `firestore:"image_reference" json:"imageReference"`
	ImageDigest    string                      `firestore:"image_digest" json:"imageDigest"`
	Provenance     *InferenceModelProvenance   `firestore:"provenance" json:"provenance"`
	Status         InferenceModelVersionStatus `firestore:"status" json:"status"`
	PublishedAt    int64                       `firestore:"published_at" json:"publishedAt"`
}

func (version *InferenceModelVersion) GetModelName() string { return "inference_model_versions" }

func (version *InferenceModelVersion) Normalize() {
	version.ID = strings.TrimSpace(version.ID)
	version.ModelID = strings.TrimSpace(version.ModelID)
	version.Version = strings.TrimSpace(version.Version)
	version.ImageReference = strings.TrimSpace(version.ImageReference)
	version.ImageDigest = strings.TrimSpace(version.ImageDigest)
	version.Status = InferenceModelVersionStatus(strings.ToUpper(strings.TrimSpace(string(version.Status))))
	if version.Status == "" {
		version.Status = InferenceModelVersionAvailable
	}
	if version.Provenance != nil {
		version.Provenance.SourceRepository = strings.TrimSpace(version.Provenance.SourceRepository)
		version.Provenance.ModelRepository = strings.TrimSpace(version.Provenance.ModelRepository)
		version.Provenance.ModelRevision = strings.TrimSpace(version.Provenance.ModelRevision)
		version.Provenance.WeightsPath = strings.TrimSpace(version.Provenance.WeightsPath)
		version.Provenance.WeightsSHA256 = strings.TrimSpace(version.Provenance.WeightsSHA256)
		if version.Provenance.SourceRevision != nil {
			value := strings.TrimSpace(*version.Provenance.SourceRevision)
			version.Provenance.SourceRevision = &value
		}
	}
}

func (version InferenceModelVersion) Validate() error {
	if !modelArtifactIDPattern.MatchString(version.ModelID) {
		return fmt.Errorf("modelId must be a stable identifier containing only letters, numbers, dots, underscores, and hyphens")
	}
	if !modelVersionPattern.MatchString(version.Version) {
		return fmt.Errorf("version must be a Docker-safe semantic version")
	}
	parsedReference, err := reference.ParseNormalizedNamed(version.ImageReference)
	if err != nil || reference.IsNameOnly(parsedReference) {
		return fmt.Errorf("imageReference must contain an explicit semantic-version tag")
	}
	tagged, ok := parsedReference.(reference.Tagged)
	if !ok || tagged.Tag() != version.Version {
		return fmt.Errorf("imageReference tag must match version")
	}
	parsedDigest, err := digest.Parse(version.ImageDigest)
	if err != nil || parsedDigest.Algorithm() != digest.SHA256 {
		return fmt.Errorf("imageDigest must be a valid sha256 digest")
	}
	if canonical, ok := parsedReference.(reference.Canonical); ok && canonical.Digest() != parsedDigest {
		return fmt.Errorf("imageReference and imageDigest disagree")
	}
	if version.Status != InferenceModelVersionAvailable && version.Status != InferenceModelVersionRevoked {
		return fmt.Errorf("status must be AVAILABLE or REVOKED")
	}
	if err := validateInferenceModelVersionProvenance(version.Provenance); err != nil {
		return err
	}
	return nil
}

func validateInferenceModelVersionProvenance(provenance *InferenceModelProvenance) error {
	if provenance == nil || provenance.SourceRevision == nil {
		return fmt.Errorf("complete provenance is required")
	}
	if !modelRepositoryPattern.MatchString(provenance.SourceRepository) ||
		!modelGitRevisionPattern.MatchString(*provenance.SourceRevision) ||
		!modelRepositoryPattern.MatchString(provenance.ModelRepository) ||
		!modelGitRevisionPattern.MatchString(provenance.ModelRevision) ||
		!validModelWeightsPath(provenance.WeightsPath) ||
		!modelSHA256Pattern.MatchString(provenance.WeightsSHA256) {
		return fmt.Errorf("provenance must contain valid immutable source and weights identifiers")
	}
	return nil
}

func validModelWeightsPath(value string) bool {
	return value != "" && !strings.Contains(value, `\`) && !path.IsAbs(value) &&
		path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "../")
}

// InferenceModelVersionID deterministically identifies one model/version pair.
func InferenceModelVersionID(modelID, version string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(modelID) + "\x00" + strings.TrimSpace(version)))
	return hex.EncodeToString(sum[:])
}
