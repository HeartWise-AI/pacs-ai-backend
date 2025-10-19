#!/usr/bin/env python3
"""
Test script for the Orchestrator API websearch tool.
This script demonstrates sending a message to the agent and getting the response.
"""

import argparse
import json

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


def send_message(thread_id, message_text,):
    """Send a message to the agent and get the response"""
    print(f"\n1. Sending message: '{message_text}'...")
    
    message_data = {"message": message_text}
    response = requests.post(f"{BASE_URL}/chat/{thread_id}", json=message_data, timeout=60)
    print_response(response, "Chat Response")

    return response.json() if response.status_code == 200 else None


def main():
    parser = argparse.ArgumentParser(
        description="Test the Orchestrator API websearch tool"
    )
    parser.add_argument(
        "--message",
        help="Message to send to the agent",
        default="What are the clinical guidelines for a LVEF of 35%?",
    )
    args = parser.parse_args()

    thread_id = create_new_thread()
    send_message(thread_id, args.message)

    print("\n✅ API test completed successfully!")
    print(f"\n📋 Usage example:")
    print(f"python test_websearch_tool.py --message 'What are the clinical guidelines for a LVEF of 35%?'")


if __name__ == "__main__":
    main()
    # python test_websearch_tool.py
