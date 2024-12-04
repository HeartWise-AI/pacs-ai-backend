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

    # List to store all processed frames
    all_dicoms = []

    # Variables to store patient information
    age = 65 # Default value
    gender = "MALE"  # Default value

    # Process each DICOM file
    for dicom_path in dicom_paths:
        try:
            # Read DICOM file
            ds = pydicom.dcmread(dicom_path)

            # Extract pixel data
            pixel_array = ds.pixel_array

            # Add color channel dimension if grayscale
            if len(pixel_array.shape) == 2:
                pixel_array = np.expand_dims(pixel_array, axis=0)

            # Add to frames list
            all_dicoms.append(pixel_array)

            # Try to extract patient age
            try:
                if hasattr(ds, 'PatientAge'):
                    age = int(ds.PatientAge[:-1])
                elif hasattr(ds, 'PatientBirthDate'):
                    age = 0  # Replace with actual calculation if needed
            except:
                age = 0

            # Try to extract patient gender
            try:
                if hasattr(ds, 'PatientSex'):
                    gender = "MALE" if ds.PatientSex.upper() == "M" else "FEMALE"
            except:
                gender = "MALE"

        except Exception as e:
            print(f"Error processing DICOM file {dicom_path}: {str(e)}")
            continue

    # Convert frames to numpy array and reshape to required format
    dicoms_array = np.array(all_dicoms)

    # Reshape to match required 5D format: Series × Frames × Color Channels × Height × Width
    if len(dicoms_array.shape) == 4:
        dicoms_array = np.expand_dims(dicoms_array, axis=0)

    # Transpose to match the 5D format:
    dicoms_array = np.transpose(dicoms_array, (0,2,1,3,4))

    # Prepare request payload
    payload = {
        "inferences": dicoms_array.tolist(),
        "age": age,
        "gender": gender,
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
        default=['sample_data/XA_1.dcm', 'sample_data/XA_2.dcm'],
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
