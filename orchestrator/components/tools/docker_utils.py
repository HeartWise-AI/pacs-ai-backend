import json
import re
from typing import Any

import docker
import requests
from docker.errors import DockerException, NotFound
from langchain_core.callbacks import AsyncCallbackManagerForToolRun, CallbackManagerForToolRun
from langchain_core.tools import BaseTool
from logger import logger, sanitize_for_logging
from pydantic import BaseModel, Field

# Import static tools
from .clinical_websearch import ClinicalWebSearchTool

# Global mapping to store tool name -> container ID
TOOL_CONTAINER_MAPPING: dict[str, str] = {}

# Global mapping to store tool name -> supported modalities
TOOL_MODALITY_MAPPING: dict[str, list[str]] = {}

# Initialize Docker client
try:
    docker_client = docker.from_env()
    logger.info("Docker client initialized successfully")
except DockerException as e:
    logger.error(f"Failed to initialize Docker client: {str(e)}")
    docker_client = None


class StudiesInput(BaseModel):
    """Input schema for studies-based tools."""

    studies: list[dict[str, Any]] = Field(
        ...,
        description="List of studies data containing series instance metadata and other relevant data",
    )
    api_base_url: str | None = Field(
        None,
        description="Base URL for API requests",
    )
    bearer_token: str | None = Field(
        None,
        description="Bearer token for authentication",
    )


class DynamicContainerTool(BaseTool):
    """A tool that communicates with a Docker container service endpoint."""

    def __init__(
        self, name: str, description: str, args_schema: type[BaseModel] = StudiesInput
    ):
        super().__init__(name=name, description=description, args_schema=args_schema)

    def _filter_studies_by_modality(self, studies: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """
        Filter studies to only include those matching the tool's supported modalities.
        
        Args:
            studies: List of study dictionaries to filter
            
        Returns:
            Filtered list of studies containing only matching modalities
        """
        # Get supported modalities for this tool
        supported_modalities = TOOL_MODALITY_MAPPING.get(self.name, [])
        
        # If no modality restrictions, return all studies
        if not supported_modalities:
            logger.info(f"Tool {self.name} has no modality restrictions, passing all studies")
            return studies
            
        # Filter studies based on modality
        filtered_studies = []
        for study in studies:
            study_modality = study.get("modality")
            if study_modality and study_modality in supported_modalities:
                filtered_studies.append(study)
                logger.info(f"Including study {study.get('studyInstanceUID', 'unknown')} with modality {study_modality} for tool {self.name}")
            else:
                logger.info(f"Excluding study {study.get('studyInstanceUID', 'unknown')} with modality {study_modality} for tool {self.name}")
        
        logger.info(f"Filtered {len(studies)} studies to {len(filtered_studies)} studies for tool {self.name}")
        return filtered_studies

    def _run(
        self,
        studies: list[dict[str, Any]],
        api_base_url: str | None = None,
        bearer_token: str | None = None,
        run_manager: CallbackManagerForToolRun | None = None,
    ) -> dict[str, Any]:
        """
        Execute the tool by sending a request to the container's API endpoint.

        Args:
            studies (Dict[str, Any]): The studies data to analyze
            api_base_url (Optional[str]): Base URL for API requests
            bearer_token (Optional[str]): Bearer token for authentication
            run_manager (Optional[CallbackManagerForToolRun]): Callback manager

        Returns:
            Dict[str, Any]: The API response or error information
        """
        # Validate payload
        if self._is_invalid_payload(studies):
            return self._create_error_response("No valid studies data provided")

        # Filter studies by modality
        filtered_studies = self._filter_studies_by_modality(studies)
        
        # Check if any studies remain after filtering
        if len(filtered_studies) == 0:
            supported_modalities = TOOL_MODALITY_MAPPING.get(self.name, [])
            return self._create_error_response(
                f"No studies found matching tool's supported modalities: {', '.join(supported_modalities)}"
            )

        # Get container ID from global mapping using tool name
        if self.name not in TOOL_CONTAINER_MAPPING:
            return self._create_error_response(
                f"No container information found for tool {self.name}"
            )

        container_id = TOOL_CONTAINER_MAPPING[self.name]

        # Use provided base URL or fall back to default
        if not api_base_url:
            api_base_url = "http://localhost:8000"
            logger.warning(f"No api_base_url provided for tool {self.name}, using default: {api_base_url}")
        
        # Construct URL using the new format with container ID
        url = f"{api_base_url}/v1/inference/model/proxy/container/{container_id}/predict"
        
        # Log the request with sanitized data
        sanitized_studies = sanitize_for_logging(filtered_studies)
        logger.info(f"Sending request to {url} with {len(filtered_studies)} studies: {json.dumps(sanitized_studies, ensure_ascii=False)}")

        return self._send_request(url, filtered_studies, bearer_token)

    def _is_invalid_payload(self, payload: Any) -> bool:
        """Check if the payload is invalid."""
        if not payload:
            return True

        return bool(isinstance(payload, str) and payload.startswith("{'__arg"))

    def _send_request(self, url: str, payload: list[dict[str, Any]], bearer_token: str | None = None) -> dict[str, Any]:
        """Send a request to the container endpoint."""
        try:
            # Clean the payload by removing unwanted fields
            cleaned_payload = []
            for study in payload:
                # Create a copy of the study dict and remove unwanted fields
                cleaned_study = study.copy()
                cleaned_study.pop('modality', None)
                cleaned_study.pop('previewImageBase64', None)
                cleaned_study['ForceJSON'] = True
                cleaned_payload.append(cleaned_study)

            # Log the cleaned payload (already sanitized since we removed previewImageBase64)
            logger.info(f"Cleaned payload for {self.name}: {json.dumps(cleaned_payload[0], ensure_ascii=False)}")

            # Prepare headers
            headers = {}
            if bearer_token:
                headers["Authorization"] = f"Bearer {bearer_token}"

            response = requests.post(url, json=cleaned_payload[0], timeout=500, headers=headers) # TODO: Only taking the first study for now, but should be able to handle multiple studies to comparare multiple timepoints
            response.raise_for_status()

            result = response.json()
            
            # Log the response with sanitization
            sanitized_result = sanitize_for_logging(result)
            logger.info(f"Response from {self.name}: {json.dumps(sanitized_result, ensure_ascii=False)}")
            
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
        studies: list[dict[str, Any]],
        api_base_url: str | None = None,
        bearer_token: str | None = None,
        run_manager: AsyncCallbackManagerForToolRun | None = None,
    ) -> dict[str, Any]:
        """
        Asynchronously execute the tool. Currently calls the synchronous version.

        Args:
            studies (List[Dict[str, Any]]): The studies data to analyze
            api_base_url (Optional[str]): Base URL for API requests
            bearer_token (Optional[str]): Bearer token for authentication
            run_manager (Optional[AsyncCallbackManagerForToolRun]): Async callback manager

        Returns:
            Dict[str, Any]: The API response or error information
        """
        # This method simply calls the synchronous version for now
        return self._run(studies, api_base_url, bearer_token)


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
    tool_info: dict[str, Any], container_id: str
) -> BaseTool | None:
    """
    Create a dynamically defined tool based on container information.

    Args:
        tool_info (Dict[str, Any]): Tool information from the model-facts endpoint
        container_id (str): The container ID

    Returns:
        Optional[BaseTool]: A dynamically created tool based on container info
    """
    if not tool_info:
        return None

    tool_name = tool_info.get("name")
    description = tool_info.get("description")
    supported_modalities = tool_info.get("supported_dicom_modalities", [])

    if not tool_name or not description:
        logger.warning("Missing required tool information (name or description)")
        return None

    # Needs to support JSON output mode for LLM handling
    if "JSON" not in tool_info.get("supported_output_modes", []):
        logger.warning(f"Tool {tool_name} does not support JSON output mode")
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
        # Add to global mappings
        TOOL_CONTAINER_MAPPING[sanitized_name] = container_id
        TOOL_MODALITY_MAPPING[sanitized_name] = supported_modalities
        
        logger.info(f"Added mapping for tool {sanitized_name} -> container {container_id}")
        logger.info(f"Tool {sanitized_name} supports modalities: {', '.join(supported_modalities) if supported_modalities else 'all'}")

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
    container_id = container.get("id", "")
    ip_address = container.get("ipv4_address", "")

    if not container_id:
        logger.warning(f"No container ID found for container: {container_name}")
        return None

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
        return create_dynamic_tool(tool_info, container_id)

    logger.info(f"No tool info available for container: {container_name}")
    return None


def create_static_tools() -> dict[str, BaseTool]:
    """
    Create static tools that don't depend on Docker containers.

    Returns:
        Dict[str, BaseTool]: Dictionary of static tool instances keyed by tool name
    """
    static_tools = {}

    try:
        # Create clinical web search tool
        clinical_search_tool = ClinicalWebSearchTool()
        static_tools[clinical_search_tool.name] = clinical_search_tool
        logger.info(f"Created static tool: {clinical_search_tool.name}")

    except Exception as e:
        logger.error(f"Failed to create static tools: {str(e)}")

    return static_tools


def discover_and_create_tools(network_name: str = "pacs-net") -> dict[str, BaseTool]:
    """
    Discover containers on the specified network and create corresponding tools,
    plus add static tools that don't depend on containers.

    Args:
        network_name (str): Docker network name. Defaults to "pacs-net".

    Returns:
        Dict[str, BaseTool]: Dictionary of tool instances keyed by tool name
    """
    tools_dict = {}

    # Add static tools first
    static_tools = create_static_tools()
    tools_dict.update(static_tools)
    logger.info(f"Added {len(static_tools)} static tools")

    # Get containers on the network
    containers = get_containers_on_network(network_name)

    if not containers:
        logger.warning(f"No containers found on network: {network_name}")
    else:
        # Log discovered containers with sanitization
        sanitized_containers = sanitize_for_logging(containers)
        logger.info(f"Discovered containers: {json.dumps(sanitized_containers, ensure_ascii=False)}")
        
        # Create tools for each container
        for container in containers:
            container_name = container.get("name", "").replace("/", "")
            tool = create_tool_for_container(container)

            if tool:
                logger.info(f"Created tool '{tool.name}' for container '{container_name}'")
                tools_dict[tool.name] = tool

    logger.info(
        f"Total tools created: {len(tools_dict)} ({len(static_tools)} static, {len(tools_dict) - len(static_tools)} dynamic)"
    )
    return tools_dict
