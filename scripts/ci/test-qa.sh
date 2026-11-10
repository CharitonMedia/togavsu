#!/bin/sh
# QA harness tests: every unittest under tests/ in a clean virtualenv, then the harness
# integrity run. Needs tesseract-ocr, tesseract-ocr-eng and fonts-dejavu-core.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
command -v tesseract >/dev/null || { echo "tesseract is required" >&2; exit 1; }
venv=${TOGAVSU_QA_VENV:-$(mktemp -d)/venv}
python3 -m venv "$venv"
"$venv/bin/python" -m pip install --quiet -r tests/requirements-dev.txt
"$venv/bin/python" -m unittest discover -s tests -p 'test_*.py'
"$venv/bin/python" -m tests.harness
echo "QA harness tests passed"
