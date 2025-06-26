import base64
import numpy as np
import plotly.graph_objects as go
from plotly.colors import qualitative
from typing import Dict, List

def decode_labelmap(encoded_data: str, dimensions: List[int]) -> np.ndarray:
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

def create_colormap(segments: Dict[str, int]) -> Dict[int, str]:
    """
    Create a colormap for different segments using plotly colors.
    
    Args:
        segments: Dictionary mapping segment names to their numeric labels
        
    Returns:
        Dictionary mapping numeric labels to color strings
    """
    # Use plotly's qualitative color palette
    colors = qualitative.Plotly[:len(segments)]
    if len(segments) > len(colors):
        # If we need more colors, cycle through the palette
        colors = colors * (len(segments) // len(colors) + 1)
    
    return {label: colors[i] for i, label in enumerate(segments.values())}

def visualize_2d_segmentation(labelmap: np.ndarray, segments: Dict[str, int], output_path: str = "segmentation_2d.html") -> None:
    """
    Visualize 2D segmentation with colored overlay using plotly.
    
    Args:
        labelmap: 2D numpy array of segmentation labels
        segments: Dictionary mapping segment names to their numeric labels
        output_path: Path to save the visualization (default: segmentation_2d.html)
    """
    colormap = create_colormap(segments)
    
    # Create figure
    fig = go.Figure()
    
    # Add each segment as a separate trace
    for name, label in segments.items():
        mask = labelmap == label
        if not np.any(mask):
            continue
            
        # Get coordinates where this segment exists
        y_coords, x_coords = np.where(mask)
        
        fig.add_trace(go.Scatter(
            x=x_coords,
            y=y_coords,
            mode='markers',
            marker=dict(
                color=colormap[label],
                size=3,
                opacity=0.7
            ),
            name=name
        ))
    
    fig.update_layout(
        title="2D Segmentation Visualization",
        xaxis_title="X",
        yaxis_title="Y",
        showlegend=True,
        width=800,
        height=800
    )
    
    # Invert y-axis to match image coordinates
    fig.update_yaxis(autorange="reversed")
    
    fig.write_html(output_path)

def visualize_3d_segmentation(labelmap: np.ndarray, segments: Dict[str, int], output_path: str = "segmentation_3d.html") -> None:
    """
    Create 3D visualization of segmentation using plotly.
    
    Args:
        labelmap: 3D numpy array of segmentation labels
        segments: Dictionary mapping segment names to their numeric labels
        output_path: Path to save the visualization (default: segmentation_3d.html)
    """
    colormap = create_colormap(segments)
    
    # Create figure
    fig = go.Figure()
    
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
            
        fig.add_trace(go.Scatter3d(
            x=points[:, 0],
            y=points[:, 1],
            z=points[:, 2],
            mode='markers',
            marker=dict(
                color=colormap[label],
                size=2,
                opacity=0.6
            ),
            name=name
        ))
    
    fig.update_layout(
        title="3D Segmentation Visualization",
        scene=dict(
            xaxis_title="X",
            yaxis_title="Y",
            zaxis_title="Z"
        ),
        showlegend=True,
        width=900,
        height=700
    )
    
    fig.write_html(output_path)

def visualize_segmentation(encoded_data: str, dimensions: List[int], segments: Dict[str, int], 
                         output_path: str = None) -> None:
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
        default_path = "segmentation_2d.html"
        save_path = output_path if output_path else default_path
        visualize_2d_segmentation(labelmap[0], segments, save_path)
    else:
        # 3D visualization
        default_path = "segmentation_3d.html"
        save_path = output_path if output_path else default_path
        visualize_3d_segmentation(labelmap, segments, save_path) 