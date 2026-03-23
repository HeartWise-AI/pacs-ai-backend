import json
import operator
from datetime import datetime, timezone
from pathlib import Path
from typing import Annotated, Any, TypedDict

from dotenv import load_dotenv
from langchain_core.language_models import BaseLanguageModel
from langchain_core.messages import AnyMessage, SystemMessage, ToolMessage
from langchain_core.tools import BaseTool
from langgraph.graph import END, StateGraph
from langgraph.prebuilt import ToolNode
from logger import logger, sanitize_for_logging

_ = load_dotenv()

# Token counting and context management
MAX_CONTEXT_TOKENS = 64_000  # Conservative limit for most models
SYSTEM_PROMPT_RESERVE = 500  # Reserve tokens for system prompt
RESPONSE_BUFFER = 1_000  # Reserve tokens for response generation


class ToolCallLog(TypedDict):
    """
    A TypedDict representing a log entry for a tool call.

    Attributes:
        timestamp (str): The timestamp of when the tool call was made.
        tool_call_id (str): The unique identifier for the tool call.
        name (str): The name of the tool that was called.
        args (Any): The arguments passed to the tool.
        content (str): The content or result of the tool call.
    """

    timestamp: str
    tool_call_id: str
    name: str
    args: Any
    content: str


class AgentState(TypedDict):
    """
    A TypedDict representing the state of an agent.

    Attributes:
        messages (Annotated[List[AnyMessage], operator.add]): A list of messages
            representing the conversation history. The operator.add annotation
            indicates that new messages should be appended to this list.
        studies (Optional[Dict[str, Any]]): The studies data, if available.
        last_tool_call (Optional[ToolCallLog]): The last tool call log, used to
            repopulate the studies when the agent is re-initialized.
        api_base_url (Optional[str]): The base URL for API requests, if available.
        bearer_token (Optional[str]): The bearer token for authentication, if available.
    """

    messages: Annotated[list[AnyMessage], operator.add]
    studies: Any = None
    last_tool_call: ToolCallLog | None = None
    api_base_url: str | None = None
    bearer_token: str | None = None


class Agent:
    """
    A class representing an agent that processes requests and executes tools based on
    language model responses.

    Attributes:
        model (BaseLanguageModel): The language model used for processing.
        tools (Dict[str, BaseTool]): A dictionary of available tools.
        checkpointer (Any): Manages and persists the agent's state.
        system_prompt (str): The system instructions for the agent.
        workflow (StateGraph): The compiled workflow for the agent's processing.
        log_tools (bool): Whether to log tool calls.
        log_path (Path): Path to save tool call logs.
    """

    def __init__(
        self,
        model: BaseLanguageModel,
        tools: list[BaseTool],
        checkpointer: Any = None,
        system_prompt: str = "",
        log_tools: bool = True,
        log_dir: str | None = "logs",
    ):
        """
        Initialize the Agent.

        Args:
            model (BaseLanguageModel): The language model to use.
            tools (List[BaseTool]): A list of available tools.
            checkpointer (Any, optional): State persistence manager. Defaults to None.
            system_prompt (str, optional): System instructions. Defaults to "".
            log_tools (bool, optional): Whether to log tool calls. Defaults to True.
            log_dir (str, optional): Directory to save logs. Defaults to 'logs'.
        """
        self.system_prompt = system_prompt
        self.log_tools = log_tools
        self.tools = {t.name: t for t in tools}
        self.model = model.bind_tools(tools)

        if self.log_tools:
            self.log_path = Path(log_dir or "logs")
            self.log_path.mkdir(exist_ok=True)

        # Define the agent workflow
        self.workflow = self._create_workflow(checkpointer)

    def _create_workflow(self, checkpointer):
        """Create the agent workflow graph."""
        workflow = StateGraph(AgentState)
        workflow.add_node("process", self.process_request)
        workflow.add_node("execute", self.execute_tools)
        workflow.add_conditional_edges(
            "process", self.has_tool_calls, {True: "execute", False: END}
        )
        workflow.add_edge("execute", "process")
        workflow.set_entry_point("process")

        return workflow.compile(checkpointer=checkpointer)

    def get_conversation_state(self, thread_id: str) -> dict[str, Any] | None:
        """
        Retrieve the conversation state for a given thread.

        Args:
            thread_id (str): The thread identifier

        Returns:
            Optional[Dict[str, Any]]: The conversation state or None if not found
        """
        try:
            config = {"configurable": {"thread_id": thread_id}}
            state = self.workflow.get_state(config)
            return state.values if state else None
        except Exception as e:
            logger.error(f"Error retrieving conversation state for thread {thread_id}: {e}")
            return None

    def update_conversation_state(self, thread_id: str, state_update: dict[str, Any]) -> bool:
        """
        Update the conversation state for a given thread.

        Args:
            thread_id (str): The thread identifier
            state_update (Dict[str, Any]): The state updates to apply

        Returns:
            bool: True if successful, False otherwise
        """
        try:
            config = {"configurable": {"thread_id": thread_id}}
            self.workflow.update_state(config, state_update)
            return True
        except Exception as e:
            logger.error(f"Error updating conversation state for thread {thread_id}: {e}")
            return False

    def _estimate_tokens(self, text: str) -> int:
        """Rough token estimation (1 token ≈ 4 chars for most models)."""
        return len(text) // 4

    def _get_message_tokens(self, message: AnyMessage) -> int:
        """Estimate tokens for a message."""
        if hasattr(message, "content") and message.content:
            if isinstance(message.content, str):
                return self._estimate_tokens(message.content)
            if isinstance(message.content, list):
                total = 0
                for item in message.content:
                    if isinstance(item, dict) and "text" in item:
                        total += self._estimate_tokens(item["text"])
                return total
        return 0

    def _truncate_messages(self, messages: list[AnyMessage]) -> list[AnyMessage]:
        """Truncate messages to fit within context window while preserving recent context."""
        if not messages:
            return messages

        # Always keep system message if present
        system_msgs = [msg for msg in messages if isinstance(msg, SystemMessage)]
        other_msgs = [msg for msg in messages if not isinstance(msg, SystemMessage)]

        # Calculate system prompt tokens
        system_tokens = sum(self._get_message_tokens(msg) for msg in system_msgs)
        available_tokens = MAX_CONTEXT_TOKENS - system_tokens - RESPONSE_BUFFER

        if available_tokens <= 0:
            logger.warning("System prompt too long, truncating other messages severely")
            return system_msgs + other_msgs[-1:] if other_msgs else system_msgs

        # Keep recent messages that fit in available context
        truncated_msgs = []
        current_tokens = 0

        # Process messages in reverse order to keep most recent
        for msg in reversed(other_msgs):
            msg_tokens = self._get_message_tokens(msg)
            if current_tokens + msg_tokens <= available_tokens:
                truncated_msgs.insert(0, msg)
                current_tokens += msg_tokens
            else:
                break

        if len(truncated_msgs) < len(other_msgs):
            logger.info(
                f"Truncated {len(other_msgs) - len(truncated_msgs)} older messages to fit context window"
            )

        return system_msgs + truncated_msgs

    def process_request(self, state: AgentState) -> dict[str, list[AnyMessage]]:
        """
        Process the request using the language model with proper context management.

        Args:
            state (AgentState): The current state of the agent.

        Returns:
            Dict[str, List[AnyMessage]]: A dictionary containing the model's response.
        """
        # Repopulate studies from last_tool_call if it's missing
        if state.get("studies") is None and state.get("last_tool_call"):
            last_tool_call = state["last_tool_call"]
            if last_tool_call and "content" in last_tool_call:
                state["studies"] = last_tool_call["content"]
                logger.info("Repopulated studies from last tool call")

        messages = state["messages"]

        # Add system prompt only if not already present
        has_system_prompt = any(isinstance(msg, SystemMessage) for msg in messages)
        if self.system_prompt and not has_system_prompt:
            messages = [SystemMessage(content=self.system_prompt)] + messages

        # Truncate messages to fit context window
        messages = self._truncate_messages(messages)

        # Log context info
        total_tokens = sum(self._get_message_tokens(msg) for msg in messages)
        logger.debug(f"Processing request with {len(messages)} messages, ~{total_tokens} tokens")

        response = self.model.invoke(messages)
        return {"messages": [response]}

    def has_tool_calls(self, state: AgentState) -> bool:
        """
        Check if the response contains any tool calls.

        Args:
            state (AgentState): The current state of the agent.

        Returns:
            bool: True if tool calls exist, False otherwise.
        """
        response = state["messages"][-1]
        return len(response.tool_calls) > 0

    def _execute_single_tool(self, call, studies, api_base_url=None, bearer_token=None):
        """Execute a single tool and return the result."""
        tool_name = call["name"]

        if tool_name not in self.tools:
            logger.warning(f"Invalid tool requested: {tool_name}")
            return "Invalid tool requested, please try again with a valid tool."

        tool = self.tools[tool_name]
        
        # Check if tool has modality requirements and if there are matching studies
        from components.tools.docker_utils import TOOL_MODALITY_MAPPING
        
        supported_modalities = TOOL_MODALITY_MAPPING.get(tool_name, [])
        
        # If tool has modality requirements, check if we have matching studies
        if supported_modalities and isinstance(studies, list):
            matching_studies = []
            for study in studies:
                study_modality = study.get("modality")
                if study_modality and study_modality in supported_modalities:
                    matching_studies.append(study)
            
            if not matching_studies:
                logger.warning(f"Tool {tool_name} requires modalities {supported_modalities} but no matching studies found")
                return f"Tool {tool_name} requires studies with modalities {', '.join(supported_modalities)}, but no matching studies are available. Available study modalities: {', '.join(set(study.get('modality', 'unknown') for study in studies))}"
            
            logger.info(f"Tool {tool_name} will process {len(matching_studies)} studies matching modalities: {', '.join(supported_modalities)}")
        elif supported_modalities:
            logger.info(f"Tool {tool_name} supports modalities {supported_modalities}, but studies format is not a list - will be filtered by tool")
        else:
            logger.info(f"Tool {tool_name} has no modality restrictions")

        logger.info(f"Executing tool: {tool_name}")

        try:
            tool_args = call.get("args", {})
            # Only add studies if the tool expects it
            if hasattr(tool, "args_schema") and hasattr(tool.args_schema, "model_fields"):
                if "studies" in tool.args_schema.model_fields:
                    tool_args["studies"] = studies
                # Add api_base_url if the tool expects it
                if "api_base_url" in tool.args_schema.model_fields and api_base_url:
                    tool_args["api_base_url"] = api_base_url
                # Add bearer_token if the tool expects it
                if "bearer_token" in tool.args_schema.model_fields and bearer_token:
                    tool_args["bearer_token"] = bearer_token
            result = tool.invoke(tool_args)
            logger.info(f"Tool {tool_name} execution successful")
            return result
        except Exception as e:
            logger.error(f"Error executing tool {tool_name}: {str(e)}")
            return f"Error executing tool: {str(e)}"

    def execute_tools(self, state: AgentState) -> dict[str, list[ToolMessage]]:
        """
        Execute tool calls from the model's response.

        Args:
            state (AgentState): The current state of the agent.

        Returns:
            Dict[str, List[ToolMessage]]: A dictionary containing tool execution results.
        """
        tool_calls = state["messages"][-1].tool_calls
        results = []
        studies = state.get("studies")
        api_base_url = state.get("api_base_url")
        bearer_token = state.get("bearer_token")

        logger.debug(f"Studies type: {type(studies)}")
        if studies is not None and hasattr(studies, "keys"):
            # Sanitize studies before logging keys
            sanitized_studies = sanitize_for_logging(studies)
            logger.debug(f"Studies keys: {list(sanitized_studies.keys()) if hasattr(sanitized_studies, 'keys') else 'N/A'}")

        for call in tool_calls:
            logger.info(f"Processing tool call: {call['name']}")
            result = self._execute_single_tool(call, studies, api_base_url, bearer_token)

            # Create a log entry for the tool call
            tool_call_log: ToolCallLog = {
                "timestamp": datetime.now(tz=timezone.utc).isoformat(),
                "tool_call_id": call["id"],
                "name": call["name"],
                "args": call["args"],
                "content": result,
            }

            # Update the last_tool_call in the state
            state["last_tool_call"] = tool_call_log

            results.append(
                ToolMessage(
                    tool_call_id=call["id"],
                    name=call["name"],
                    args=call["args"],
                    content=str(result),
                )
            )

        if self.log_tools:
            self._save_tool_calls(results)

        logger.info("Tool execution complete, returning to model processing")
        return {"messages": results, "last_tool_call": state["last_tool_call"]}

    def _save_tool_calls(self, tool_calls: list[ToolMessage]) -> None:
        """
        Save tool calls to a JSON file with timestamp-based naming.

        Args:
            tool_calls (List[ToolMessage]): List of tool calls to save.
        """
        if not self.log_tools:
            return

        timestamp = datetime.now(tz=timezone.utc).strftime("%Y%m%d_%H%M%S")
        filename = self.log_path / f"tool_calls_{timestamp}.json"

        logs: list[ToolCallLog] = []
        for call in tool_calls:
            log_entry = {
                "tool_call_id": call.tool_call_id,
                "name": call.name,
                "args": call.args,
                "content": call.content,
                "timestamp": datetime.now(tz=timezone.utc).isoformat(),
            }
            # Sanitize the log entry before adding to logs
            sanitized_log_entry = sanitize_for_logging(log_entry)
            logs.append(sanitized_log_entry)

        try:
            with open(filename, "w") as f:
                json.dump(logs, f, indent=4)
            logger.info(f"Tool calls logged to {filename} (sanitized)")
        except Exception as e:
            logger.error(f"Failed to save tool call logs: {str(e)}")
