import os
import warnings
from typing import *
from dotenv import load_dotenv
from transformers import logging as tf_logging
import uvicorn

from logger import logger, setup_logger

def configure_environment():
    """Configure the environment and load environment variables."""
    # Load environment variables
    _ = load_dotenv()
    
    # Suppress warnings and set logging level
    warnings.filterwarnings("ignore")
    tf_logging.set_verbosity_error()
    
    # Configure logging level based on environment or default to INFO
    log_level = os.getenv("LOG_LEVEL", "INFO")
    setup_logger(log_level=log_level)
    
    # Log application startup
    logger.info("Environment configured")

def get_server_config():
    """Get server configuration from environment variables."""
    host = os.getenv("HOST", "0.0.0.0")
    port = int(os.getenv("PORT", "8585"))
    reload = os.getenv("RELOAD", "True").lower() == "true"
    
    return {
        "host": host,
        "port": port,
        "reload": reload
    }

def main():
    """
    Main entry point for the Orchestrator application.
    Initializes environment and starts the FastAPI server.
    """
    try:
        # Configure the environment
        configure_environment()
        
        # Get server configuration
        config = get_server_config()
        
        # Log startup information
        logger.info(f"Starting Orchestrator API server on {config['host']}:{config['port']}")
        logger.info(f"Auto-reload is {'enabled' if config['reload'] else 'disabled'}")
        
        # Start the FastAPI server
        uvicorn.run(
            "api:app", 
            host=config["host"], 
            port=config["port"], 
            reload=config["reload"]
        )
        
    except Exception as e:
        logger.critical(f"Failed to start server: {str(e)}")
        # Exit with error code
        exit(1)

if __name__ == "__main__":
    main()
