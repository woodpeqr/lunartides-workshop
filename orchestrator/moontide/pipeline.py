import logging

import requests

from moontide import config

_logger = logging.getLogger(__name__)

SAMPLE_CONTACT = {
    "name": "Alex Johnson",
    "company": "Acme Corp",
    "email": "alex.johnson@acme.com",
}


def run_once() -> None:
    """Run one pass of the enrichment pipeline."""
    contact = SAMPLE_CONTACT.copy()

    # Step 1: Validate
    try:
        resp = requests.post(config.FLUX_URL, json=contact, timeout=10)
        resp.raise_for_status()
        contact = resp.json()
    except Exception as exc:
        _logger.warning("validate failed: %s", exc)

    # Step 2: Enrich
    try:
        resp = requests.post(config.RIFT_URL, json=contact, timeout=10)
        resp.raise_for_status()
        contact = resp.json()
    except Exception as exc:
        _logger.warning("enrich failed: %s", exc)

    # Step 3: Score
    try:
        resp = requests.post(config.SWELL_URL, json=contact, timeout=10)
        resp.raise_for_status()
    except Exception as exc:
        _logger.warning("score failed: %s", exc)


def run(stop_event) -> None:
    """Run the pipeline loop until stop_event is set."""
    while not stop_event.is_set():
        run_once()
        stop_event.wait(config.PIPELINE_INTERVAL_SECONDS)
