import logging
import os
import sys
from pathlib import Path
import json
from typing import Any, Dict, List


def sanitize_for_logging(data: Any) -> Any:
    """
    Sanitize data for logging by replacing sensitive data with dummy strings
    and handling various data types recursively.
    
    Args:
        data: Data to sanitize (can be dict, list, or other types)
        
    Returns:
        Sanitized data with sensitive information replaced
    """
    if isinstance(data, dict):
        sanitized = {}
        for key, value in data.items():
            # Handle image data fields
            if key == "previewImageBase64" and value:
                sanitized[key] = "[IMAGE_DATA_REDACTED]"
            elif key == "image_data" and isinstance(value, str) and len(value) > 100:
                sanitized[key] = "[IMAGE_DATA_REDACTED]"
            # Handle base64 encoded data in general
            elif key.lower().endswith("base64") and isinstance(value, str) and len(value) > 100:
                sanitized[key] = "[BASE64_DATA_REDACTED]"
            # Handle tool calls arguments that might contain sensitive data
            elif key == "arguments" and isinstance(value, (dict, str)):
                if isinstance(value, str):
                    try:
                        # Try to parse as JSON and sanitize
                        parsed_args = json.loads(value)
                        sanitized_args = sanitize_for_logging(parsed_args)
                        sanitized[key] = json.dumps(sanitized_args)
                    except (json.JSONDecodeError, TypeError):
                        # If not JSON, check if it's a long string that might be sensitive
                        if len(value) > 1000:
                            sanitized[key] = "[LARGE_STRING_REDACTED]"
                        else:
                            sanitized[key] = value
                else:
                    sanitized[key] = sanitize_for_logging(value)
            # Handle any field that might contain large amounts of data
            elif isinstance(value, str) and len(value) > 5000:
                sanitized[key] = "[LARGE_CONTENT_REDACTED]"
            # Handle tool calls specifically
            elif key == "tool_calls" and isinstance(value, list):
                sanitized[key] = [sanitize_for_logging(tool_call) for tool_call in value]
            # Handle content fields that might have image URLs or large text
            elif key == "content" and isinstance(value, list):
                sanitized_content = []
                for item in value:
                    if isinstance(item, dict):
                        if item.get("type") == "image_url":
                            sanitized_item = item.copy()
                            if "image_url" in sanitized_item and "url" in sanitized_item["image_url"]:
                                sanitized_item["image_url"]["url"] = "[IMAGE_URL_REDACTED]"
                            sanitized_content.append(sanitized_item)
                        else:
                            sanitized_content.append(sanitize_for_logging(item))
                    else:
                        sanitized_content.append(sanitize_for_logging(item))
                sanitized[key] = sanitized_content
            else:
                sanitized[key] = sanitize_for_logging(value)
        return sanitized
    elif isinstance(data, list):
        return [sanitize_for_logging(item) for item in data]
    elif isinstance(data, str) and len(data) > 10000:
        # Handle very long strings that might be sensitive
        return "[VERY_LARGE_STRING_REDACTED]"
    else:
        return data


def setup_logger(name=None, log_level=None):
    """
    Configure and return a logger with the given name and log level.

    Args:
        name (str, optional): Logger name. Defaults to root logger.
        log_level (str, optional): Log level. Defaults to value from environment or INFO.

    Returns:
        logging.Logger: Configured logger instance
    """
    name = name or "Orchestrator"
    log_level = log_level or os.getenv("LOG_LEVEL", "INFO").upper()
    numeric_level = getattr(logging, log_level, logging.INFO)

    logger = logging.getLogger(name)
    logger.setLevel(numeric_level)

    # Avoid adding handlers multiple times
    if not logger.handlers:
        handler = logging.StreamHandler(sys.stdout)
        formatter = logging.Formatter("%(asctime)s - %(name)s - %(levelname)s - %(message)s")
        handler.setFormatter(formatter)
        logger.addHandler(handler)

        # Add file handler if LOG_FILE env var is set
        log_file = os.getenv("LOG_FILE")
        if log_file:
            log_path = Path(log_file)
            log_path.parent.mkdir(exist_ok=True, parents=True)
            file_handler = logging.FileHandler(log_file)
            file_handler.setFormatter(formatter)
            logger.addHandler(file_handler)

    return logger


class ChatLogger:
    """
    Enhanced logger class for tracking full chat conversations with sanitization.
    """
    
    def __init__(self, logger_instance: logging.Logger):
        self.logger = logger_instance
        self.chat_history: Dict[str, List[Dict[str, Any]]] = {}
    
    def log_chat_message(self, thread_id: str, role: str, content: Any, message_type: str = "chat"):
        """
        Log a chat message with sanitization.
        
        Args:
            thread_id: The thread ID for the conversation
            role: Role of the message sender (user, assistant, system, tool)
            content: The message content
            message_type: Type of message (chat, tool_call, system, etc.)
        """
        # Sanitize the content
        sanitized_content = sanitize_for_logging(content)
        
        # Create log entry
        log_entry = {
            "thread_id": thread_id,
            "role": role,
            "content": sanitized_content,
            "message_type": message_type,
            "timestamp": None  # Will be added by logging framework
        }
        
        # Add to chat history
        if thread_id not in self.chat_history:
            self.chat_history[thread_id] = []
        self.chat_history[thread_id].append(log_entry)
        
        # Log the message
        self.logger.info(f"CHAT [{thread_id}] {role.upper()}: {json.dumps(sanitized_content, ensure_ascii=False)}")
    
    def log_tool_execution(self, thread_id: str, tool_name: str, input_data: Any, output_data: Any):
        """
        Log tool execution with sanitized input and output data.
        
        Args:
            thread_id: The thread ID for the conversation
            tool_name: Name of the tool being executed
            input_data: Input data sent to the tool
            output_data: Output data returned by the tool
        """
        sanitized_input = sanitize_for_logging(input_data)
        sanitized_output = sanitize_for_logging(output_data)
        
        log_entry = {
            "thread_id": thread_id,
            "tool_name": tool_name,
            "input": sanitized_input,
            "output": sanitized_output,
            "message_type": "tool_execution"
        }
        
        # Add to chat history
        if thread_id not in self.chat_history:
            self.chat_history[thread_id] = []
        self.chat_history[thread_id].append(log_entry)
        
        # Log the tool execution
        self.logger.info(f"TOOL [{thread_id}] {tool_name}: INPUT={json.dumps(sanitized_input, ensure_ascii=False)}")
        self.logger.info(f"TOOL [{thread_id}] {tool_name}: OUTPUT={json.dumps(sanitized_output, ensure_ascii=False)}")
    
    def log_studies_payload(self, thread_id: str, studies: List[Dict[str, Any]]):
        """
        Log studies payload with sanitization.
        
        Args:
            thread_id: The thread ID for the conversation
            studies: List of study data
        """
        sanitized_studies = sanitize_for_logging(studies)
        
        log_entry = {
            "thread_id": thread_id,
            "studies": sanitized_studies,
            "message_type": "studies_payload"
        }
        
        # Add to chat history
        if thread_id not in self.chat_history:
            self.chat_history[thread_id] = []
        self.chat_history[thread_id].append(log_entry)
        
        self.logger.info(f"STUDIES [{thread_id}] Payload: {json.dumps(sanitized_studies, ensure_ascii=False)}")
    
    def get_chat_history(self, thread_id: str) -> List[Dict[str, Any]]:
        """
        Get the chat history for a specific thread.
        
        Args:
            thread_id: The thread ID
            
        Returns:
            List of chat history entries for the thread
        """
        return self.chat_history.get(thread_id, [])
    
    def clear_chat_history(self, thread_id: str):
        """
        Clear chat history for a specific thread.
        
        Args:
            thread_id: The thread ID to clear
        """
        if thread_id in self.chat_history:
            del self.chat_history[thread_id]
            self.logger.info(f"CHAT [{thread_id}] History cleared")


# Create the default logger
logger = setup_logger()

# Create the chat logger instance
chat_logger = ChatLogger(logger)
