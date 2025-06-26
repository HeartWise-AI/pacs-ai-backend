import re
from typing import Any

import requests
from langchain_core.tools import BaseTool


class DicomLvefTool(BaseTool):
    """Tool for detecting Left Ventricular Ejection Fraction (LVEF) from X-ray Angiography DICOM payloads."""

    name: str = "dicom_lvef_tool"
    description: str = "This tool detects the LVEF from XA dicom payload. It should be used for X-ray Angiography DICOM files when you need to analyze cardiac function."
    api_url: str = "http://localhost:8001/inference/predict"

    def __init__(self, api_url: str | None = None):
        """Initialize the DicomLvefTool.

        Args:
            api_url (Optional[str]): The URL of the LVEF detection API service.
                If None, use the default URL.
        """
        super().__init__()
        if api_url:
            self.api_url = api_url

        # Ensure the tool name complies with OpenAI's pattern requirement (^[a-zA-Z0-9_-]+$)
        # This is a safeguard in case the class attribute is changed
        self.name = re.sub(r"[^a-zA-Z0-9_-]", "_", self.name)
        if self.name and self.name[0].isdigit():
            self.name = f"tool_{self.name}"

    def _run(self, *args, **kwargs) -> dict[str, Any]:
        """Process a DICOM payload to detect LVEF.

        Args can be passed as positional or keyword arguments.

        Returns:
            Dict containing the LVEF results from the external service
        """
        try:
            # Get the DICOM payload from args or kwargs
            dicom_payload = None

            if args and len(args) > 0:
                # If the first arg is a dict, use it as the payload
                if isinstance(args[0], dict):
                    dicom_payload = args[0]
                # Handle the case where the model returns a string description instead of the payload
                elif isinstance(args[0], str) and args[0].startswith("{'__arg"):
                    # This is just a placeholder - the real payload should be passed separately
                    pass
                # Any other string or object, try to use it
                else:
                    dicom_payload = args[0]
            elif "dicom_payload" in kwargs:
                dicom_payload = kwargs["dicom_payload"]

            if not dicom_payload:
                return {"status": "error", "message": "No DICOM payload provided"}

            print(f"Sending DICOM payload to {self.api_url}")
            # Send the payload to the external service
            response = requests.post(self.api_url, json=dicom_payload)
            response.raise_for_status()

            # Return the results
            return {
                "status": "success",
                "results": response.json(),
                "message": "LVEF analysis completed successfully",
            }
        except Exception as e:
            return {"status": "error", "message": f"Error processing DICOM data: {str(e)}"}
