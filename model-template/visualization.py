import base64

import matplotlib.pyplot as plt
import numpy as np


def decode_labelmap(encoded_data: str, dimensions: list[int]) -> np.ndarray:
    """
    Decode base64 encoded labelmap data into numpy array.

    Args:
        encoded_data: Base64 encoded string of labelmap data
        dimensions: List of dimensions [height, width, depth] or [1, width, height]

    Returns:
        Numpy array of decoded labelmap data
    """
    decoded_data = base64.b64decode(encoded_data)
    labelmap = np.frombuffer(decoded_data, dtype=np.uint8)
    return labelmap.reshape(dimensions)


def create_colormap(segments: dict[str, int]) -> dict[int, tuple]:
    """
    Create a colormap for different segments.

    Args:
        segments: Dictionary mapping segment names to their numeric labels

    Returns:
        Dictionary mapping numeric labels to RGB colors
    """
    # Generate distinct colors for each segment
    num_segments = len(segments)
    colors = plt.cm.rainbow(np.linspace(0, 1, num_segments))
    return {
        label: tuple(color[:3]) for label, color in zip(segments.values(), colors, strict=False)
    }


def visualize_2d_segmentation(
    labelmap: np.ndarray, segments: dict[str, int], output_path: str = None
) -> None:
    """
    Visualize 2D segmentation with colored overlay.

    Args:
        labelmap: 2D numpy array of segmentation labels
        segments: Dictionary mapping segment names to their numeric labels
        output_path: Optional path to save the visualization
    """
    plt.figure(figsize=(10, 10))

    # Create a color overlay
    colormap = create_colormap(segments)
    overlay = np.zeros((*labelmap.shape, 3))

    # Add each segment with its color
    for label in segments.values():
        mask = labelmap == label
        for i in range(3):
            overlay[mask, i] = colormap[label][i]

    plt.imshow(overlay)

    # Add legend
    patches = [plt.Rectangle((0, 0), 1, 1, fc=colormap[label]) for label in segments.values()]
    plt.legend(patches, segments.keys(), loc="center left", bbox_to_anchor=(1, 0.5))

    if output_path:
        plt.savefig(output_path, bbox_inches="tight")
    plt.show()


def visualize_3d_segmentation(
    labelmap: np.ndarray, segments: dict[str, int], output_path: str = None
) -> None:
    """
    Create 3D visualization of segmentation using matplotlib.

    Args:
        labelmap: 3D numpy array of segmentation labels
        segments: Dictionary mapping segment names to their numeric labels
        output_path: Optional path to save the visualization
    """
    fig = plt.figure(figsize=(10, 10))
    ax = fig.add_subplot(111, projection="3d")
    colormap = create_colormap(segments)

    # Downsample factor to improve performance
    downsample = 4

    # Add points for each segment
    for name, label in segments.items():
        mask = labelmap == label
        if not np.any(mask):
            continue

        # Get coordinates of points and downsample
        points = np.array(np.where(mask)).T[::downsample]
        if len(points) == 0:
            continue

        color = colormap[label]
        ax.scatter(
            points[:, 0], points[:, 1], points[:, 2], c=[color], label=name, alpha=0.6, s=1
        )

    ax.set_xlabel("X")
    ax.set_ylabel("Y")
    ax.set_zlabel("Z")
    ax.legend()

    if output_path:
        plt.savefig(output_path, bbox_inches="tight")
    plt.show()


def visualize_segmentation(
    encoded_data: str, dimensions: list[int], segments: dict[str, int], output_path: str = None
) -> None:
    """
    Main function to visualize segmentation data based on dimensions.

    Args:
        encoded_data: Base64 encoded string of labelmap data
        dimensions: List of dimensions [height, width, depth] or [1, width, height]
        segments: Dictionary mapping segment names to their numeric labels
        output_path: Optional path to save the visualization
    """
    # Decode labelmap
    labelmap = decode_labelmap(encoded_data, dimensions)

    # Determine visualization type based on dimensions
    if dimensions[0] == 1:
        # 2D visualization
        visualize_2d_segmentation(labelmap[0], segments, output_path)
    else:
        # 3D visualization
        visualize_3d_segmentation(labelmap, segments, output_path)
