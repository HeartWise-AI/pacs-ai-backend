#!/usr/bin/env python3
"""
Test script for the modified Orchestrator API that accepts multiple DICOM payloads.
This script demonstrates uploading multiple studies simultaneously (e.g., CR and US studies)
with their respective preview images.
"""

import argparse
import base64
import json
import mimetypes
import os
import sys

import requests

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


def create_new_thread(bearer_token=None, api_base_url=None):
    """Create a new thread and return the thread ID"""
    print("\n1. Creating a new thread...")
    
    # Prepare request data
    request_data = {}
    if bearer_token:
        request_data["bearer_token"] = bearer_token
    if api_base_url:
        request_data["api_base_url"] = api_base_url
    
    response = requests.post(f"{BASE_URL}/new_thread", json=request_data, timeout=10)
    print_response(response, "New Thread Response")

    if response.status_code == 200:
        thread_id = response.json().get("thread_id")
        print(f"Thread ID: {thread_id}")
        return thread_id
    raise Exception(f"Failed to create thread: {response.status_code}")


def upload_multiple_studies_payloads(thread_id, study_configs, bearer_token=None, api_base_url=None):
    """Upload multiple studies payloads to the specified thread
    
    Args:
        thread_id: The thread ID to upload to
        study_configs: List of dictionaries, each containing:
            - study_instance_uid: Study Instance UID (optional, will be generated if not provided)
            - modality: Modality type (e.g., 'CR', 'US', 'XA')
            - image_path: Path to preview image (optional)
            - additional_metadata: Additional metadata dict (optional)
            - series_instance_uids: List of series instance UIDs (optional)
        bearer_token: Bearer token for authentication (optional)
        api_base_url: API base URL for callbacks (optional)
    """
    print(f"\n2. Creating and uploading {len(study_configs)} studies payloads...")

    studies_payload = []
    
    for i, config in enumerate(study_configs, 1):
        modality = config.get('modality', 'XA')
        image_path = config.get('image_path')
        study_instance_uid = config.get('study_instance_uid', f"generated-study-{modality}-{i}")
        additional_metadata = config.get('additional_metadata', {})
        series_instance_uids = config.get('series_instance_uids', [f"generated-series-{modality}-{i}"])
        
        print(f"   Processing study {i} ({modality})...")
        
        # Get preview image if available
        preview_base64 = None
        if image_path and os.path.exists(image_path):
            try:
                with open(image_path, 'rb') as img_file:
                    preview_base64 = base64.b64encode(img_file.read()).decode('utf-8')
                print(f"     Added preview image from {image_path}")
            except Exception as e:
                print(f"     Warning: Could not encode preview image: {e}")
        
        # Create study payload structure directly
        study_data = {
            "studyInstanceUID": study_instance_uid,
            "additionalMetadata": additional_metadata,
            "seriesInstanceUIDs": series_instance_uids,
            "modality": modality,
            "previewImageBase64": preview_base64
        }
        studies_payload.append(study_data)

    # Prepare request data
    request_data = {
        "payload": studies_payload,
        "thread_id": thread_id
    }
    if bearer_token:
        request_data["bearer_token"] = bearer_token
    if api_base_url:
        request_data["api_base_url"] = api_base_url

    # Send all studies payload to the server
    response = requests.post(
        f"{BASE_URL}/dicom/{thread_id}",
        json=request_data,
        timeout=10,
    )
    print_response(response, "Studies Upload Response")

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


def send_message(thread_id, message_text, image_path=None, bearer_token=None, api_base_url=None):
    """Send a message to the agent and get the response"""
    print(f"\n3. Sending message: '{message_text}'...")
    
    message_data = {"message": message_text}
    
    # Add authentication data if provided
    if bearer_token:
        message_data["bearer_token"] = bearer_token
    if api_base_url:
        message_data["api_base_url"] = api_base_url
    
    # Add image data if image_path is provided
    if image_path:
        print(f"   Including image: {image_path}")
        image_data, image_type = encode_image_to_base64(image_path)
        if image_data:
            message_data["image_data"] = image_data
            message_data["image_type"] = image_type
        else:
            print("   Warning: Failed to encode image, sending message without image")

    response = requests.post(f"{BASE_URL}/chat/{thread_id}", json=message_data, timeout=60)
    print_response(response, "Chat Response")

    return response.json() if response.status_code == 200 else None


def get_thread_info(thread_id, bearer_token=None, api_base_url=None):
    """Get information about a thread"""
    print(f"\n4. Getting thread info for {thread_id}...")

    # Prepare query parameters
    params = {}
    if bearer_token:
        params["bearer_token"] = bearer_token
    if api_base_url:
        params["api_base_url"] = api_base_url

    response = requests.get(f"{BASE_URL}/threads/{thread_id}", params=params, timeout=10)

    return response.json() if response.status_code == 200 else None


def main():
    parser = argparse.ArgumentParser(
        description="Test the modified Orchestrator API with multiple studies payloads"
    )
    parser.add_argument(
        "--cr-study-uid",
        help="Study Instance UID for CR study",
        default="1.2.3.4.5.6.7.8.9.10.CR",
    )
    parser.add_argument(
        "--us-study-uid",
        help="Study Instance UID for US study",
        default="1.2.3.4.5.6.7.8.9.10.US",
    )
    parser.add_argument(
        "--xa-study-uid",
        help="Study Instance UID for XA study",
        default="1.2.3.4.5.6.7.8.9.10.XA",
    )
    parser.add_argument(
        "--message",
        help="Message to send to the agent",
        default="Assess the right ventricular function",
    )
    parser.add_argument(
        "--cr-image",
        help="Path to preview image for CR study",
        default="xray.jpeg",
    )
    parser.add_argument(
        "--us-image",
        help="Path to preview image for US study",
        default="us.jpeg",
    )
    parser.add_argument(
        "--xa-image",
        help="Path to preview image for XA study",
        default="xa.jpeg",
    )
    parser.add_argument(
        "--bearer-token",
        help="Bearer token for API authentication",
        default=None,
    )
    parser.add_argument(
        "--api-base-url",
        help="API base URL for callbacks",
        default="http://localhost:8000",
    )
    args = parser.parse_args()

    try:
        # Step 1: Create a new thread
        thread_id = create_new_thread(args.bearer_token, args.api_base_url)

        # Step 2: Upload multiple studies payloads - CR and US studies
        study_configs = [
            {
                'study_instance_uid': args.cr_study_uid,
                'modality': 'CR',  # Computed Radiography
                'image_path': args.cr_image,
                'additional_metadata': {},
                'series_instance_uids': ['1.2.3.4.5.6.7.8.9.10.CR.1', '1.2.3.4.5.6.7.8.9.10.CR.2', '1.2.3.4.5.6.7.8.9.10.CR.3']
            },
            {
                'study_instance_uid': args.us_study_uid,
                'modality': 'US',  # Ultrasound
                'image_path': args.us_image,
                'additional_metadata': {},
                'series_instance_uids': ['1.2.3.4.5.6.7.8.9.10.US.1', '1.2.3.4.5.6.7.8.9.10.US.2', '1.2.3.4.5.6.7.8.9.10.US.3']
            },
            {
                'study_instance_uid': "2.16.124.113611.1.118.1.1.7162550",
                'modality': 'XA',  # X-ray Angiography
                'image_path': args.xa_image,
                'additional_metadata': {},
                'series_instance_uids': ['1.3.46.670589.29.1877192777354251339637785287592012', '1.3.46.670589.29.1877192777354251339637786612934242', '1.3.46.670589.29.1877192777354251339637786800967842']
            }
        ]
        
        upload_multiple_studies_payloads(thread_id, study_configs, args.bearer_token, args.api_base_url)

        # Step 2.5: Check thread info to verify studies are tracked
        thread_info = get_thread_info(thread_id, args.bearer_token, args.api_base_url)
        if thread_info and "studies_count" in thread_info:
            print(f"\n✅ Thread now tracking {thread_info['studies_count']} studies")
            preview_count = 0
            for i, study in enumerate(thread_info.get("studies", []), 1):
                has_preview = study.get('hasPreviewImage', False)
                if has_preview:
                    preview_count += 1
                print(f"   Study {i}: {study.get('studyInstanceUID', 'N/A')} ({study.get('modality', 'N/A')}) {'[Preview image available]' if has_preview else ''}")
            
            if preview_count > 0:
                print(f"   📸 {preview_count} preview images automatically included in LLM context")

        # Step 3: Send initial message to the agent
        # Note: Preview images from both studies payloads are automatically included in LLM context
        send_message(thread_id, args.message, bearer_token=args.bearer_token, api_base_url=args.api_base_url)

        print("\n✅ API test completed successfully!")
        print(f"\n📋 Usage example:")
        print(f"python test_dicom_api.py --cr-image xray.jpeg --us-image us.jpeg --xa-image xa.jpeg --bearer-token your_token --api-base-url https://api.example.com")

    except Exception as e:
        print(f"❌ Error: {str(e)}")
        sys.exit(1)


if __name__ == "__main__":
    main()
    # python test_dicom_api.py --cr-image xray.jpeg --us-image us.jpeg --xa-image xa.jpeg --api-base-url http://localhost:8000 --bearer-token 2zYzodq7m1M5CzbnER1E5QeSZMP 
