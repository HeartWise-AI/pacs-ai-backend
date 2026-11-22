"""Request-body fast path: large strings bypass the JSON parser and decode once.

Synthetic payloads only (random bytes and generated DICOM, no PHI).
"""

import asyncio
import base64
import json
import sys
from pathlib import Path

import numpy as np
import pytest
from fastapi.exceptions import RequestValidationError

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from utils import fast_request
from utils.fast_request import (
    LARGE_STRING_BYTES,
    decode_base64,
    parse_predict_request,
    read_body,
    split_large_strings,
)
from utils.http_utils import PredictRequest

RNG = np.random.default_rng(7)
BLOBS = [RNG.integers(0, 256, size, dtype=np.uint8).tobytes() for size in (9000, 30001, 12)]


def make_body(images, **extra) -> bytes:
    document = {"outputMode": "JSON", "seriesInstanceImages": images, **extra}
    return json.dumps(document).encode()


def b64(data: bytes) -> str:
    return base64.b64encode(data).decode()


def test_large_strings_are_replaced_by_views_of_the_body():
    body = make_body({"0": {"0": b64(BLOBS[0]), "1": b64(BLOBS[1]), "2": b64(BLOBS[2])}})

    document, payloads = split_large_strings(body)

    assert len(document) < 600
    assert len(payloads) == 2  # the 12-byte instance stays inline
    assert [bytes(view) for view in payloads.values()] == [b64(BLOBS[0]).encode(), b64(BLOBS[1]).encode()]
    assert all(view.obj is body for view in payloads.values())
    skeleton = json.loads(document)
    assert list(skeleton["seriesInstanceImages"]["0"].values())[:2] == list(payloads)


def test_fast_parse_matches_the_framework_parse():
    images = {"0": {"0": b64(BLOBS[0]), "1": b64(BLOBS[2])}, "3": {"5": b64(BLOBS[1])}}
    body = make_body(images, additionalMetadata={"k": {"predicted_class": "A4C"}}, seriesInstanceMetadata={})

    fast = parse_predict_request(body)
    reference = PredictRequest(**json.loads(body))

    assert fast.outputMode == reference.outputMode
    assert fast.additionalMetadata == reference.additionalMetadata
    assert fast.seriesInstanceMetadata == reference.seriesInstanceMetadata
    assert {s: list(i) for s, i in fast.seriesInstanceImages.items()} == {
        s: list(i) for s, i in reference.seriesInstanceImages.items()
    }
    for series, instances in reference.seriesInstanceImages.items():
        for instance, text in instances.items():
            payload = fast.payload(fast.seriesInstanceImages[series][instance])
            assert decode_base64(payload) == base64.b64decode(text)


def test_instance_objects_with_an_image_field_are_supported():
    body = make_body({"0": {"0": {"image": b64(BLOBS[1]), "view": "A4C"}}})

    request = parse_predict_request(body)
    instance = request.seriesInstanceImages[0][0]

    assert instance["view"] == "A4C"
    assert decode_base64(request.payload(instance["image"])) == BLOBS[1]


def test_release_payloads_drops_every_reference_to_the_body():
    body = bytearray(make_body({"0": {"0": b64(BLOBS[0])}}))
    request = parse_predict_request(body)
    token = request.seriesInstanceImages[0][0]
    assert isinstance(request.payload(token), memoryview)

    request.release_payloads()

    assert request.payload(token) == token
    body.clear()  # raises BufferError while a view of the body is still exported


def test_escaped_strings_use_the_standard_parser():
    body = make_body({"0": {"0": b64(BLOBS[0])}}, additionalMetadata={"note": 'a "quoted" \\ value'})
    assert split_large_strings(body) is None

    request = parse_predict_request(body)

    assert request.additionalMetadata == {"note": 'a "quoted" \\ value'}
    text = request.seriesInstanceImages[0][0]
    assert request.payload(text) is text
    assert decode_base64(text) == BLOBS[0]


def test_unterminated_string_is_left_to_the_standard_parser():
    assert split_large_strings(b'{"outputMode": "JSON') is None


@pytest.mark.parametrize(
    "body,error_type",
    [
        (b'{"outputMode": ', "json_invalid"),
        (b"\xff\xfe", "json_invalid"),
        (b'{"seriesInstanceImages": {}}', "missing"),
        (b'{"outputMode": "JSON", "seriesInstanceImages": {"x": {"0": "AAAA"}}}', "int_parsing"),
        (b"[1, 2]", "model_type"),
    ],
)
def test_invalid_requests_raise_the_framework_validation_error(body, error_type):
    with pytest.raises(RequestValidationError) as raised:
        parse_predict_request(body)

    errors = raised.value.errors()
    assert errors[0]["type"] == error_type
    assert errors[0]["loc"][0] == "body"


@pytest.mark.parametrize("payload_type", [str, bytes, memoryview])
def test_decode_base64_matches_the_standard_decoder(payload_type):
    text = b64(BLOBS[1])
    payload = text if payload_type is str else payload_type(text.encode())

    assert decode_base64(payload) == BLOBS[1]


def test_decode_base64_keeps_lenient_and_invalid_input_behaviour():
    wrapped = "\n".join(b64(BLOBS[0])[i:i + 76] for i in range(0, 12000, 76))
    assert decode_base64(wrapped) == base64.b64decode(wrapped) == BLOBS[0]
    assert decode_base64("QUJD!!") == base64.b64decode("QUJD!!")
    assert decode_base64("A") is None  # the standard decoder raises: incorrect padding
    assert decode_base64("QUJ") is None


def test_decode_base64_without_the_accelerator(monkeypatch):
    monkeypatch.setattr(fast_request, "pybase64", None)

    assert decode_base64(b64(BLOBS[1])) == BLOBS[1]
    assert decode_base64(memoryview(b64(BLOBS[0]).encode())) == BLOBS[0]
    assert decode_base64("A") is None


class _FakeRequest:
    def __init__(self, chunks, content_length=None):
        self._chunks = chunks
        self.headers = {} if content_length is None else {"content-length": str(content_length)}

    async def stream(self):
        for chunk in self._chunks:
            yield chunk

    async def body(self):
        return b"".join(self._chunks)


@pytest.mark.parametrize(
    "declared", [None, "exact", "short", "long", "invalid"], ids=lambda value: str(value)
)
def test_read_body_returns_exactly_the_bytes_sent(declared):
    chunks = [BLOBS[0][:5000], b"", BLOBS[0][5000:], BLOBS[2]]
    sent = b"".join(chunks)
    length = {None: None, "exact": len(sent), "short": len(sent) - 100, "long": len(sent) + 100, "invalid": "x"}[
        declared
    ]

    received = asyncio.run(read_body(_FakeRequest(chunks, length)))

    assert bytes(received) == sent


def test_extract_dicoms_decodes_each_instance_once(monkeypatch):
    import logic
    from logic import CustomPredictionService
    from pydicom.dataset import Dataset, FileMetaDataset
    from pydicom.uid import ExplicitVRLittleEndian, generate_uid
    from io import BytesIO

    def dicom_bytes(frames: int) -> bytes:
        ds = Dataset()
        ds.file_meta = FileMetaDataset()
        ds.file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
        ds.SOPClassUID = ds.file_meta.MediaStorageSOPClassUID = "1.2.840.10008.5.1.4.1.1.3.1"
        ds.SOPInstanceUID = ds.file_meta.MediaStorageSOPInstanceUID = generate_uid()
        ds.SeriesInstanceUID = generate_uid()
        ds.Rows, ds.Columns, ds.SamplesPerPixel = 40, 40, 3
        ds.PhotometricInterpretation, ds.PlanarConfiguration = "RGB", 0
        ds.BitsAllocated, ds.BitsStored, ds.HighBit, ds.PixelRepresentation = 8, 8, 7, 0
        if frames > 1:
            ds.NumberOfFrames = frames
        ds.PixelData = RNG.integers(0, 256, frames * 40 * 40 * 3, dtype=np.uint8).tobytes()
        buffer = BytesIO()
        ds.save_as(buffer, enforce_file_format=True)
        return buffer.getvalue()

    clip, still = dicom_bytes(4), dicom_bytes(1)
    body = make_body(
        {"0": {"0": b64(still), "1": b64(clip), "2": "not base64!", "3": b64(b"x" * 9000)}}
    )
    request = parse_predict_request(body)
    calls = []
    real_decode = logic.decode_base64
    monkeypatch.setattr(logic, "decode_base64", lambda payload: calls.append(1) or real_decode(payload))

    service = CustomPredictionService.__new__(CustomPredictionService)
    dicoms = service._extract_dicoms(request)

    assert len(calls) == 4
    assert [int(d.NumberOfFrames) for d in dicoms] == [4]
    assert all(bytes(d.PixelData) == clip[-len(d.PixelData):] for d in dicoms)
    assert request._payloads == {}

    reference = service._extract_dicoms(PredictRequest(**json.loads(body)))
    assert [d.SOPInstanceUID for d in reference] == [d.SOPInstanceUID for d in dicoms]

    wrapped = parse_predict_request(make_body({"0": {"0": {"image": b64(clip)}, "1": {"image": b64(still)}}}))
    assert [d.SOPInstanceUID for d in service._extract_dicoms(wrapped)] == [d.SOPInstanceUID for d in dicoms]


def test_large_string_threshold_keeps_small_documents_whole():
    body = make_body({"0": {"0": "A" * (LARGE_STRING_BYTES - 1)}})
    document, payloads = split_large_strings(body)
    assert document == body and payloads == {}
