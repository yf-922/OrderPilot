"""Small resident tokenizer service for strict Agent prompt budgets."""
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import tiktoken

DEFAULT_ENCODING = os.getenv("AGENT_TOKEN_ENCODING", "cl100k_base")


def count_payload(payload):
    encoding_name = payload.get("encoding") or DEFAULT_ENCODING
    encoding = tiktoken.get_encoding(encoding_name)
    envelope = {"messages": payload.get("messages", []), "tools": payload.get("tools", [])}
    encoded = json.dumps(envelope, ensure_ascii=False, separators=(",", ":"))
    return len(encoding.encode(encoded)), encoding_name


class Handler(BaseHTTPRequestHandler):
    def _write(self, status, payload):
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/healthz":
            self._write(200, {"status": "ok", "encoding": DEFAULT_ENCODING})
            return
        self._write(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/v1/count":
            self._write(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length) or b"{}")
            tokens, encoding = count_payload(payload)
            self._write(200, {"tokens": tokens, "encoding": encoding})
        except Exception as exc:
            self._write(400, {"error": str(exc)})

    def log_message(self, *_args):
        return


if __name__ == "__main__":
    port = int(os.getenv("AGENT_TOKENIZER_PORT", "8091"))
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
