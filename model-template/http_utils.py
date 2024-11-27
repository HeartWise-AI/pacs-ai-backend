from fastapi import FastAPI, Request, Response, staticfiles
from fastapi.responses import JSONResponse, RedirectResponse
from typing import Any, Optional
import os
from pathlib import Path

class HTTPResponse:
    def __init__(
        self, 
        status: int, 
        success: bool, 
        message: str, 
        data: Optional[Any] = None, 
        error_code: Optional[Any] = None
    ):
        self.status = status
        self.success = success
        self.message = message
        self.error_code = error_code
        self.data = data if data is not None else {}

    def to_json_response(self) -> JSONResponse:
        response_data = {
            "success": self.success,
            "message": self.message,
            "data": self.data
        }
        
        if self.error_code is not None:
            response_data["errorCode"] = self.error_code

        return JSONResponse(
            content=response_data,
            status_code=self.status
        )

def setup_file_server(app: FastAPI, path: str, root_dir: str):
    """
    Sets up a static file server for the specified path and root directory.
    
    Args:
        app: FastAPI application instance
        path: URL path to serve files from
        root_dir: Directory containing the static files
    """
    if any(char in path for char in "{}*"):
        raise ValueError("FileServer does not permit any URL parameters.")

    # Ensure path starts with /
    if not path.startswith('/'):
        path = '/' + path

    # Handle trailing slash redirect
    if path != "/" and not path.endswith('/'):
        @app.get(path)
        async def redirect(request: Request):
            return RedirectResponse(url=f"{path}/", status_code=301)
        path += '/'

    # Mount the static directory
    static_path = path.rstrip('/')
    app.mount(
        static_path,
        staticfiles.StaticFiles(directory=root_dir),
        name=f"static_{Path(root_dir).name}"
    )

# Example usage in main.py:
"""
from fastapi import FastAPI
from http_utils import HTTPResponse, setup_file_server
import os

app = FastAPI()

# Setup static file serving for docs
docs_dir = os.path.join(os.getcwd(), "docs")
setup_file_server(app, "/docs", docs_dir)

@app.get("/example")
async def example_endpoint():
    response = HTTPResponse(
        status=200,
        success=True,
        message="Success",
        data={"key": "value"},
        error_code=None
    )
    return response.to_json_response()
"""