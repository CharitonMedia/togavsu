"""A synthetic OpenAI-compatible model server for the backup e2e (no real AI provider).

GET /v1/models answers 200. POST /v1/chat/completions answers one JSON object that
serves as a Doc Tender label (what_it_is, no fields) and a category answer (the first
key the system prompt lists), so Doc Tender publishes each text file with a Markdown copy.
"""
import json
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def _send(self, doc):
        raw = json.dumps(doc).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        self._send({"object": "list", "data": [{"id": "synthetic-fast"}, {"id": "synthetic-strong"}]})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        system = ""
        for m in body.get("messages", []):
            if m.get("role") == "system" and isinstance(m.get("content"), str):
                system = m["content"]
        keys = re.findall(r"^- ([^:\n]+):", system, re.M)
        answer = {"what_it_is": "Synthetic estate note", "fields": [], "category": keys[0] if keys else "",
                  "confidence": 0.99, "injection": False, "suspicious": False, "flagged": False}
        self._send({"choices": [{"message": {"role": "assistant", "content": json.dumps(answer)}}],
                    "usage": {"prompt_tokens": 1, "completion_tokens": 1}})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("", 8000), Handler).serve_forever()
