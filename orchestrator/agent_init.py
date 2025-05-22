import os
import re
from typing import Tuple, Dict, Optional
from langchain_ollama import ChatOllama
from langgraph.checkpoint.memory import MemorySaver
from langchain_core.tools import BaseTool

from components.agent import Agent
from components.tools.docker_utils import discover_and_create_tools
from components.utils import load_prompts_from_file
from logger import logger

def initialize_agent(
    prompt_file: str,
    model: str = "qwen3:8b",
    temperature: float = 0.7,
    top_p: float = 0.95,
    base_url: str = "http://ollama:11434",
    network_name: str = "pacs-net"
) -> Tuple[Agent, Dict[str, BaseTool]]:
    """
    Initialize the Orchestrator agent with tools discovered from Docker containers.

    Args:
        prompt_file (str): Path to file containing system prompts
        model (str, optional): Model to use. Defaults to "gpt-4o".
        temperature (float, optional): Temperature for the model. Defaults to 0.7.
        top_p (float, optional): Top P for the model. Defaults to 0.95.
        openai_kwargs (Dict[str, str], optional): Additional keyword arguments for OpenAI API.
        network_name (str, optional): Docker network name to discover containers from.

    Returns:
        Tuple[Agent, Dict[str, BaseTool]]: Initialized agent and dictionary of tool instances
    """
    # Load system prompts
    try:
        prompts = load_prompts_from_file(prompt_file)
        system_prompt = prompts.get("MEDICAL_ASSISTANT", "")
        if not system_prompt:
            logger.warning(f"No MEDICAL_ASSISTANT prompt found in {prompt_file}, using empty prompt")
    except Exception as e:
        logger.error(f"Failed to load prompts from {prompt_file}: {str(e)}")
        system_prompt = ""
    
    logger.info(f"Discovering tools from Docker network: {network_name}")
    
    # Discover and create tools from Docker containers
    tools_dict = discover_and_create_tools(network_name)
    
    # Verify all tool names match OpenAI's pattern requirement
    sanitized_tools_dict = {}
    for tool_name, tool in tools_dict.items():
        # Apply OpenAI's pattern requirement (^[a-zA-Z0-9_-]+$)
        sanitized_name = re.sub(r'[^a-zA-Z0-9_-]', '_', tool.name)
        
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

    config ={}
    config["base_url"] = base_url

    # Initialize the model and agent
    checkpointer = MemorySaver()
    model_instance = ChatOllama(
        model=model, 
        temperature=temperature, 
        top_p=top_p, 
        **config
    )
    
    agent = Agent(
        model_instance,
        tools=list(sanitized_tools_dict.values()),
        log_tools=True,
        log_dir="logs",
        system_prompt=system_prompt,
        checkpointer=checkpointer,
    )

    logger.info("Agent initialized successfully")
    return agent, sanitized_tools_dict 