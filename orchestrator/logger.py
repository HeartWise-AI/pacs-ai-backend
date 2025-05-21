import logging
import os
import sys
from pathlib import Path

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
        formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
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

# Create the default logger
logger = setup_logger() 