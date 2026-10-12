package modelupgrade

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/containerready"
)

var (
	ErrCandidateImageReference = errors.New("invalid candidate image reference")
	ErrCandidateImageIdentity  = errors.New("invalid candidate image identity")
	ErrCandidateReadiness      = errors.New("candidate is not ready")
	ErrCandidateContract       = errors.New("invalid candidate contract")
)

var semanticVersionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

type CandidateValidationConfig struct {
	AllowedImageNamespace string
	ReadinessTimeout      time.Duration
	PollInterval          time.Duration
}

func (config CandidateValidationConfig) Validate() error {
	namespace := strings.TrimSuffix(strings.TrimSpace(config.AllowedImageNamespace), "/")
	if namespace == "" {
		return fmt.Errorf("candidate image namespace is required")
	}
	if _, err := reference.ParseNormalizedNamed(namespace + "/allowed-image"); err != nil {
		return fmt.Errorf("candidate image namespace is invalid: %w", err)
	}
	if config.ReadinessTimeout <= 0 {
		return fmt.Errorf("candidate readiness timeout must be positive")
	}
	if config.PollInterval <= 0 || config.PollInterval > config.ReadinessTimeout {
		return fmt.Errorf("candidate poll interval must be positive and no greater than the readiness timeout")
	}
	return nil
}

type CandidateDocker interface {
	containerready.Docker
	InspectImage(context.Context, string) (docker.InspectImageResult, error)
}

type CandidateAPI interface {
	containerready.Metadata
	GetModelRuntime(context.Context, string) (api.ModelRuntime, error)
}

type CandidateValidator struct {
	docker CandidateDocker
	api    CandidateAPI
	config CandidateValidationConfig
}

func NewCandidateValidator(
	docker CandidateDocker,
	api CandidateAPI,
	config CandidateValidationConfig,
) (*CandidateValidator, error) {
	if docker == nil || api == nil {
		return nil, fmt.Errorf("candidate Docker and inference API dependencies are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config.AllowedImageNamespace = strings.TrimSuffix(strings.TrimSpace(config.AllowedImageNamespace), "/")
	return &CandidateValidator{docker: docker, api: api, config: config}, nil
}

type candidateImageReference struct {
	normalized     string
	repositoryName string
	tag            string
	digest         string
}

// ResolvedCandidateImage pins a requested registry reference to the exact
// local image and repository digest that must be used to create the candidate.
type ResolvedCandidateImage struct {
	Reference    string
	Repository   string
	Tag          string
	Digest       string
	LocalImageID string

	labels map[string]string
}

// ValidatedCandidate contains the immutable image identity and live runtime
// contract that passed validation. The candidate remains outside active routing.
type ValidatedCandidate struct {
	ContainerID    string
	ContainerName  string
	ImageReference string
	ImageDigest    string
	LocalImageID   string
	ModelInfo      api.ModelInfo
	Runtime        api.ModelRuntime
}

type ValidateCandidateContainer struct {
	ContainerID          string
	Image                ResolvedCandidateImage
	ActiveModelInfo      api.ModelInfo
	RegisteredOutputMode entity.OutputMode
}

// ValidateImageReference rejects mutable, unversioned, or out-of-namespace
// references before the upgrade workflow contacts a registry.
func (validator *CandidateValidator) ValidateImageReference(value, expectedDigest string) (string, error) {
	image, err := validateImageReference(value, expectedDigest, validator.config.AllowedImageNamespace)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrCandidateImageReference, err)
	}
	return image.normalized, nil
}

// ResolveImage verifies the already-pulled image and returns the immutable
// local image ID that should be passed to CreateCandidateContainer.
func (validator *CandidateValidator) ResolveImage(
	ctx context.Context,
	value string,
	expectedDigest string,
) (ResolvedCandidateImage, error) {
	image, err := validateImageReference(value, expectedDigest, validator.config.AllowedImageNamespace)
	if err != nil {
		return ResolvedCandidateImage{}, fmt.Errorf("%w: %v", ErrCandidateImageReference, err)
	}

	inspection, err := validator.docker.InspectImage(ctx, image.normalized)
	if err != nil {
		return ResolvedCandidateImage{}, fmt.Errorf("%w: inspect image: %v", ErrCandidateImageIdentity, err)
	}
	localID, err := digest.Parse(inspection.ID)
	if err != nil || localID.Algorithm() != digest.SHA256 {
		return ResolvedCandidateImage{}, fmt.Errorf("%w: local image ID must be a sha256 digest", ErrCandidateImageIdentity)
	}
	resolvedDigest, err := resolveImageDigest(image, inspection)
	if err != nil {
		return ResolvedCandidateImage{}, fmt.Errorf("%w: %v", ErrCandidateImageIdentity, err)
	}

	return ResolvedCandidateImage{
		Reference:    image.normalized,
		Repository:   image.repositoryName,
		Tag:          image.tag,
		Digest:       resolvedDigest,
		LocalImageID: localID.String(),
		labels:       cloneLabels(inspection.Labels),
	}, nil
}

// ValidateContainer starts a stopped candidate, waits for Docker and HTTP
// readiness, and validates its runtime and release contracts. Cleanup remains
// the caller's responsibility on both success and failure.
func (validator *CandidateValidator) ValidateContainer(
	ctx context.Context,
	request ValidateCandidateContainer,
) (ValidatedCandidate, error) {
	if strings.TrimSpace(request.ContainerID) == "" {
		return ValidatedCandidate{}, fmt.Errorf("%w: candidate container ID is required", ErrCandidateContract)
	}
	if request.Image.LocalImageID == "" || request.Image.Digest == "" || request.Image.Reference == "" {
		return ValidatedCandidate{}, fmt.Errorf("%w: resolved image identity is incomplete", ErrCandidateImageIdentity)
	}

	validationCtx, cancel := context.WithTimeout(ctx, validator.config.ReadinessTimeout)
	defer cancel()
	containerInfo, err := validator.docker.GetContainerInfo(validationCtx, request.ContainerID)
	if err != nil {
		return ValidatedCandidate{}, fmt.Errorf("%w: inspect candidate container: %w", ErrCandidateReadiness, err)
	}
	if containerInfo.ImageID == "" || containerInfo.ImageID != request.Image.LocalImageID {
		return ValidatedCandidate{}, fmt.Errorf(
			"%w: candidate container image does not match the resolved local image ID",
			ErrCandidateImageIdentity,
		)
	}
	name, modelInfo, err := containerready.Ensure(
		validationCtx,
		validator.docker,
		validator.api,
		request.ContainerID,
		validator.config.ReadinessTimeout,
		validator.config.PollInterval,
	)
	if err != nil {
		return ValidatedCandidate{}, fmt.Errorf("%w: %w", ErrCandidateReadiness, err)
	}

	runtime, err := validator.api.GetModelRuntime(validationCtx, name)
	if err != nil {
		return ValidatedCandidate{}, fmt.Errorf("%w: model runtime: %w", ErrCandidateReadiness, err)
	}
	if err = validateCandidateRuntime(runtime); err != nil {
		return ValidatedCandidate{}, fmt.Errorf("%w: %w", ErrCandidateContract, err)
	}
	if err = validateCandidateIdentity(
		request.Image,
		request.ActiveModelInfo,
		modelInfo,
		request.RegisteredOutputMode,
	); err != nil {
		return ValidatedCandidate{}, fmt.Errorf("%w: %w", ErrCandidateContract, err)
	}

	return ValidatedCandidate{
		ContainerID:    request.ContainerID,
		ContainerName:  name,
		ImageReference: request.Image.Reference,
		ImageDigest:    request.Image.Digest,
		LocalImageID:   request.Image.LocalImageID,
		ModelInfo:      modelInfo,
		Runtime:        runtime,
	}, nil
}

func validateImageReference(value, expectedDigest, allowedNamespace string) (candidateImageReference, error) {
	value = strings.TrimSpace(value)
	parsed, err := reference.ParseNormalizedNamed(value)
	if err != nil || reference.IsNameOnly(parsed) {
		return candidateImageReference{}, fmt.Errorf("a versioned image reference or immutable digest is required")
	}

	allowed, err := reference.ParseNormalizedNamed(strings.TrimSuffix(strings.TrimSpace(allowedNamespace), "/") + "/allowed-image")
	if err != nil {
		return candidateImageReference{}, fmt.Errorf("configured image namespace is invalid")
	}
	allowedPath := strings.TrimSuffix(reference.Path(allowed), "/allowed-image")
	if reference.Domain(parsed) != reference.Domain(allowed) ||
		!strings.HasPrefix(reference.Path(parsed), allowedPath+"/") {
		return candidateImageReference{}, fmt.Errorf("image repository namespace is not allowed")
	}

	result := candidateImageReference{
		normalized:     reference.FamiliarString(parsed),
		repositoryName: reference.TrimNamed(parsed).Name(),
	}
	if tagged, ok := parsed.(reference.Tagged); ok {
		result.tag = tagged.Tag()
		if _, valid := parseSemanticVersion(result.tag); !valid {
			return candidateImageReference{}, fmt.Errorf("image tag must be an explicit Docker-safe semantic version")
		}
	}
	if canonical, ok := parsed.(reference.Canonical); ok {
		if canonical.Digest().Algorithm() != digest.SHA256 {
			return candidateImageReference{}, fmt.Errorf("image digest must use sha256")
		}
		result.digest = canonical.Digest().String()
	}
	if result.tag == "" && result.digest == "" {
		return candidateImageReference{}, fmt.Errorf("a versioned image tag or immutable digest is required")
	}

	if strings.TrimSpace(expectedDigest) != "" {
		expected, parseErr := digest.Parse(strings.TrimSpace(expectedDigest))
		if parseErr != nil || expected.Algorithm() != digest.SHA256 {
			return candidateImageReference{}, fmt.Errorf("expected digest must be a valid sha256 digest")
		}
		if result.digest != "" && result.digest != expected.String() {
			return candidateImageReference{}, fmt.Errorf("image reference and expected digest disagree")
		}
		result.digest = expected.String()
	}
	return result, nil
}

func resolveImageDigest(image candidateImageReference, inspection docker.InspectImageResult) (string, error) {
	for _, value := range inspection.RepoDigests {
		parsed, err := reference.ParseNormalizedNamed(value)
		if err != nil {
			continue
		}
		canonical, ok := parsed.(reference.Canonical)
		if !ok || reference.TrimNamed(parsed).Name() != image.repositoryName {
			continue
		}
		if canonical.Digest().Algorithm() != digest.SHA256 {
			continue
		}
		resolved := canonical.Digest().String()
		if image.digest != "" && image.digest != resolved {
			continue
		}
		return resolved, nil
	}
	if image.digest != "" {
		return "", fmt.Errorf("pulled image does not expose the expected repository digest")
	}
	return "", fmt.Errorf("pulled image has no repository digest for the requested repository")
}

func validateCandidateRuntime(runtime api.ModelRuntime) error {
	if err := runtime.Validate(); err != nil {
		return fmt.Errorf("candidate runtime contract is invalid: %w", err)
	}
	if runtime.ActiveRequests != 0 {
		return fmt.Errorf("candidate must not have active inference requests")
	}
	if runtime.Error != "" {
		return fmt.Errorf("candidate runtime reports an error")
	}
	if runtime.State != api.RuntimeUnloaded {
		return fmt.Errorf("candidate runtime state %q must be UNLOADED before activation", runtime.State)
	}
	return nil
}

func validateCandidateIdentity(
	image ResolvedCandidateImage,
	active api.ModelInfo,
	candidate api.ModelInfo,
	requiredOutputMode entity.OutputMode,
) error {
	if strings.TrimSpace(candidate.ModelID) == "" || candidate.ModelID != active.ModelID {
		return fmt.Errorf("candidate model identity does not match the active model")
	}
	if candidate.Provenance == nil || candidate.Provenance.SourceRevision == nil {
		return fmt.Errorf("candidate provenance must be complete and immutable")
	}
	if err := candidate.Provenance.Validate(); err != nil {
		return fmt.Errorf("candidate provenance is invalid: %w", err)
	}
	if err := candidate.Resources.Validate(); err != nil {
		return fmt.Errorf("candidate resource contract is invalid: %w", err)
	}
	if requiredOutputMode != "" && !slices.Contains(candidate.SupportedOutputModes, string(requiredOutputMode)) {
		return fmt.Errorf("candidate does not support registered output mode %s", requiredOutputMode)
	}
	if image.Tag != "" && candidate.Version != image.Tag {
		return fmt.Errorf("candidate version does not match requested image tag")
	}
	if !newerSemanticVersion(active.Version, candidate.Version) {
		return fmt.Errorf("candidate version must be newer than active version %s", active.Version)
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
		if image.labels[label] != expected {
			return fmt.Errorf("candidate OCI label %s does not match runtime metadata", label)
		}
	}
	return nil
}

type semanticVersion struct {
	major      string
	minor      string
	patch      string
	prerelease []string
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	match := semanticVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return semanticVersion{}, false
	}
	version := semanticVersion{major: match[1], minor: match[2], patch: match[3]}
	if match[4] == "" {
		return version, true
	}
	version.prerelease = strings.Split(match[4], ".")
	for _, identifier := range version.prerelease {
		if isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
			return semanticVersion{}, false
		}
	}
	return version, true
}

func newerSemanticVersion(active, candidate string) bool {
	left, leftOK := parseSemanticVersion(active)
	right, rightOK := parseSemanticVersion(candidate)
	if !leftOK || !rightOK {
		return false
	}
	if comparison := compareNumericIdentifier(left.major, right.major); comparison != 0 {
		return comparison < 0
	}
	if comparison := compareNumericIdentifier(left.minor, right.minor); comparison != 0 {
		return comparison < 0
	}
	if comparison := compareNumericIdentifier(left.patch, right.patch); comparison != 0 {
		return comparison < 0
	}
	return comparePrerelease(left.prerelease, right.prerelease) < 0
}

func comparePrerelease(left, right []string) int {
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	if len(left) == 0 {
		return 1
	}
	if len(right) == 0 {
		return -1
	}
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] == right[index] {
			continue
		}
		leftNumeric, rightNumeric := isNumeric(left[index]), isNumeric(right[index])
		switch {
		case leftNumeric && rightNumeric:
			return compareNumericIdentifier(left[index], right[index])
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case left[index] < right[index]:
			return -1
		default:
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func compareNumericIdentifier(left, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}
