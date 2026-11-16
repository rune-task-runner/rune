"""Log the body of every POST and answer 400, so a client shows what it would send a model.

    python3 -I capture.py PORT FILE
"""
import http.server
import json
import sys


def handler(out):
    """Return a request handler that appends every POST to the file named out."""

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            length = int(self.headers.get("content-length", 0))
            body = self.rfile.read(length)
            about = {"path": self.path, "encoding": self.headers.get("content-encoding")}
            with open(out, "ab") as f:
                f.write(json.dumps(about).encode() + b"\n" + body + b"\n")
            self.send_response(400)
            self.send_header("content-type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":{"message":"capture only"}}')

        def do_GET(self):
            self.send_response(404)
            self.end_headers()

        def log_message(self, *args):
            pass

    return Handler


def serve(port, out):
    """Return a server on loopback that captures to the file named out. Port 0 picks a free port."""
    return http.server.HTTPServer(("127.0.0.1", port), handler(out))


if __name__ == "__main__":
    serve(int(sys.argv[1]), sys.argv[2]).serve_forever()
