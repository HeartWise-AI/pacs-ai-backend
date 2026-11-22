package modelupgrade

import (
	"context"
	"regexp"
	"strings"

	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

// ImageInspector reads the local identity of an image.
type ImageInspector interface {
	InspectImage(context.Context, string) (docker.InspectImageResult, error)
}

// RunningImage compares the image a model container actually runs with the
// image registered for it in the catalog. A container replaced outside the
// gateway (a locally built image, for example) differs from its registration.
type RunningImage struct {
	// ImageID is the local image the container was created from.
	ImageID string
	// Reference is the most readable name of that image; the ID when it has none.
	Reference string
	// CreateReference is what a new container of the same image is created
	// from: Reference while it still resolves to ImageID, otherwise ImageID.
	CreateReference string
	// Digest is the registry digest of the running image when it is the
	// registered one; a locally built image has none.
	Digest string
	// RegisteredReference and RegisteredDigest identify the catalog image.
	RegisteredReference string
	RegisteredDigest    string
	// Available is false when the running image is no longer present locally,
	// in which case a container cannot be recreated from it.
	Available bool
	// Drift is true when the running image is known to differ from the
	// registered one. It is false while Available is false.
	Drift bool
}

var bareImageIDPattern = regexp.MustCompile(`^(sha256:)?[a-f0-9]{12,64}$`)

// ResolveRunningImage inspects the image behind a running container. version
// may be nil when the catalog entry cannot be read; the registration's own
// deployment snapshot is then the only reference.
func ResolveRunningImage(
	ctx context.Context,
	inspector ImageInspector,
	container docker.GetContainerInfoResult,
	registered entity.InferenceModel,
	version *entity.InferenceModelVersion,
) RunningImage {
	result := RunningImage{ImageID: strings.TrimSpace(container.ImageID)}
	deployment := entity.InferenceModelDeployment{}
	if registered.Deployment != nil {
		deployment = *registered.Deployment
	}
	if version != nil {
		result.RegisteredReference = strings.TrimSpace(version.ImageReference)
		result.RegisteredDigest = strings.TrimSpace(version.ImageDigest)
	} else {
		result.RegisteredReference = strings.TrimSpace(deployment.ImageReference)
		result.RegisteredDigest = strings.TrimSpace(deployment.ImageDigest)
	}
	if result.RegisteredReference == "" {
		result.RegisteredReference = strings.TrimSpace(registered.DockerImage)
	}
	result.Reference, result.CreateReference = result.ImageID, result.ImageID
	if result.ImageID == "" || inspector == nil {
		return result
	}
	inspection, err := inspector.InspectImage(ctx, result.ImageID)
	if err != nil || inspection.ID != result.ImageID {
		return result
	}
	result.Available = true

	for _, repoDigest := range inspection.RepoDigests {
		if _, digest, found := strings.Cut(repoDigest, "@"); found && digest != "" && digest == result.RegisteredDigest {
			result.Digest = digest
		}
	}
	switch {
	case result.RegisteredDigest != "":
		result.Drift = result.Digest == ""
	case strings.TrimSpace(deployment.LocalImageID) != "":
		// No catalog digest to compare with: trust the registration snapshot.
		result.Drift = strings.TrimSpace(deployment.LocalImageID) != result.ImageID
	}

	// Prefer the name the container was created with, then the registration's
	// own name for this very image, then any tag of the image.
	candidates := []string{strings.TrimSpace(container.ImageReference)}
	if strings.TrimSpace(deployment.LocalImageID) == result.ImageID {
		candidates = append(candidates, strings.TrimSpace(deployment.ImageReference))
	}
	candidates = append(candidates, inspection.RepoTags...)
	for _, candidate := range candidates {
		if candidate == "" || bareImageIDPattern.MatchString(candidate) || strings.HasSuffix(candidate, ":<none>") {
			continue
		}
		result.Reference = candidate
		// A tag can be moved to another image after the container started.
		if named, namedErr := inspector.InspectImage(ctx, candidate); namedErr == nil && named.ID == result.ImageID {
			result.CreateReference = candidate
		}
		break
	}
	return result
}
