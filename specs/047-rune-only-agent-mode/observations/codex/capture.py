"""Log the body of every POST and answer 400, so a client shows what it would send a model."""
import http.server, json, sys

out = sys.argv[2]

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("content-length", 0))
        body = self.rfile.read(n)
        with open(out, "ab") as f:
            f.write(json.dumps({"path": self.path, "encoding": self.headers.get("content-encoding")}).encode() + b"\n" + body + b"\n")
        self.send_response(400); self.send_header("content-type", "application/json"); self.end_headers()
        self.wfile.write(b'{"error":{"message":"capture only"}}')
    def do_GET(self):
        self.send_response(404); self.end_headers()
    def log_message(self, *a): pass

http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
