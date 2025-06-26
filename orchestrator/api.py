import json
import os
import re
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


class DicomPayloadRequest(BaseModel):
    payload: dict[str, Any]
    thread_id: str | None = None


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

    def _cleanup_inactive_threads(self):
        """Remove threads that haven't been accessed in the last 5 minutes."""
        try:
            current_time = datetime.now(tz=timezone.utc)
            inactive_threshold = current_time - timedelta(minutes=5)

            # Identify inactive threads
            inactive_threads = [
                thread_id
                for thread_id, data in self.thread_data.items()
                if data.get("last_accessed", datetime.min.replace(tzinfo=timezone.utc))
                < inactive_threshold
            ]

            # Remove inactive threads
            for thread_id in inactive_threads:
                del self.thread_data[thread_id]

            if inactive_threads:
                logger.info(f"Cleaned up {len(inactive_threads)} inactive threads")
        except Exception as e:
            logger.error(f"Error cleaning up inactive threads: {str(e)}")

    def get_thread_data(self, thread_id: str) -> dict:
        """
        Get thread-specific data or initialize if it doesn't exist

        Args:
            thread_id (str): The ID of the thread

        Returns:
            Dict: Thread-specific data dictionary
        """
        if thread_id not in self.thread_data:
            self.thread_data[thread_id] = {
                "dicom_payload": None,
                "last_accessed": datetime.now(tz=timezone.utc),
                "context_messages": [],
            }
        else:
            # Update the last accessed timestamp
            self.thread_data[thread_id]["last_accessed"] = datetime.now(tz=timezone.utc)

        return self.thread_data[thread_id]

    def handle_dicom_payload(self, payload: dict[str, Any], thread_id: str) -> dict[str, Any]:
        """
        Store DICOM payload for a specific thread and add context message

        Args:
            payload (Dict[str, Any]): The DICOM payload to store
            thread_id (str): The ID of the thread

        Returns:
            Dict[str, Any]: Response indicating success or failure
        """
        thread_data = self.get_thread_data(thread_id)

        logger.info(f"Handling DICOM payload for thread {thread_id}")

        # Store the DICOM payload in thread data
        thread_data["dicom_payload"] = payload

        # Add context message to inform the LLM that DICOM data is now available
        context_message = {
            "role": "system",
            "content": [
                {
                    "type": "text",
                    "text": "DICOM data has been uploaded and is now available for analysis. You can use the available tools to analyze the medical imaging data when you think it's appropriate.",
                }
            ],
        }

        # Add the context message to the thread's context messages
        thread_data["context_messages"].append(context_message)
        logger.info(f"Added DICOM context message to thread {thread_id}")

        return {
            "status": "success",
            "message": "DICOM payload received and stored successfully",
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

    async def process_message(self, message: str | None, thread_id: str) -> AsyncIterator[tuple]:
        """
        Process a message and generate responses.

        Args:
            message (Optional[str]): User message to process
            thread_id (str): ID of the current thread

        Yields:
            Tuple[List[MessageResponse], Optional[str], str]: Updated message history, display path, and empty string
        """
        # Initialize processing variables
        message_history = []
        self.current_thread_id = thread_id
        self.display_file_path = None

        # Get thread data and DICOM payload
        thread_data = self.get_thread_data(thread_id)
        dicom_payload = thread_data.get("dicom_payload")
        context_messages = thread_data.get("context_messages", [])

        logger.info(f"Processing message for thread {thread_id}")

        if dicom_payload is not None:
            logger.debug(f"Thread has DICOM payload of type {type(dicom_payload)}")

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
            messages.append({"role": "user", "content": [{"type": "text", "text": message}]})

        # Create the initial state and configuration
        initial_state = {"messages": messages}
        if dicom_payload is not None:
            initial_state["dicom_payload"] = dicom_payload

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


@app.post("/dicom/{thread_id}")
async def upload_dicom_payload(thread_id: str, request: DicomPayloadRequest):
    """
    Upload a DICOM payload for a specific thread

    Args:
        thread_id (str): Thread ID
        request (DicomPayloadRequest): DICOM payload request

    Returns:
        JSONResponse: Response indicating success or failure
    """
    if not thread_id:
        thread_id = str(uuid.uuid4())

    try:
        # Process the DICOM payload
        api_handler.handle_dicom_payload(request.payload, thread_id)

        return JSONResponse(
            {
                "thread_id": thread_id,
                "status": "success",
                "message": "DICOM payload stored successfully",
            }
        )
    except Exception as e:
        logger.error(f"Error handling DICOM payload: {str(e)}")
        return JSONResponse(
            {
                "thread_id": thread_id,
                "status": "error",
                "message": f"Error processing DICOM payload: {str(e)}",
            },
            status_code=500,
        )


@app.post("/chat/{thread_id}")
async def chat(thread_id: str, request: MessageRequest):
    """
    Send a message to the agent and get a response

    Args:
        thread_id (str): Thread ID
        request (MessageRequest): Message request

    Returns:
        ChatResponse: Response from the agent
    """
    # Generate a thread ID if not provided
    if not thread_id:
        thread_id = str(uuid.uuid4())

    try:
        # Get the first yielded response from the generator
        async for response_chunk in api_handler.process_message(request.message, thread_id):
            # Return the first meaningful response
            if response_chunk[0]:
                return ChatResponse(
                    thread_id=thread_id, message=response_chunk[0][0], status="success"
                )

        # If no meaningful response was generated
        return ChatResponse(
            thread_id=thread_id,
            message=MessageResponse(role="assistant", content="No response generated"),
            status="success",
        )

    except Exception as e:
        logger.error(f"Error in chat endpoint: {str(e)}")
        return ChatResponse(
            thread_id=thread_id,
            message=MessageResponse(
                role="assistant", content=f"Error processing your message: {str(e)}"
            ),
            status="error",
            error=str(e),
        )


@app.post("/new_thread")
async def new_thread():
    """
    Create a new chat thread

    Returns:
        JSONResponse: Response with the new thread ID
    """
    thread_id = str(uuid.uuid4())
    # Initialize thread data
    api_handler.get_thread_data(thread_id)

    return JSONResponse({"thread_id": thread_id})


@app.get("/threads/{thread_id}")
async def get_thread_info(thread_id: str):
    """
    Get information about a specific thread

    Args:
        thread_id (str): Thread ID

    Returns:
        JSONResponse: Thread information
    """
    try:
        thread_data = api_handler.get_thread_data(thread_id)

        return JSONResponse(
            {
                "thread_id": thread_id,
                "has_dicom_payload": thread_data.get("dicom_payload") is not None,
                "context_messages_count": len(thread_data.get("context_messages", [])),
            }
        )
    except Exception as e:
        logger.error(f"Error getting thread info: {str(e)}")
        return JSONResponse(
            {"thread_id": thread_id, "status": "error", "error": str(e)}, status_code=500
        )


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
