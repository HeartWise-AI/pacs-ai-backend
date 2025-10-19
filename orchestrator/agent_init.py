import os
import re

from components.agent import Agent
from components.tools.docker_utils import discover_and_create_tools
from components.utils import load_prompts_from_file
from langchain_core.tools import BaseTool
from langchain_ollama import ChatOllama
from langgraph.checkpoint.memory import MemorySaver
from logger import logger

# Add OpenAI import
try:
    from langchain_openai import ChatOpenAI
    OPENAI_AVAILABLE = True
except ImportError:
    OPENAI_AVAILABLE = False
    logger.warning("langchain_openai not available. Install with: pip install langchain-openai")


def get_model_config():
    """
    Get model configuration based on environment variables.
    Supports easy switching between qwen3 and OpenAI models.
    """
    # Check if ACTIVE_MODEL is set for easy switching
    active_model = os.getenv("MODEL", "").lower()
    
    if "qwen" in active_model:
        return {
            "model": os.getenv("MODEL"),
            "base_url": os.getenv("BASE_URL"),
            "model_type": "ollama"
        }
    else:
        return {
            "model": os.getenv("MODEL"),
            "api_key": os.getenv("OPENAI_API_KEY"),
            "model_type": "openai"
        }


def initialize_agent(
    prompt_file: str,
    checkpointer: MemorySaver,
    model: str = None,
    temperature: float = 0.7,
    top_p: float = 0.95,
    base_url: str = None,
    network_name: str = "pacs-net",
) -> tuple[Agent, dict[str, BaseTool]]:
    """
    Initialize the Orchestrator agent with tools discovered from Docker containers.
    Supports both Ollama and OpenAI models with automatic configuration.

    Args:
        prompt_file (str): Path to file containing system prompts
        model (str, optional): Model to use. If None, uses environment configuration.
        temperature (float, optional): Temperature for the model. Defaults to 0.7.
        top_p (float, optional): Top P for the model. Defaults to 0.95.
        base_url (str, optional): Base URL for Ollama. If None, uses environment configuration.
        network_name (str, optional): Docker network name to discover containers from.

    Returns:
        Tuple[Agent, Dict[str, BaseTool]]: Initialized agent and dictionary of tool instances
    """
    # Load system prompts
    try:
        prompts = load_prompts_from_file(prompt_file)
        system_prompt = prompts.get("MEDICAL_ASSISTANT", "")
        if not system_prompt:
            logger.warning(
                f"No MEDICAL_ASSISTANT prompt found in {prompt_file}, using empty prompt"
            )
    except Exception as e:
        logger.error(f"Failed to load prompts from {prompt_file}: {str(e)}")
        system_prompt = ""

    logger.info(f"Discovering tools from Docker network: {network_name}")

    # Discover and create tools from Docker containers
    tools_dict = discover_and_create_tools(network_name)

    # Verify all tool names match OpenAI's pattern requirement
    sanitized_tools_dict = {}
    for tool in tools_dict.values():
        # Apply OpenAI's pattern requirement (^[a-zA-Z0-9_-]+$)
        sanitized_name = re.sub(r"[^a-zA-Z0-9_-]", "_", tool.name)

        # Ensure the name doesn't start with a number
        if sanitized_name and sanitized_name[0].isdigit():
            sanitized_name = f"tool_{sanitized_name}"

        # Apply the sanitized name
        if sanitized_name != tool.name:
            logger.info(f"Sanitized tool name: '{tool.name}' -> '{sanitized_name}'")
            tool.name = sanitized_name

        sanitized_tools_dict[sanitized_name] = tool

    # Log discovered tools
    logger.info(f"Initialized {len(sanitized_tools_dict)} tools")
    for tool_name, tool in sanitized_tools_dict.items():
        logger.info(f"Tool: {tool_name} - {tool.description}")

    # Get model configuration
    config = get_model_config()
    
    # Override with parameters if provided
    if model:
        config["model"] = model
    if base_url:
        config["base_url"] = base_url

    # Initialize the appropriate model
    if config["model_type"] == "openai":
        if not OPENAI_AVAILABLE:
            raise ImportError("langchain_openai is required for OpenAI models. Install with: pip install langchain-openai")
        
        api_key = config.get("api_key")
        if not api_key:
            raise ValueError("OPENAI_API_KEY environment variable is required for OpenAI models")
        
        logger.info(f"Initializing OpenAI model: {config['model']}")
        model_instance = ChatOpenAI(
            model=config["model"],
            api_key=api_key
        )
    else:
        # Default to Ollama
        logger.info(f"Initializing Ollama model: {config['model']} at {config['base_url']}")
        model_instance = ChatOllama(
            model=config["model"],
            temperature=temperature,
            top_p=top_p,
            base_url=config["base_url"]
        )

    agent = Agent(
        model_instance,
        tools=list(sanitized_tools_dict.values()),
        log_tools=True,
        log_dir="logs",
        system_prompt=system_prompt,
        checkpointer=checkpointer,
    )

    logger.info(f"Agent initialized successfully with {config['model_type']} model: {config['model']}")
    return agent, sanitized_tools_dict
