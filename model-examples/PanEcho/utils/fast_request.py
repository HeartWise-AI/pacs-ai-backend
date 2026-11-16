"""Low-overhead handling of the PanEcho predict request body.

An echo study arrives as one JSON document with a base64 DICOM per instance; a heavy study is
about 2 GB of text. Letting FastAPI build the request costs ``json.loads`` on the whole body and
the previous code then base64-decoded every instance twice (once only to validate it); together
that was about three quarters of the request time. This module instead

* reads the body into a single buffer (no chunk list plus joined copy);
* replaces every large string literal with a short token before ``json.loads`` runs, so the
  JSON parser and pydantic only see a document of a few kilobytes, and keeps a zero-copy view
  of each replaced literal;
* decodes base64 once, with the SIMD ``pybase64`` decoder when it is installed.

The decoded bytes are the same as before; requests that the fast path cannot represent exactly
(any string with a backslash escape) are parsed by ``json.loads`` as before.
"""

from __future__ import annotations

import base64
import json
import secrets
from typing import Any, Dict, Optional, Tuple, Union

from fastapi.exceptions import RequestValidationError
from pydantic import PrivateAttr, ValidationError
from starlette.requests import Request

from utils.http_utils import PredictRequest

try:  # optional accelerator; the standard library gives the same bytes
    import pybase64
except ImportError:  # pragma: no cover - exercised only without the wheel
    pybase64 = None

# String literals at least this long are kept out of the JSON parser.
LARGE_STRING_BYTES = 4096

Payload = Union[str, bytes, bytearray, memoryview]


class PredictRequestWithPayloads(PredictRequest):
    """``PredictRequest`` whose large strings are tokens resolved through ``payload``."""

    _payloads: Dict[str, memoryview] = PrivateAttr(default_factory=dict)

    def payload(self, value: Any) -> Any:
        """Return the base64 text behind ``value`` (a view of the body for a token)."""
        if isinstance(value, str) and self._payloads:
            return self._payloads.get(value, value)
        return value

    def release_payloads(self) -> None:
        """Drop the views so the request body can be freed before inference starts."""
        self._payloads = {}


def decode_base64(payload: Payload) -> Optional[bytes]:
    """Decode base64 once; ``None`` when the text is not valid base64.

    Accepts exactly what ``base64.b64decode(payload)`` accepts and returns the same bytes. The
    strict SIMD decoder handles well-formed input; anything it rejects (for example embedded
    line breaks) is retried with the lenient standard-library decoder used previously.
    """
    if pybase64 is not None:
        try:
            return pybase64.b64decode(payload, validate=True)
        except Exception:
            pass
    try:
        return base64.b64decode(payload)
    except Exception:
        return None


def split_large_strings(
    body: Union[bytes, bytearray], min_bytes: int = LARGE_STRING_BYTES
) -> Optional[Tuple[bytes, Dict[str, memoryview]]]:
    """Replace large JSON string literals in ``body`` with tokens.

    Returns the reduced document and ``{token: view of the literal's content}``, or ``None``
    when ``body`` contains a backslash escape inside a string. Without escapes a string ends
    at the next quote, so walking quote to quote from the start of the document finds every
    string token exactly; with an escape that rule no longer holds and the caller must use a
    real JSON parser.
    """
    view = memoryview(body)
    find = body.find
    nonce = secrets.token_hex(8)
    pieces = []
    payloads: Dict[str, memoryview] = {}
    copied_to = 0
    position = 0
    while True:
        start = find(b'"', position)
        if start < 0:
            break
        end = find(b'"', start + 1)
        if end < 0:
            return None
        if find(b"\\", start + 1, end) >= 0:
            return None
        if end - start - 1 >= min_bytes:
            token = f"pacs-ai-payload:{nonce}:{len(payloads)}"
            payloads[token] = view[start + 1:end]
            pieces.append(bytes(view[copied_to:start + 1]))
            pieces.append(token.encode("ascii"))
            copied_to = end
        position = end + 1
    pieces.append(bytes(view[copied_to:]))
    return b"".join(pieces), payloads


def _json_invalid(error: json.JSONDecodeError) -> RequestValidationError:
    return RequestValidationError(
        [
            {
                "type": "json_invalid",
                "loc": ("body", error.pos),
                "msg": "JSON decode error",
                "input": {},
                "ctx": {"error": error.msg},
            }
        ]
    )


def parse_predict_request(body: Union[bytes, bytearray]) -> PredictRequestWithPayloads:
    """Build the predict request from a raw JSON body.

    Raises ``RequestValidationError`` (FastAPI answers 422) for malformed JSON or a document
    that does not match ``PredictRequest``, as the framework did when it parsed the body.
    """
    split = split_large_strings(body)
    document, payloads = split if split is not None else (body, {})
    try:
        data = json.loads(document)
    except json.JSONDecodeError as error:
        raise _json_invalid(error) from error
    except UnicodeDecodeError as error:
        raise _json_invalid(json.JSONDecodeError(str(error), "", 0)) from error
    try:
        request = PredictRequestWithPayloads.model_validate(data)
    except ValidationError as error:
        raise RequestValidationError(
            [{**item, "loc": ("body", *item["loc"])} for item in error.errors(include_url=False)]
        ) from error
    request._payloads = payloads
    return request


async def read_body(request: Request) -> Union[bytes, bytearray]:
    """Read the request body into one buffer.

    With a ``Content-Length`` the buffer is allocated once and filled in place, so the body is
    never held as both a list of chunks and their concatenation.
    """
    try:
        expected = int(request.headers.get("content-length", ""))
    except ValueError:
        expected = -1
    if expected <= 0:
        return await request.body()

    buffer = bytearray(expected)
    view = memoryview(buffer)
    filled = 0
    overflow = bytearray()
    async for chunk in request.stream():
        size = len(chunk)
        if not overflow and filled + size <= expected:
            view[filled:filled + size] = chunk
            filled += size
        else:  # more data than declared; not produced by an HTTP server, kept for safety
            overflow += chunk
    view.release()
    if filled < expected:
        del buffer[filled:]
    if overflow:
        buffer += overflow
    return buffer
