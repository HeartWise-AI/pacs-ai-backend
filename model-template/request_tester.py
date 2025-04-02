import pydicom
import requests
import numpy as np
from typing import List, Union
from pathlib import Path
import argparse
import sys
import webbrowser
import base64
import tempfile
import os
import random
from visualization import visualize_segmentation

from tqdm import tqdm


def collect_dicom_files(paths: List[str]) -> List[str]:
    """
    Collect DICOM files from a list of paths that can include both files and directories.
    
    Args:
        paths: List of paths to files or directories
        
    Returns:
        List of paths to DICOM files
    """
    dicom_files = []
    for path in paths:
        path = Path(path)
        if path.is_file():
            if path.suffix.lower() in ['.dcm', '.dicom']:
                dicom_files.append(str(path))
        elif path.is_dir():
            for file_path in path.rglob('*'):
                if file_path.is_file() and file_path.suffix.lower() in ['.dcm', '.dicom']:
                    dicom_files.append(str(file_path))
    return dicom_files


def display_response(response_data, output_mode):
    """
    Display HTML or PDF content in a browser window.

    Args:
        response_data: Server response containing base64 encoded content
        output_mode: Type of content ('HTML' or 'PDF')
    """
    try:
        # Extract the base64 content based on output mode
        if output_mode == 'HTML':
            base64_content = response_data['data']['htmlBase64']
            file_extension = '.html'
            content = base64.b64decode(base64_content).decode('utf-8')
        elif output_mode == 'PDF':
            base64_content = response_data['data']['pdfBase64']
            file_extension = '.pdf'
            content = base64.b64decode(base64_content)
        else:
            print(f"Unsupported output mode for display: {output_mode}")
            return

        # Create a temporary file
        with tempfile.NamedTemporaryFile(delete=False, suffix=file_extension, mode='wb') as tmp_file:
            if output_mode == 'HTML':
                tmp_file.write(content.encode('utf-8'))
            else:  # PDF
                tmp_file.write(content)
            temp_path = tmp_file.name

        # Open the file in the default web browser
        webbrowser.open('file://' + os.path.realpath(temp_path))

    except Exception as e:
        print(f"Error displaying content: {str(e)}")


def send_dicom_data(dicom_paths: Union[str, List[str]], server_url: str, output_mode: str = "JSON", send_metadata_only: bool = False, group_series: bool = False):
    """
    Read DICOM file(s), process the data, and send a POST request to the server.

    Args:
        dicom_paths: Path to a single DICOM file or list of paths to multiple DICOM files
        server_url: URL of the server
        output_mode: Output mode for the request (default: JSON)
        send_metadata_only: If True, sends only DICOM metadata without pixel data (default: False)
        group_series: If True, treats all DICOM files as part of the same series (default: False)
    """

    # Convert single path to list for consistent processing
    if isinstance(dicom_paths, str):
        dicom_paths = [dicom_paths]
    
    # Dictionary to store series instance metadata
    series_instance_metadata = {}
    series_instance_images = {} if not send_metadata_only else None
    
    # Process each DICOM file
    for idx, dicom_path in tqdm(enumerate(dicom_paths, start=1), total=len(dicom_paths), desc="Processing DICOM files"):
        try:
            # Read DICOM file
            ds = pydicom.dcmread(dicom_path)

            # Get series number - use 1 for all files if group_series is True, otherwise use idx
            series_number = 1 if group_series else idx
            
            # Create series entry if it doesn't exist
            if series_number not in series_instance_metadata:
                series_instance_metadata[series_number] = {}
                if not send_metadata_only and series_instance_images is not None:
                    series_instance_images[series_number] = {}

            # Generate instance number (using format from example)
            instance_number = str(random.randint(1000000000, 9999999999))

            # Extract pixel data and convert to base64 for metadata
            pixel_array = ds.pixel_array
            pixel_bytes = pixel_array.tobytes()
            pixel_base64 = base64.b64encode(pixel_bytes).decode('utf-8')

            # Read the entire DICOM file as bytes and convert to base64 for images
            if not send_metadata_only and series_instance_images is not None:
                with open(dicom_path, 'rb') as f:
                    dicom_bytes = f.read()
                    dicom_base64 = base64.b64encode(dicom_bytes).decode('utf-8')

            # Extract DICOM metadata for this instance
            instance_metadata = {
                "00280008": {
                    "Value": [getattr(ds, 'NumberOfFrames', 1)],
                    "vr": "IS"
                },
                "00280002": {
                    "Value": [getattr(ds, 'SamplesPerPixel', 1)],
                    "vr": "US"
                },
                "00280010": {
                    "Value": [getattr(ds, 'Rows', 0)],
                    "vr": "US"
                },
                "00280011": {
                    "Value": [getattr(ds, 'Columns', 0)],
                    "vr": "US"
                },
                "00280100": {
                    "Value": [getattr(ds, 'BitsAllocated', 8)],
                    "vr": "US"
                },
                "00280101": {
                    "Value": [getattr(ds, 'BitsStored', 8)],
                    "vr": "US"
                },
                "00101010": {
                    "Value": [getattr(ds, 'PatientAge', '29')],
                    "vr": "AS"
                },
                "7FE00010": {
                    "InlineBinary": pixel_base64,
                    "vr": "OB"
                }
            }
            
            # Add instance metadata to series
            series_instance_metadata[series_number][instance_number] = instance_metadata

            # Add full DICOM file as base64 if not sending metadata only
            if not send_metadata_only and series_instance_images is not None:
                series_instance_images[series_number][instance_number] = dicom_base64

        except Exception as e:
            print(f"Error processing DICOM file {dicom_path}: {str(e)}")
            continue

    # Prepare request payload
    payload = {
        "additionalMetadata": {
            "smoker": "false"  # Default value, can be modified as needed
        },
        "outputMode": output_mode
    }

    # Add either metadata or images based on send_metadata_only flag
    if send_metadata_only:
        payload["seriesInstanceMetadata"] = series_instance_metadata
    else:
        payload["seriesInstanceImages"] = series_instance_images

    # Send POST request
    try:
        response = requests.post(server_url, json=payload)
        response.raise_for_status()
        print(f"Request sent successfully. Status code: {response.status_code}")
        return response.json()
    except requests.exceptions.RequestException as e:
        print(f"Error sending request: {str(e)}")
        return None

def main():
    parser = argparse.ArgumentParser(description='Send DICOM data to server via POST request.')

    parser.add_argument(
        'dicom_paths',
        default=['/home/denis/Documents/GitHub/pacs-ai-backend/model-template/sample_data/US_Study_Full'],
        nargs='*',
        help='Path(s) to DICOM file(s) or directories containing DICOM files.'
    )

    parser.add_argument(
        '--url',
        default='http://localhost:8090/inference/predict',
        help='Server URL (default: http://localhost:8000/inference/predict)'
    )

    parser.add_argument(
        '--output_mode',
        default='HTML',
        choices=['HTML','OHIF_ANNOTATIONS','JSON','WEB_APP','PDF'],
        help='Output mode for the request (default: HTML)'
    )

    parser.add_argument(
        '--metadata-only',
        default=True,
        action='store_true',
        help='Send only DICOM metadata without separate pixel data'
    )

    parser.add_argument(
        '--group-series',
        default=True,
        action='store_true',
        help='Treat all DICOM files as part of the same series'
    )

    args = parser.parse_args()

    # Collect all DICOM files from the provided paths
    dicom_files = collect_dicom_files(args.dicom_paths)
    
    if not dicom_files:
        print("Error: No DICOM files found in the specified paths")
        sys.exit(1)
    
    print(f"Found {len(dicom_files)} DICOM files")

    # Send the request
    result = send_dicom_data(
        dicom_paths=dicom_files,
        server_url=args.url,
        output_mode=args.output_mode,
        send_metadata_only=args.metadata_only,
        group_series=args.group_series
    )

    if result:
        print("Server response received")

        # Display content if it's HTML or PDF
        if args.output_mode in ['HTML', 'PDF']:
            display_response(result, args.output_mode)
            return
        
        if args.output_mode == 'OHIF_ANNOTATIONS':
            payload = result['data']
            visualize_segmentation(
                    encoded_data=payload['segmentation']['labelmap'],
                    dimensions=payload['segmentation']['dimensions'],
                    segments=payload['segmentation']['segments']
                )
            return
        print(result)
        # result will be an HTML string containing an interactive 3D visualization


if __name__ == "__main__":
    main()
    # Example usage: python request_tester.py --url http://localhost:8000/inference/predict --output-mode CSV file1.dcm file2.dcm
