package modelupgrade

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type validatedImageReference struct {
	Normalized     string
	RepositoryName string
	Tag            string
	Digest         string
}

func validateImageReference(value, expectedDigest, allowedNamespace string) (validatedImageReference, error) {
	value = strings.TrimSpace(value)
	parsed, err := reference.ParseNormalizedNamed(value)
	if err != nil || reference.IsNameOnly(parsed) {
		return validatedImageReference{}, invalid("A versioned image reference or immutable digest is required.")
	}
	allowed, allowedErr := reference.ParseNormalizedNamed(strings.TrimSuffix(strings.TrimSpace(allowedNamespace), "/") + "/allowed-image")
	if allowedErr != nil {
		return validatedImageReference{}, invalid("The configured image namespace is invalid.")
	}
	path := reference.Path(parsed)
	allowedPath := strings.TrimSuffix(reference.Path(allowed), "/allowed-image")
	if reference.Domain(parsed) != reference.Domain(allowed) || !strings.HasPrefix(path, allowedPath+"/") {
		return validatedImageReference{}, invalid("The image repository namespace is not allowed.")
	}
	result := validatedImageReference{
		Normalized:     reference.FamiliarString(parsed),
		RepositoryName: reference.TrimNamed(parsed).Name(),
	}
	if tagged, ok := parsed.(reference.Tagged); ok {
		result.Tag = tagged.Tag()
		if result.Tag == "latest" || !semanticVersionPattern.MatchString(result.Tag) {
			return validatedImageReference{}, invalid("The image tag must be an explicit Docker-safe semantic version.")
		}
	}
	if canonical, ok := parsed.(reference.Canonical); ok {
		result.Digest = canonical.Digest().String()
	}
	if result.Tag == "" && result.Digest == "" {
		return validatedImageReference{}, invalid("A versioned image tag or immutable digest is required.")
	}
	if expectedDigest != "" {
		parsedDigest, digestErr := digest.Parse(strings.TrimSpace(expectedDigest))
		if digestErr != nil || parsedDigest.Algorithm() != digest.SHA256 {
			return validatedImageReference{}, invalid("expectedDigest must be a valid sha256 digest.")
		}
		expectedDigest = parsedDigest.String()
		if result.Digest != "" && result.Digest != expectedDigest {
			return validatedImageReference{}, invalid("The image reference and expected digest disagree.")
		}
		result.Digest = expectedDigest
	}
	return result, nil
}

func resolveImageDigest(image validatedImageReference, inspection docker.InspectImageResult) (string, error) {
	for _, value := range inspection.RepoDigests {
		parsed, err := reference.ParseNormalizedNamed(value)
		if err != nil {
			continue
		}
		canonical, ok := parsed.(reference.Canonical)
		if !ok || reference.TrimNamed(parsed).Name() != image.RepositoryName {
			continue
		}
		resolved := canonical.Digest().String()
		if image.Digest != "" && image.Digest != resolved {
			continue
		}
		return resolved, nil
	}
	if image.Digest != "" {
		return "", fmt.Errorf("pulled image does not expose the expected repository digest")
	}
	return "", fmt.Errorf("pulled image has no resolvable repository digest")
}

func validateCandidateIdentity(
	image validatedImageReference,
	resolvedDigest string,
	inspection docker.InspectImageResult,
	active api.ModelInfo,
	candidate api.ModelInfo,
	requiredOutputMode entity.OutputMode,
) (*entity.InferenceModelProvenance, error) {
	if candidate.ModelID == "" || candidate.ModelID != active.ModelID {
		return nil, fmt.Errorf("candidate model identity does not match the active registration")
	}
	if candidate.Provenance == nil || candidate.Provenance.SourceRevision == nil {
		return nil, fmt.Errorf("candidate runtime provenance is incomplete")
	}
	if err := candidate.Resources.Validate(); err != nil {
		return nil, fmt.Errorf("candidate resource contract is invalid: %w", err)
	}
	if requiredOutputMode != "" && !slices.Contains(candidate.SupportedOutputModes, string(requiredOutputMode)) {
		return nil, fmt.Errorf("candidate does not support the registered output mode %s", requiredOutputMode)
	}
	if err := candidate.Provenance.Validate(); err != nil {
		return nil, err
	}
	if image.Tag != "" && candidate.Version != image.Tag {
		return nil, fmt.Errorf("candidate model version does not match the requested image tag")
	}
	if !newerSemanticVersion(active.Version, candidate.Version) {
		return nil, fmt.Errorf("candidate model version must be newer than the active version")
	}
	expectedLabels := map[string]string{
		"org.opencontainers.image.version":  candidate.Version,
		"org.opencontainers.image.revision": *candidate.Provenance.SourceRevision,
		"org.opencontainers.image.source":   "https://github.com/" + candidate.Provenance.SourceRepository,
		"ai.heartwise.model.repository":     candidate.Provenance.ModelRepository,
		"ai.heartwise.model.revision":       candidate.Provenance.ModelRevision,
		"ai.heartwise.model.weights.path":   candidate.Provenance.WeightsPath,
		"ai.heartwise.model.weights.sha256": candidate.Provenance.WeightsSHA256,
	}
	for label, expected := range expectedLabels {
		if inspection.Labels[label] != expected {
			return nil, fmt.Errorf("candidate OCI label %s does not match runtime metadata", label)
		}
	}
	if resolvedDigest == "" || inspection.ID == "" {
		return nil, fmt.Errorf("candidate image identity is incomplete")
	}
	return &entity.InferenceModelProvenance{
		SourceRepository: candidate.Provenance.SourceRepository,
		SourceRevision:   *candidate.Provenance.SourceRevision,
		ModelRepository:  candidate.Provenance.ModelRepository,
		ModelRevision:    candidate.Provenance.ModelRevision,
		WeightsPath:      candidate.Provenance.WeightsPath,
		WeightsSHA256:    candidate.Provenance.WeightsSHA256,
	}, nil
}

func newerSemanticVersion(active, candidate string) bool {
	activeMatch := semanticVersionPattern.FindStringSubmatch(active)
	candidateMatch := semanticVersionPattern.FindStringSubmatch(candidate)
	if activeMatch == nil || candidateMatch == nil {
		return false
	}
	for index := 1; index <= 3; index++ {
		left, _ := strconv.Atoi(activeMatch[index])
		right, _ := strconv.Atoi(candidateMatch[index])
		if right != left {
			return right > left
		}
	}
	return comparePrerelease(activeMatch[4], candidateMatch[4]) < 0
}

func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		if leftParts[index] == rightParts[index] {
			continue
		}
		leftNumber, leftErr := strconv.Atoi(leftParts[index])
		rightNumber, rightErr := strconv.Atoi(rightParts[index])
		switch {
		case leftErr == nil && rightErr == nil:
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		case leftErr == nil:
			return -1
		case rightErr == nil:
			return 1
		case leftParts[index] < rightParts[index]:
			return -1
		default:
			return 1
		}
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}
	return 1
}
