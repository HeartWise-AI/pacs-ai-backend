import argparse
import base64
import os
import pprint
import sys
import tempfile
import webbrowser
from typing import Union

import requests
from request_payload import build_request_payload, collect_dicom_files


def display_response(response_data, output_mode):
    """
    Display HTML or PDF content in a browser window.

    Args:
        response_data: Server response containing base64 encoded content
        output_mode: Type of content ('HTML' or 'PDF')
    """
    try:
        # Extract the base64 content based on output mode
        if output_mode == "HTML":
            base64_content = response_data["data"]["htmlBase64"]
            file_extension = ".html"
            content = base64.b64decode(base64_content).decode("utf-8")
        elif output_mode == "PDF":
            base64_content = response_data["data"]["pdfBase64"]
            file_extension = ".pdf"
            content = base64.b64decode(base64_content)
        else:
            print(f"Unsupported output mode for display: {output_mode}")
            return

        # Save file in current directory
        if output_mode == "HTML":
            # Generate a unique filename for HTML
            import datetime
            timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
            filename = f"output_{timestamp}.html"
            
            with open(filename, "w", encoding="utf-8") as f:
                f.write(content)
            
            file_path = os.path.abspath(filename)
            print(f"HTML file saved as: {file_path}")
        else:  # PDF
            # For PDF, still use temporary file since we need binary mode
            with tempfile.NamedTemporaryFile(
                delete=False, suffix=file_extension, mode="wb"
            ) as tmp_file:
                tmp_file.write(content)
                file_path = tmp_file.name

        # Open the file in the default web browser
        webbrowser.open("file://" + os.path.realpath(file_path))

    except Exception as e:
        print(f"Error displaying content: {str(e)}")


def send_dicom_data(
    dicom_paths: Union[str, list[str]],
    server_url: str,
    output_mode: str = "JSON",
    send_metadata_only: bool = False,
    group_series: bool = False,
) -> requests.Response:
    """
    Read DICOM file(s), process the data, and send a POST request to the server.

    Args:
        dicom_paths: Path to a single DICOM file or list of paths to multiple DICOM files
        server_url: URL of the server
        output_mode: Output mode for the request (default: JSON)
        send_metadata_only: If True, sends only DICOM metadata without pixel data (default: False)
        group_series: If True, treats all DICOM files as part of the same series (default: False)
    """

    if isinstance(dicom_paths, str):
        dicom_paths = [dicom_paths]
    payload = build_request_payload(
        dicom_paths,
        output_mode=output_mode,
        send_metadata_only=send_metadata_only,
        group_series=group_series,
    )

    # Send POST request
    try:
        response: requests.Response = requests.post(
            server_url, 
            json=payload, 
            timeout=500,
            headers={"Content-Type": "application/json"}
        )
        
        return response
    
    except requests.exceptions.RequestException as e:
        print(f"Error sending request: {str(e)}")
        return None


def main():
    parser = argparse.ArgumentParser(description="Send DICOM data to server via POST request.")

    parser.add_argument(
        "dicom_paths",
        default=["/home/pacs-ai/pacs-ai-backend/model-template/sample_data/US_Study"],
        nargs="*",
        help="Path(s) to DICOM file(s) or directories containing DICOM files.",
    )

    parser.add_argument(
        "--url",
        default="http://localhost:8001/inference/predict",
        help="Server URL (default: http://localhost:8001/inference/predict)",
    )

    parser.add_argument(
        "--output_mode",
        default="JSON",
        choices=["HTML", "OHIF_ANNOTATIONS", "JSON", "WEB_APP", "PDF"],
        help="Output mode for the request (default: HTML)",
    )

    parser.add_argument(
        "--metadata-only",
        default=False,
        action="store_true",
        help="Send only DICOM metadata without separate pixel data",
    )

    parser.add_argument(
        "--group-series",
        default=False,
        action="store_true",
        help="Treat all DICOM files as part of the same series",
    )

    args = parser.parse_args()

    # Collect all DICOM files from the provided paths
    dicom_files = collect_dicom_files(args.dicom_paths)

    if not dicom_files:
        print("Error: No DICOM files found in the specified paths")
        sys.exit(1)

    print(f"Found {len(dicom_files)} DICOM files")

    # Send the request
    response: requests.Response = send_dicom_data(
        dicom_paths=[str(path) for path in dicom_files],
        server_url=args.url,
        output_mode=args.output_mode,
        send_metadata_only=args.metadata_only,
        group_series=args.group_series,
    )

    print("Server response received with status code: ", response)
    response_json = response.json()
    if not response_json['success']:
        print("Server response failed with errorCode: ", response_json['errorCode'])
        print("Server response failed with errorMessage: ", response_json['message'])
        return

    # Display content if it's HTML or PDF
    if args.output_mode in ["HTML", "PDF"]:
        display_response(response_json, args.output_mode)
        return

    if args.output_mode == "OHIF_ANNOTATIONS":
        from visualization import visualize_segmentation

        payload = response_json["data"]
        visualize_segmentation(
            encoded_data=payload["segmentation"]["labelmap"],
            dimensions=payload["segmentation"]["dimensions"],
            segments=payload["segmentation"]["segments"],
        )
        return
    
    if args.output_mode == "JSON":
        pprint.pprint(response_json)
    # result will be an HTML string containing an interactive 3D visualization


if __name__ == "__main__":
    main()
    # Example usage: python request_tester.py --url http://localhost:8000/inference/predict --output-mode CSV file1.dcm file2.dcm
