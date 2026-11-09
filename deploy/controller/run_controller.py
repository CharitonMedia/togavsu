"""Control-only entrypoint. The Cursor backend needs the existing Cursor key; the Claude Actions
backend needs none here, because lane jobs use the repository's Max sign-in secret."""
import json
import os
from pathlib import Path
import runpy
import sys

plan = sys.argv[sys.argv.index("--plan") + 1] if "--plan" in sys.argv[:-1] else None
backend = json.loads(Path(plan).read_text()).get("worker_backend", "cursor") if plan else "cursor"
if backend == "cursor":
    if "CURSOR_API_KEY" not in os.environ:
        for line in Path("/home/patrick/.env").read_text().splitlines():
            if line.startswith("CURSOR_API_KEY="):
                os.environ["CURSOR_API_KEY"] = line.split("=", 1)[1].strip().strip("\"'")
                break
    if not os.environ.get("CURSOR_API_KEY"):
        raise SystemExit("Cursor control credential unavailable")
else:
    os.environ.pop("CURSOR_API_KEY", None)
script = Path(__file__).resolve().with_name("build_controller.py")
sys.argv[0] = str(script)
runpy.run_path(str(script), run_name="__main__")
