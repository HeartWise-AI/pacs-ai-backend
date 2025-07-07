import json
import os
import re
import requests
import threading
import time
import uuid
from collections.abc import AsyncIterator
from datetime import datetime, timedelta, timezone
from typing import Any

from agent_init import initialize_agent
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from langgraph.checkpoint.memory import MemorySaver
from logger import logger
from pydantic import BaseModel


# Define request and response models
class MessageRequest(BaseModel):
    message: str | None = None
    thread_id: str | None = None
    image_data: str | None = None
    image_type: str | None = None
    bearer_token: str | None = None
    api_base_url: str | None = None


class StudyData(BaseModel):
    studyInstanceUID: str
    additionalMetadata: dict[str, Any] = {}
    seriesInstanceUIDs: list[str] = []
    modality: str | None = None
    previewImageBase64: str | None = None


class DicomPayloadRequest(BaseModel):
    payload: list[StudyData]
    thread_id: str | None = None
    bearer_token: str | None = None
    api_base_url: str | None = None


class ThreadCreateRequest(BaseModel):
    metadata: dict[str, Any] | None = None
    bearer_token: str | None = None
    api_base_url: str | None = None


class ThreadGetRequest(BaseModel):
    bearer_token: str | None = None
    api_base_url: str | None = None


class ToolResultResponse(BaseModel):
    tool_name: str
    result: Any


class MessageResponse(BaseModel):
    role: str  # 'assistant' or 'tool'
    content: str
    tool_results: list[ToolResultResponse] | None = None


class ChatResponse(BaseModel):
    thread_id: str
    message: MessageResponse | None = None
    status: str = "success"
    error: str | None = None


# Initialize FastAPI app
app = FastAPI(
    title="Orchestrator API",
    description="Medical Reasoning Agent for X-ray Angiography DICOM Analysis",
)

# Add CORS middleware
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # Allows all origins
    allow_credentials=True,
    allow_methods=["*"],  # Allows all methods
    allow_headers=["*"],  # Allows all headers
)


class APIHandler:
    """
    Handles API requests and manages agent interactions
    """

    def __init__(self):
        """Initialize the API handler with necessary directories and the agent."""
        self.checkpointer = MemorySaver()  # Use a single checkpointer instance
        self._initialize_agent()
        self.thread_data = {}  # Store thread-specific data
        self.current_thread_id = None
        self.display_file_path = None
        self._start_tools_refresh_thread()
        self._start_thread_cleanup_thread()

    def _create_agent_and_tools(self):
        """Create and return a new agent and tools dictionary."""
        model = os.getenv("MODEL")
        base_url = os.getenv("BASE_URL")
        docker_network = os.getenv("DOCKER_NETWORK")

        return initialize_agent(
            "components/docs/system_prompts.txt",
            checkpointer=self.checkpointer,  # Pass the existing checkpointer
            model=model,
            temperature=0,
            top_p=0.95,
            base_url=base_url,
            network_name=docker_network,
        )

    def _initialize_agent(self):
        """Initialize the agent with the required tools."""
        try:
            logger.info("Initializing Orchestrator agent")
            self.agent, self.tools_dict = self._create_agent_and_tools()
            logger.info(f"Agent initialized with {len(self.tools_dict)} tools")
        except Exception as e:
            logger.error(f"Failed to initialize agent: {str(e)}")
            raise RuntimeError(f"Agent initialization failed: {str(e)}") from e

    def _refresh_tools(self):
        """Refresh the agent's tools list."""
        try:
            logger.info("Refreshing agent tools")
            # Re-discover and create tools
            new_agent, new_tools_dict = self._create_agent_and_tools()

            # Update agent and tools
            self.agent = new_agent
            self.tools_dict = new_tools_dict
            logger.info(f"Agent tools refreshed - now has {len(self.tools_dict)} tools")
        except Exception as e:
            logger.error(f"Failed to refresh agent tools: {str(e)}")

    def _tools_refresh_worker(self):
        """Background worker that refreshes tools periodically."""
        while True:
            # Sleep for 60 seconds (1 minute)
            time.sleep(60)
            self._refresh_tools()

    def _start_tools_refresh_thread(self):
        """Start a background thread that refreshes tools every minute."""
        refresh_thread = threading.Thread(
            target=self._tools_refresh_worker,
            daemon=True,  # Make the thread a daemon so it exits when the main program exits
        )
        refresh_thread.start()
        logger.info("Started tools refresh background thread (refreshes every minute)")

    def _thread_cleanup_worker(self):
        """Background worker that cleans up inactive threads periodically."""
        while True:
            # Sleep for 60 seconds (1 minute)
            time.sleep(60)
            self._cleanup_inactive_threads()

    def _start_thread_cleanup_thread(self):
        """Start a background thread that cleans up inactive threads every minute."""
        cleanup_thread = threading.Thread(
            target=self._thread_cleanup_worker,
            daemon=True,  # Make the thread a daemon so it exits when the main program exits
        )
        cleanup_thread.start()
        logger.info("Started thread cleanup background thread (runs every minute)")

    def get_thread_data(self, thread_id: str) -> dict:
        """Get or create thread data for a specific thread."""
        if thread_id not in self.thread_data:
            self.thread_data[thread_id] = {
                "studies": [],  # Track studies objects for this thread
                "has_dicom_payload": False,
                "created_at": datetime.now(timezone.utc),
                "last_activity": datetime.now(timezone.utc),
                "context_messages": [],
                "bearer_token": None,
                "api_base_url": None,
            }
        else:
            # Update the last activity timestamp
            self.thread_data[thread_id]["last_activity"] = datetime.now(timezone.utc)
            
        return self.thread_data[thread_id]

    def set_thread_auth(self, thread_id: str, bearer_token: str = None, api_base_url: str = None):
        """Set authentication details for a thread."""
        thread_data = self.get_thread_data(thread_id)
        if bearer_token:
            thread_data["bearer_token"] = bearer_token
        if api_base_url:
            thread_data["api_base_url"] = api_base_url
        thread_data["last_activity"] = datetime.now(timezone.utc)

    def get_thread_auth(self, thread_id: str) -> tuple[str, str]:
        """Get authentication details for a thread."""
        thread_data = self.get_thread_data(thread_id)
        return thread_data.get("bearer_token"), thread_data.get("api_base_url")

    def make_authenticated_request(self, thread_id: str, method: str, endpoint: str, data: dict = None) -> dict:
        """Make an authenticated request to the Go API server."""
        bearer_token, api_base_url = self.get_thread_auth(thread_id)
        
        if not bearer_token or not api_base_url:
            logger.warning(f"Missing authentication details for thread {thread_id}")
            return {"error": "Missing authentication details"}
        
        headers = {
            "Authorization": f"Bearer {bearer_token}",
            "Content-Type": "application/json"
        }
        
        url = f"{api_base_url.rstrip('/')}/{endpoint.lstrip('/')}"
        
        try:
            
            if method.upper() == "GET":
                response = requests.get(url, headers=headers, params=data)
            elif method.upper() == "POST":
                response = requests.post(url, headers=headers, json=data)
            elif method.upper() == "PUT":
                response = requests.put(url, headers=headers, json=data)
            elif method.upper() == "DELETE":
                response = requests.delete(url, headers=headers)
            else:
                return {"error": f"Unsupported HTTP method: {method}"}
            
            if response.status_code == 200:
                return response.json()
            else:
                logger.error(f"API request failed with status {response.status_code}: {response.text}")
                return {"error": f"API request failed with status {response.status_code}"}
                
        except Exception as e:
            logger.error(f"Error making authenticated request: {str(e)}")
            return {"error": f"Request failed: {str(e)}"}

    def _cleanup_inactive_threads(self):
        """Clean up threads that have been inactive for more than 1 hour."""
        try:
            current_time = datetime.now(timezone.utc)
            inactive_threads = []
            
            for thread_id, data in self.thread_data.items():
                last_activity = data.get("last_activity")
                if last_activity and (current_time - last_activity) > timedelta(hours=1):
                    inactive_threads.append(thread_id)
            
            # Remove inactive threads
            for thread_id in inactive_threads:
                del self.thread_data[thread_id]
                logger.info(f"Cleaned up inactive thread: {thread_id}")
                
        except Exception as e:
            logger.error(f"Error during thread cleanup: {str(e)}")

    def handle_studies_payload(self, payload: list[StudyData], thread_id: str) -> dict[str, Any]:
        """
        Handle studies payload for a specific thread

        Args:
            payload (list[StudyData]): List of study data objects
            thread_id (str): Thread ID

        Returns:
            dict[str, Any]: Result of processing the studies payload
        """
        # Get or create thread data
        thread_data = self.get_thread_data(thread_id)

        # Convert payload to dictionary format
        payload_dict = [study.model_dump() for study in payload]

        # Store the studies payload in thread data
        thread_data["studies"] = payload_dict
        thread_data["has_dicom_payload"] = True
        thread_data["last_activity"] = datetime.now(timezone.utc)

        # Create a summary of the studies for the context message
        studies_summary = []
        for study in payload_dict:
            study_uid = study.get("studyInstanceUID", "Unknown")
            modality = study.get("modality", "Unknown")
            series_count = len(study.get("seriesInstanceUIDs", []))
            studies_summary.append(f"Study {study_uid[:8]}... ({modality}, {series_count} series)")

        # Create context message
        context_message = (
            f"DICOM Studies uploaded:\n"
            f"- Number of studies: {len(payload_dict)}\n"
            f"- Studies: {', '.join(studies_summary)}\n"
            f"These studies are now available for analysis."
        )

        # Store context message
        thread_data["context_messages"].append({
            "role": "system",
            "content": context_message,
            "timestamp": datetime.now(timezone.utc).isoformat()
        })

        logger.info(f"Stored {len(payload_dict)} studies for thread {thread_id}")

        return {
            "thread_id": thread_id,
            "status": "success",
            "message": f"Successfully processed {len(payload_dict)} studies",
            "studies_count": len(payload_dict)
        }

    def _parse_tool_result(self, content: str) -> Any:
        """
        Parse the result from a tool call

        Args:
            content (str): The content to parse

        Returns:
            Any: The parsed content
        """
        if not content:
            return None

        try:
            # If content is a JSON array string, parse it and return the first element
            if content.startswith("[") and content.endswith("]"):
                return json.loads(content)[0]
            return content
        except json.JSONDecodeError as e:
            logger.warning(f"Failed to parse tool result as JSON: {str(e)}")
            return content

    def _remove_think_tags(self, content: str) -> str:
        """
        Remove content enclosed in <think> tags

        Args:
            content (str): The content to process

        Returns:
            str: Content with <think> tags and their contents removed
        """
        if not content:
            return content

        # Check if content contains think tags
        if "<think>" in content:
            logger.info("Removing <think> tags from response content")
            # Remove content between <think> and </think> tags
            return re.sub(r"<think>.*?</think>", "", content, flags=re.DOTALL)

        return content

    async def process_message(self, message: str | None, thread_id: str, image_data: str | None, image_type: str | None) -> AsyncIterator[tuple]:
        """
        Process a message and generate responses.

        Args:
            message (Optional[str]): User message to process
            thread_id (str): ID of the current thread
            image_data (Optional[str]): Image data to include in the user message
            image_type (Optional[str]): Image type to include in the user message

        Yields:
            Tuple[List[MessageResponse], Optional[str], str]: Updated message history, display path, and empty string
        """
        # Initialize processing variables
        message_history = []
        self.current_thread_id = thread_id
        self.display_file_path = None

        # Get thread data and studies payload
        thread_data = self.get_thread_data(thread_id)
        studies = thread_data.get("studies", [])
        context_messages = thread_data.get("context_messages", [])
        
        # Get API base URL from thread data
        bearer_token, api_base_url = self.get_thread_auth(thread_id)

        logger.info(f"Processing message for thread {thread_id}")

        if studies:
            logger.info(f"Thread has {len(studies)} studies tracked in context")

        if context_messages:
            logger.info(
                f"Including {len(context_messages)} context message(s) for thread {thread_id}"
            )

        # Prepare messages for the agent
        messages = []

        # Add context messages first (if any)
        messages.extend(context_messages)

        # Add the user message
        if message is not None:
            user_content = [{"type": "text", "text": message}]
            
            # Add image data to the same user message if provided
            if image_data and image_type:
                user_content.append({
                    "type": "image_url",
                    "image_url": {
                        "url": f"data:{image_type};base64,{image_data}"
                    }
                })
                logger.info(f"Added image to user message (type: {image_type})")
            
            messages.append({"role": "user", "content": user_content})

        # Create the initial state and configuration
        initial_state = {"messages": messages}
        if studies:
            initial_state["studies"] = studies  # Include studies separately for easy access
        if api_base_url:
            initial_state["api_base_url"] = api_base_url  # Include API base URL for tools
        if bearer_token:
            initial_state["bearer_token"] = bearer_token  # Include bearer token for authentication

        config = {"configurable": {"thread_id": self.current_thread_id}}

        try:
            # Stream responses from the agent
            for event in self.agent.workflow.stream(initial_state, config):
                if not isinstance(event, dict):
                    continue

                if "process" in event:
                    for result in self._handle_process_event(event, message_history):
                        yield result

                elif "execute" in event:
                    for result in self._handle_execute_event(event, message_history):
                        yield result

        except Exception as e:
            logger.error(f"Error processing message: {str(e)}")
            error_message = f"❌ Error: {str(e)}"
            message_history.append(MessageResponse(role="assistant", content=error_message))
            yield message_history, self.display_file_path, ""

    def _handle_process_event(self, event, message_history):
        """Handle a 'process' event from the agent workflow."""
        content = event["process"]["messages"][-1].content
        if content:
            # Remove temporary file paths from content
            content = re.sub(r"temp/[^\s]*", "", content)
            # Remove think tags and their contents
            content = self._remove_think_tags(content)
            message_history.append(MessageResponse(role="assistant", content=content))
            yield message_history, self.display_file_path, ""

    def _handle_execute_event(self, event, message_history):
        """Handle an 'execute' event from the agent workflow."""
        tool_results = []

        # First, collect all tool results
        for message in event["execute"]["messages"]:
            tool_name = message.name
            tool_result = self._parse_tool_result(message.content)

            if not tool_result:
                continue

            # Format the result for easier processing
            formatted_result = " ".join(
                line.strip() for line in str(tool_result).splitlines()
            ).strip()

            tool_results.append({"tool_name": tool_name, "result": formatted_result})

            # Special handling for image_visualizer tool
            if (
                tool_name == "image_visualizer"
                and isinstance(tool_result, dict)
                and "image_path" in tool_result
            ):
                self.display_file_path = tool_result["image_path"]

        # If we have tool results, formulate a response
        if tool_results:
            # Format tool results for the model
            tool_results_str = ""
            for result in tool_results:
                tool_results_str += f"Tool: {result['tool_name']}\nResult: {result['result']}\n\n"

            # Construct a prompt for the model to formulate a response
            response_prompt = f"""The following tool(s) were executed:

{tool_results_str}

Based on these tool results, please provide a clear, helpful response for the user that explains what was found or done."""

            try:
                # Send to the model for interpretation
                messages = [
                    {"role": "user", "content": [{"type": "text", "text": response_prompt}]}
                ]

                # Use the agent's model to generate a response
                response = self.agent.model.invoke(messages)

                # Add the interpreted response to the message history
                interpreted_content = (
                    response.content if hasattr(response, "content") else str(response)
                )

                # Remove any think tags from the interpreted content
                interpreted_content = self._remove_think_tags(interpreted_content)

                # Add the formulated response to the message history
                message_history.append(
                    MessageResponse(
                        role="assistant",
                        content=interpreted_content,
                        tool_results=[
                            ToolResultResponse(
                                tool_name=result["tool_name"], result=result["result"]
                            )
                            for result in tool_results
                        ],
                    )
                )
            except Exception as e:
                logger.error(f"Error formatting tool results into response: {str(e)}")
                # Fallback: Add raw tool results if response formatting fails
                for result in tool_results:
                    message_history.append(
                        MessageResponse(
                            role="assistant",
                            content=result["result"],
                            tool_results=[
                                ToolResultResponse(
                                    tool_name=result["tool_name"], result=result["result"]
                                )
                            ],
                        )
                    )

            # Handle special image visualizer case
            if self.display_file_path:
                message_history.append(
                    MessageResponse(
                        role="assistant",
                        content={"path": self.display_file_path},
                    )
                )

        yield message_history, self.display_file_path, ""


# Initialize API handler
api_handler = APIHandler()


@app.post("/new_thread")
async def new_thread(request: ThreadCreateRequest = None):
    """
    Create a new chat thread

    Returns:
        JSONResponse: Response with the new thread ID
    """
    thread_id = str(uuid.uuid4())
    
    # Initialize thread data
    thread_data = api_handler.get_thread_data(thread_id)
    
    # Set authentication details if provided
    if request and request.bearer_token:
        api_handler.set_thread_auth(thread_id, request.bearer_token, request.api_base_url)
    
    # Set metadata if provided
    if request and request.metadata:
        thread_data["metadata"] = request.metadata

    return JSONResponse({"thread_id": thread_id})


@app.post("/chat/{thread_id}")
async def chat_with_thread(thread_id: str, request: MessageRequest):
    """
    Chat with a specific thread

    Args:
        thread_id (str): The thread ID to chat with
        request (MessageRequest): The message request containing the message and optional image data

    Returns:
        JSONResponse: Response with the chat result
    """
    # Set authentication details if provided
    if request.bearer_token:
        api_handler.set_thread_auth(thread_id, request.bearer_token, request.api_base_url)
    
    # Update thread activity
    thread_data = api_handler.get_thread_data(thread_id)
    thread_data["last_activity"] = datetime.now(timezone.utc)
    
    try:
        # Process the message using the existing process_message method
        async for response_chunk in api_handler.process_message(
            request.message, thread_id, request.image_data, request.image_type
        ):
            # Return the first meaningful response
            if response_chunk[0]:
                return JSONResponse({
                    "thread_id": thread_id,
                    "message": {
                        "role": response_chunk[0][0].role,
                        "content": response_chunk[0][0].content
                    },
                    "status": "success"
                })

        # If no meaningful response was generated
        return JSONResponse({
            "thread_id": thread_id,
            "message": {
                "role": "assistant",
                "content": "No response generated"
            },
            "status": "success"
        })

    except Exception as e:
        logger.error(f"Error in chat endpoint: {str(e)}")
        return JSONResponse({
            "thread_id": thread_id,
            "message": {
                "role": "assistant",
                "content": f"Error processing your message: {str(e)}"
            },
            "status": "error",
            "error": str(e)
        })


@app.post("/dicom/{thread_id}")
async def upload_dicom_payload(thread_id: str, request: DicomPayloadRequest):
    """
    Upload a DICOM payload to a specific thread

    Args:
        thread_id (str): The thread ID to upload the payload to
        request (DicomPayloadRequest): The DICOM payload request

    Returns:
        JSONResponse: Response with the upload result
    """
    # Set authentication details if provided
    if request.bearer_token:
        api_handler.set_thread_auth(thread_id, request.bearer_token, request.api_base_url)
    
    # Update thread activity
    thread_data = api_handler.get_thread_data(thread_id)
    thread_data["last_activity"] = datetime.now(timezone.utc)
    
    try:
        # Handle the DICOM payload using the existing handle_studies_payload method
        result = api_handler.handle_studies_payload(request.payload, thread_id)
        
        return JSONResponse({
            "thread_id": thread_id,
            "status": "success",
            "message": "DICOM payload uploaded successfully"
        })
    
    except Exception as e:
        logger.error(f"Error handling DICOM payload: {str(e)}")
        return JSONResponse({
            "thread_id": thread_id,
            "status": "error",
            "message": f"Error processing DICOM payload: {str(e)}"
        }, status_code=500)


@app.get("/threads/{thread_id}")
async def get_thread_info(thread_id: str, bearer_token: str = None, api_base_url: str = None):
    """
    Get information about a specific thread

    Args:
        thread_id (str): The thread ID to get information about
        bearer_token (str, optional): Bearer token for authentication
        api_base_url (str, optional): API base URL for callbacks

    Returns:
        JSONResponse: Response with thread information
    """
    # Set authentication details if provided
    if bearer_token:
        api_handler.set_thread_auth(thread_id, bearer_token, api_base_url)
    
    # Get thread data
    thread_data = api_handler.get_thread_data(thread_id)
    thread_data["last_activity"] = datetime.now(timezone.utc)
    
    # Convert datetime objects to ISO format strings for JSON serialization
    created_at = thread_data.get("created_at")
    last_activity = thread_data.get("last_activity")
    
    return JSONResponse({
        "thread_id": thread_id,
        "has_dicom_payload": thread_data.get("has_dicom_payload", False),
        "studies_count": len(thread_data.get("studies", [])),
        "studies": thread_data.get("studies", []),
        "created_at": created_at.isoformat() if created_at else None,
        "last_activity": last_activity.isoformat() if last_activity else None,
        "status": "success"
    })


@app.post("/refresh_tools")
async def refresh_tools():
    """
    Manually trigger a refresh of the agent's tools

    Returns:
        JSONResponse: Response indicating success or failure
    """
    try:
        api_handler._refresh_tools()

        return JSONResponse(
            {
                "status": "success",
                "message": f"Tools refreshed successfully. Agent now has {len(api_handler.tools_dict)} tools.",
            }
        )
    except Exception as e:
        logger.error(f"Error refreshing tools: {str(e)}")
        return JSONResponse(
            {"status": "error", "message": f"Error refreshing tools: {str(e)}"}, status_code=500
        )
