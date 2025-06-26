import json
import re
from typing import Any

import docker
import requests
from docker.errors import DockerException, NotFound
from langchain_core.callbacks import AsyncCallbackManagerForToolRun, CallbackManagerForToolRun
from langchain_core.tools import BaseTool
from logger import logger
from pydantic import BaseModel, Field

# Global mapping to store tool name -> container IP and port
TOOL_CONTAINER_MAPPING: dict[str, tuple[str, int]] = {}

# Initialize Docker client
try:
    docker_client = docker.from_env()
    logger.info("Docker client initialized successfully")
except DockerException as e:
    logger.error(f"Failed to initialize Docker client: {str(e)}")
    docker_client = None


class DicomPayloadInput(BaseModel):
    """Input schema for DICOM payload-based tools."""

    dicom_payload: dict[str, Any] = Field(
        ...,
        description="DICOM payload containing series instance metadata and other relevant data",
    )


class DynamicContainerTool(BaseTool):
    """A tool that communicates with a Docker container service endpoint."""

    def __init__(
        self, name: str, description: str, args_schema: type[BaseModel] = DicomPayloadInput
    ):
        super().__init__(name=name, description=description, args_schema=args_schema)

    def _run(
        self,
        dicom_payload: dict[str, Any],
        run_manager: CallbackManagerForToolRun | None = None,
    ) -> dict[str, Any]:
        """
        Execute the tool by sending a request to the container's API endpoint.

        Args:
            dicom_payload (Dict[str, Any]): The DICOM payload to analyze
            run_manager (Optional[CallbackManagerForToolRun]): Callback manager

        Returns:
            Dict[str, Any]: The API response or error information
        """
        # Validate payload
        if self._is_invalid_payload(dicom_payload):
            return self._create_error_response("No valid DICOM payload provided")

        # Get container IP and port from global mapping using tool name
        if self.name not in TOOL_CONTAINER_MAPPING:
            return self._create_error_response(
                f"No container information found for tool {self.name}"
            )

        container_ip, port = TOOL_CONTAINER_MAPPING[self.name]

        # Send request to the container endpoint
        url = f"http://{container_ip}:{port}/api/inference/predict"
        logger.info(f"Sending request to {url}")

        return self._send_request(url, dicom_payload)

    def _is_invalid_payload(self, payload: Any) -> bool:
        """Check if the payload is invalid."""
        if not payload:
            return True

        return bool(isinstance(payload, str) and payload.startswith("{'__arg"))

    def _send_request(self, url: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Send a request to the container endpoint."""
        try:
            response = requests.post(url, json=payload, timeout=30)
            response.raise_for_status()

            result = response.json()
            return self._format_result(result)

        except Exception as e:
            logger.error(f"Error in {self.name}: {str(e)}")
            return self._create_error_response(str(e))

    def _format_result(self, result: Any) -> dict[str, Any]:
        """Format the result with appropriate metadata."""
        metadata = {
            "tool_name": self.name,
            "analysis_status": "completed",
        }

        if isinstance(result, dict):
            if "metadata" not in result:
                result["metadata"] = metadata
            return result
        return {"result": result, "metadata": metadata}

    def _create_error_response(self, error_message: str) -> dict[str, Any]:
        """Create a standardized error response."""
        return {
            "status": "error",
            "message": f"Error using {self.name}: {error_message}",
            "metadata": {
                "tool_name": self.name,
                "analysis_status": "failed",
                "error": error_message,
            },
        }

    async def _arun(
        self,
        dicom_payload: dict[str, Any],
        run_manager: AsyncCallbackManagerForToolRun | None = None,
    ) -> dict[str, Any]:
        """
        Asynchronously execute the tool. Currently calls the synchronous version.

        Args:
            dicom_payload (Dict[str, Any]): The DICOM payload to analyze
            run_manager (Optional[AsyncCallbackManagerForToolRun]): Async callback manager

        Returns:
            Dict[str, Any]: The API response or error information
        """
        # This method simply calls the synchronous version for now
        return self._run(dicom_payload)


def get_containers_on_network(network_name: str) -> list[dict[str, Any]]:
    """
    Get containers running on the specified Docker network.

    Args:
        network_name (str): The name of the Docker network

    Returns:
        List[Dict[str, Any]]: List of container information dictionaries
    """
    if docker_client is None:
        logger.error("Docker client not initialized")
        return []

    try:
        # Get network details
        try:
            network = docker_client.networks.get(network_name)
        except NotFound:
            logger.warning(f"No network found with name: {network_name}")
            return []

        # Use the Docker API to get network details
        network_attrs = network.attrs

        # No containers in this network
        if "Containers" not in network_attrs:
            logger.info(f"No containers found in network: {network_name}")
            return []

        containers = []
        # Extract each container's information
        for container_id, container_data in network_attrs["Containers"].items():
            try:
                # Get container details
                container = docker_client.containers.get(container_id)
                container_attrs = container.attrs

                # Extract IP address from network data
                ip_address = container_data.get("IPv4Address", "").split("/")[0]

                container_info = {
                    "id": container_id,
                    "name": container_data.get("Name"),
                    "ipv4_address": ip_address,
                    "image": container_attrs.get("Config", {}).get("Image", ""),
                    "labels": container_attrs.get("Config", {}).get("Labels", {}),
                    "ports": container_attrs.get("NetworkSettings", {}).get("Ports", {}),
                }

                containers.append(container_info)

            except NotFound:
                logger.warning(f"Container {container_id} not found")
                continue
            except Exception as e:
                logger.error(f"Error getting container {container_id} details: {str(e)}")
                continue

        return containers

    except Exception as e:
        logger.error(f"Error getting containers on network {network_name}: {str(e)}")
        return []


def fetch_tool_info_from_endpoint(container_ip: str, port: int = 80) -> dict[str, Any] | None:
    """
    Fetch tool information from the /inference/model-facts endpoint of a container.

    Args:
        container_ip (str): IP address of the container
        port (int): Port number for the service (default: 80)

    Returns:
        Optional[Dict[str, Any]]: Tool information or None if unavailable
    """
    url = f"http://{container_ip}:{port}/api/inference/model-facts"

    try:
        response = requests.get(url, timeout=2)
        response.raise_for_status()
        model_facts = response.json()

        response = requests.get(url.replace("model-facts", "model-info"), timeout=2)
        response.raise_for_status()
        model_info = response.json()

        return {
            "name": model_facts["data"]["en"]["Summary"]["Name"],
            "description": model_facts["data"]["en"]["Summary"]["Description"],
            "supported_dicom_tags": model_info["data"]["supportedDicomTags"],
            "supported_output_modes": model_info["data"]["supportedOutputModes"],
            "supported_dicom_modalities": model_info["data"]["supportedDicomModalities"],
        }

        # Store the container IP and port in the global mapping
        # The actual tool name (post-sanitization) will be added later in create_dynamic_tool
    except requests.RequestException as e:
        logger.debug(f"Failed to fetch tool info from {url}: {str(e)}")
        return None
    except json.JSONDecodeError as e:
        logger.debug(f"Failed to parse tool info JSON from {url}: {str(e)}")
        return None
    except KeyError as e:
        logger.debug(f"Missing expected field in tool info from {url}: {str(e)}")
        return None
    except Exception as e:
        logger.debug(f"Unexpected error fetching tool info from {url}: {str(e)}")
        return None


def create_dynamic_tool(
    tool_info: dict[str, Any], container_ip: str, port: int = 80
) -> BaseTool | None:
    """
    Create a dynamically defined tool based on container information.

    Args:
        tool_info (Dict[str, Any]): Tool information from the model-facts endpoint
        container_ip (str): IP address of the container
        port (int): Port number for the service

    Returns:
        Optional[BaseTool]: A dynamically created tool based on container info
    """
    if not tool_info:
        return None

    tool_name = tool_info.get("name")
    description = tool_info.get("description")

    if not tool_name or not description:
        logger.warning("Missing required tool information (name or description)")
        return None

    # TODO Support more modalities and different level of dicom tags
    if "XA" not in tool_info.get("supported_dicom_modalities", []):
        logger.warning(f"Tool {tool_name} does not support XA modality")
        return None
    if "JSON" not in tool_info.get("supported_output_modes", []):
        logger.warning(f"Tool {tool_name} does not support JSON output mode")
        return None
    if "*" not in tool_info.get("supported_dicom_tags", []):
        logger.warning(f"Tool {tool_name} does not support all DICOM tags")
        return None

    # Sanitize tool name to match OpenAI's pattern requirement (^[a-zA-Z0-9_-]+$)
    # Replace spaces and other invalid characters with underscores
    sanitized_name = re.sub(r"[^a-zA-Z0-9_-]", "_", tool_name)

    # Ensure the name doesn't start with a number (adding prefix if needed)
    if sanitized_name and sanitized_name[0].isdigit():
        sanitized_name = f"tool_{sanitized_name}"

    # Log if name was changed during sanitization
    if sanitized_name != tool_name:
        logger.info(f"Tool name sanitized: '{tool_name}' -> '{sanitized_name}'")

    try:
        # Add to global mapping
        TOOL_CONTAINER_MAPPING[sanitized_name] = (container_ip, port)
        logger.info(f"Added mapping for tool {sanitized_name} -> {container_ip}:{port}")

        return DynamicContainerTool(name=sanitized_name, description=description)
    except Exception as e:
        logger.error(f"Failed to create tool {sanitized_name}: {str(e)}")
        return None


def create_tool_for_container(container: dict[str, Any]) -> BaseTool | None:
    """
    Create a tool instance based on container information.

    Args:
        container (Dict[str, Any]): Container information

    Returns:
        Optional[BaseTool]: A tool instance or None if no matching tool
    """
    container_name = container.get("name", "").lower()
    ip_address = container.get("ipv4_address", "")

    if not ip_address:
        logger.warning(f"No IP address found for container: {container_name}")
        return None

    # Default port for the container service
    port = 80

    # Get tool information from the container's API
    tool_info = fetch_tool_info_from_endpoint(ip_address, port)

    if tool_info:
        logger.info(
            f"Retrieved tool info from {container_name}: {tool_info.get('name', 'unknown')}"
        )
        return create_dynamic_tool(tool_info, ip_address, port)

    logger.info(f"No tool info available for container: {container_name}")
    return None


def discover_and_create_tools(network_name: str = "pacs-net") -> dict[str, BaseTool]:
    """
    Discover containers on the specified network and create corresponding tools.

    Args:
        network_name (str): Docker network name. Defaults to "pacs-net".

    Returns:
        Dict[str, BaseTool]: Dictionary of tool instances keyed by tool name
    """
    tools_dict = {}

    # Get containers on the network
    containers = get_containers_on_network(network_name)

    if not containers:
        logger.warning(f"No containers found on network: {network_name}")
        return tools_dict

    # Create tools for each container
    for container in containers:
        container_name = container.get("name", "").replace("/", "")
        tool = create_tool_for_container(container)

        if tool:
            logger.info(f"Created tool '{tool.name}' for container '{container_name}'")
            tools_dict[tool.name] = tool

    return tools_dict
