#!/usr/bin/env python3
"""
Test script for the modified Orchestrator API that accepts DICOM payloads.
"""

import argparse
import base64
import json
import mimetypes

import requests
from request_tester import create_dicom_payload

# Base URL for API requests
BASE_URL = "http://localhost:8585"


def print_response(response, label=None):
    """Print a formatted API response"""
    if label:
        print(f"\n===== {label} =====")

    status_code = response.status_code
    print(f"Status Code: {status_code}")

    try:
        json_data = response.json()
        print(json.dumps(json_data, indent=2))
    except:
        print("Response content:", response.text)


def create_new_thread():
    """Create a new thread and return the thread ID"""
    print("\n1. Creating a new thread...")
    response = requests.post(f"{BASE_URL}/new_thread", timeout=10)
    print_response(response, "New Thread Response")

    if response.status_code == 200:
        thread_id = response.json().get("thread_id")
        print(f"Thread ID: {thread_id}")
        return thread_id
    raise Exception(f"Failed to create thread: {response.status_code}")


def upload_dicom_payload(thread_id, dicom_file_paths, meta_only=True, group_series=False):
    """Upload a DICOM payload to the specified thread"""
    print("\n2. Creating and uploading DICOM payload...")

    # Create DICOM payload using request_tester function
    payload = create_dicom_payload(
        dicom_paths=dicom_file_paths,
        output_mode="JSON",
        send_metadata_only=meta_only,
        group_series=group_series,
    )

    # Send payload to the server
    response = requests.post(
        f"{BASE_URL}/dicom/{thread_id}",
        json={"payload": payload, "thread_id": thread_id},
        timeout=10,
    )
    print_response(response, "DICOM Upload Response")

    return response.json() if response.status_code == 200 else None


def encode_image_to_base64(image_path):
    """Encode an image file to base64 string and detect MIME type"""
    try:
        with open(image_path, 'rb') as image_file:
            encoded_string = base64.b64encode(image_file.read()).decode('utf-8')
        
        # Detect MIME type
        mime_type, _ = mimetypes.guess_type(image_path)
        if mime_type is None:
            # Default to JPEG if we can't determine the type
            mime_type = 'image/jpeg'
        
        return encoded_string, mime_type
    except Exception as e:
        print(f"Error encoding image {image_path}: {str(e)}")
        return None, None


def send_message(thread_id, message_text, image_path=None):
    """Send a message to the agent and get the response"""
    print(f"\n3. Sending message: '{message_text}'...")
    
    message_data = {"message": message_text}
    
    # Add image data if image_path is provided
    if image_path:
        print(f"   Including image: {image_path}")
        image_data, image_type = encode_image_to_base64(image_path)
        if image_data:
            message_data["image_data"] = image_data
            message_data["image_type"] = image_type
        else:
            print("   Warning: Failed to encode image, sending message without image")

    response = requests.post(f"{BASE_URL}/chat/{thread_id}", json=message_data, timeout=10)
    print_response(response, "Chat Response")

    return response.json() if response.status_code == 200 else None


def get_thread_info(thread_id):
    """Get information about a thread"""
    print(f"\n4. Getting thread info for {thread_id}...")

    response = requests.get(f"{BASE_URL}/threads/{thread_id}", timeout=10)
    print_response(response, "Thread Info Response")

    return response.json() if response.status_code == 200 else None


def main():
    parser = argparse.ArgumentParser(
        description="Test the modified Orchestrator API with DICOM payloads"
    )
    parser.add_argument("--dicom", nargs="+", required=True, help="Path(s) to DICOM file(s)")
    parser.add_argument(
        "--message",
        help="Message to send to the agent",
        default="What type of image is this? Please analyze and describe what you see.",
    )
    parser.add_argument(
        "--image",
        help="Path to an image file to include in the chat message",
        default="xray.jpeg",
    )
    parser.add_argument(
        "--metadata-only",
        action="store_true",
        default=True,
        help="Send only DICOM metadata without pixel data",
    )
    parser.add_argument(
        "--group-series",
        action="store_true",
        default=False,
        help="Treat all DICOM files as part of the same series",
    )
    args = parser.parse_args()

    try:
        # Step 1: Create a new thread
        thread_id = create_new_thread()

        # Step 2: Upload the DICOM payload
        # upload_dicom_payload(
        #     thread_id, args.dicom, meta_only=args.metadata_only, group_series=args.group_series
        # )

        # Step 3: Send initial message to the agent with optional image
        send_message(thread_id, args.message, args.image)

        print("\n✅ API test completed successfully!")

    except Exception as e:
        print(f"\n❌ Error during testing: {str(e)}")


if __name__ == "__main__":
    main()
#  python test_dicom_api.py --dicom XA_1.dcm --message "What type of image is this?" --image xray.jpeg
