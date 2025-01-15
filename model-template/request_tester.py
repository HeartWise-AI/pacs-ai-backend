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


def send_dicom_data(dicom_paths: Union[str, List[str]], server_url: str, output_mode: str = "JSON"):
    """
    Read DICOM file(s), process the data, and send a POST request to the server.

    Args:
        dicom_paths: Path to a single DICOM file or list of paths to multiple DICOM files
        server_url: URL of the server
        output_mode: Output mode for the request (default: JSON)
    """

    # Convert single path to list for consistent processing
    if isinstance(dicom_paths, str):
        dicom_paths = [dicom_paths]
    
    # Dictionary to store series instance metadata
    series_instance_metadata = {}
    
    # Process each DICOM file
    for idx, dicom_path in enumerate(dicom_paths, start=1):
        try:
            # Read DICOM file
            ds = pydicom.dcmread(dicom_path)

            # Get series number, default to "5" if not present
            series_number = str(getattr(ds, 'SeriesNumber', 5))
            
            # Create series entry if it doesn't exist
            if series_number not in series_instance_metadata:
                series_instance_metadata[series_number] = {}

            # Generate instance number (using format from example)
            instance_number = f"{series_number}10000"

            # Extract pixel data and convert to base64
            pixel_array = ds.pixel_array
            pixel_bytes = pixel_array.tobytes()
            pixel_base64 = base64.b64encode(pixel_bytes).decode('utf-8')

            # Extract DICOM metadata for this instance
            instance_metadata = {
                "00280008": {
                    "Value": [getattr(ds, 'NumberOfFrames', 1)],
                    "vr": "IS"
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

        except Exception as e:
            print(f"Error processing DICOM file {dicom_path}: {str(e)}")
            continue

    # Prepare request payload
    payload = {
        "seriesInstanceMetadata": series_instance_metadata,
        "additionalMetadata": {
            "smoker": "false"  # Default value, can be modified as needed
        },
        "outputMode": output_mode
    }

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
        'dicom_files',
        default=['sample_data/DX_1.dcm'],#, 'sample_data/XA_2.dcm'],
        nargs='*',
        help='Path(s) to DICOM file(s). You can specify multiple files.'
    )

    parser.add_argument(
        '--url',
        default='http://localhost:8000/inference/predict',
        help='Server URL (default: http://localhost:8000/inference/predict)'
    )

    parser.add_argument(
        '--output_mode',
        default='HTML',
        choices=['HTML','OHIF_ANNOTATIONS','JSON','WEB_APP','PDF'],
        help='Output mode for the request (default: HTML)'
    )

    args = parser.parse_args()

    # Verify that all files exist
    for file_path in args.dicom_files:
        if not Path(file_path).is_file():
            print(f"Error: File not found: {file_path}")
            sys.exit(1)

    # Send the request
    result = send_dicom_data(
        dicom_paths=args.dicom_files,
        server_url=args.url,
        output_mode=args.output_mode
    )

    if result:
        print("Server response received")

        # Display content if it's HTML or PDF
        if args.output_mode in ['HTML', 'PDF']:
            display_response(result, args.output_mode)
        else:
            print("Server response:")
            print(result)

if __name__ == "__main__":
    main()
    # Example usage: python request_tester.py --url http://localhost:8000/inference/predict --output-mode CSV file1.dcm file2.dcm
