"""Container runtime configuration that the predict path depends on."""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def test_nginx_does_not_cut_off_long_predictions():
    config = (ROOT / "nginx.conf").read_text(encoding="utf-8")
    location = config[config.index("location /api/"):]

    for directive in ("proxy_connect_timeout", "proxy_send_timeout", "proxy_read_timeout"):
        match = re.search(rf"{directive}\s+(\d+)s;", location)
        assert match, f"{directive} is not set; nginx would default to 60 s"
        assert int(match.group(1)) >= 1800
    assert "proxy_request_buffering off;" in location
    assert "client_max_body_size 0;" in location


def test_accelerated_decoders_are_installed_with_the_image():
    requirements = (ROOT / "requirements.txt").read_text(encoding="utf-8").splitlines()

    assert any(line.startswith("pybase64==") for line in requirements)
    assert any(line.startswith("pyjpegls==") for line in requirements)
