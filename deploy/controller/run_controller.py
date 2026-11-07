"""Control-only entrypoint; expose only the existing Cursor key to this process."""
import os
from pathlib import Path
import runpy
import sys

if "CURSOR_API_KEY" not in os.environ:
    for line in Path("/home/patrick/.env").read_text().splitlines():
        if line.startswith("CURSOR_API_KEY="):
            os.environ["CURSOR_API_KEY"] = line.split("=", 1)[1].strip().strip("\"'")
            break
if not os.environ.get("CURSOR_API_KEY"):
    raise SystemExit("Cursor control credential unavailable")
script = Path(__file__).resolve().with_name("build_controller.py")
sys.argv[0] = str(script)
runpy.run_path(str(script), run_name="__main__")
