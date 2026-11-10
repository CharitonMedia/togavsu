"""Control-only entrypoint. The Cursor backend needs the existing Cursor key. The Claude Actions
backend needs none to launch, because lane jobs use the repository's Max sign-in secret; it still
loads the key when present so agents launched on Cursor before the switch are observed to the end."""
import json
import os
from pathlib import Path
import runpy
import sys

plan = sys.argv[sys.argv.index("--plan") + 1] if "--plan" in sys.argv[:-1] else None
backend = json.loads(Path(plan).read_text()).get("worker_backend", "cursor") if plan else "cursor"
env_file = Path("/home/patrick/.env")
if "CURSOR_API_KEY" not in os.environ and env_file.exists():
    for line in env_file.read_text().splitlines():
        if line.startswith("CURSOR_API_KEY="):
            os.environ["CURSOR_API_KEY"] = line.split("=", 1)[1].strip().strip("\"'")
            break
if backend == "cursor" and not os.environ.get("CURSOR_API_KEY"):
    raise SystemExit("Cursor control credential unavailable")
script = Path(__file__).resolve().with_name("build_controller.py")
sys.argv[0] = str(script)
runpy.run_path(str(script), run_name="__main__")
