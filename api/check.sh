#!/bin/sh
# API contract check. The platform owner can call this from product CI.
# It does not execute runtime fixtures, install the product, or claim acceptance.
#
# Install, from the repository root:
#   python3 -m pip install -r api/requirements-dev.txt
# Run:
#   sh api/check.sh
set -eu

cd "$(dirname "$0")/.."

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

if [ ! -f api/requirements-dev.txt ]; then
  echo "missing api/requirements-dev.txt" >&2
  exit 1
fi

for path in \
  api/openapi/v1/openapi.json \
  api/openapi/marketplace/openapi.json \
  api/openapi/v1/mcp-tools.json \
  docs/api/coverage.md \
  api/tests/test_contract.py \
  api/tests/test_stage1.py
do
  if [ ! -f "$path" ]; then
    echo "missing $path" >&2
    exit 1
  fi
done

if ! python3 -c 'import openapi_spec_validator' >/dev/null 2>&1; then
  echo "openapi-spec-validator is not installed. Run: python3 -m pip install -r api/requirements-dev.txt" >&2
  exit 1
fi

python3 - << 'PY'
import importlib.metadata
import sys

version = importlib.metadata.version("openapi-spec-validator")
if version != "0.9.0":
    print(f"openapi-spec-validator {version} is installed; api/requirements-dev.txt pins 0.9.0", file=sys.stderr)
    sys.exit(1)
PY

export PYTHONPATH=api
found=0
set --
for file in api/tests/test_*.py; do
  if [ ! -f "$file" ]; then
    echo "no tests in api/tests" >&2
    exit 1
  fi
  found=1
  name=$(basename "$file" .py)
  set -- "$@" "api.tests.$name"
done
if [ "$found" -eq 0 ]; then
  echo "no tests in api/tests" >&2
  exit 1
fi
python3 -m unittest "$@"

python3 -m openapi_spec_validator api/openapi/v1/openapi.json
python3 -m openapi_spec_validator api/openapi/marketplace/openapi.json

python3 - << 'PY'
import json
import sys

from openapi_spec_validator import validate
from openapi_spec_validator.readers import read_from_filename

spec, _ = read_from_filename("api/openapi/v1/openapi.json")
validate(spec)
problem = spec["components"]["schemas"]["Problem"]
if "code" not in problem.get("properties", {}):
    print("Problem schema did not resolve", file=sys.stderr)
    sys.exit(1)

broken = json.loads(json.dumps(spec))
broken["paths"]["/broken"] = {
    "get": {
        "responses": {
            "200": {
                "description": "broken",
                "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Missing"}}},
            }
        }
    }
}
try:
    validate(broken)
except Exception as exc:
    print(f"broken spec rejected: {type(exc).__name__}")
else:
    print("broken spec was accepted", file=sys.stderr)
    sys.exit(1)
PY

python3 -m contract.build
git diff --exit-code -- \
  api/openapi/v1/openapi.json \
  api/openapi/marketplace/openapi.json \
  api/openapi/v1/mcp-tools.json \
  docs/api/coverage.md

echo "api/check.sh passed. Runtime fixtures were not executed."
