#!/usr/bin/env python3
"""Request dump server for debugging aqua.

Run it, then point aqua at it:

    python3 scripts/dump-server.py &
    AQUA_BASE_URL=http://127.0.0.1:8799 AQUAVOICE_AVALON_KEY=x ./aqua

Prints every request: method, path, all headers (including the real
Authorization value — this is a debug tool, it shows secrets), and each
multipart field. Binary fields are shown as size plus head/tail bytes.
"""
import http.server

PORT = 8799


class H(http.server.BaseHTTPRequestHandler):
    def _dump(self):
        n = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(n)
        print(f"\n=== {self.command} {self.path}")
        for k, v in self.headers.items():
            print(f"  {k}: {v}")
        print(f"--- body: {len(body)} bytes ---")
        ctype = self.headers.get('Content-Type', '')
        bnd = ctype.split('boundary=')[-1].encode() if 'boundary=' in ctype else None
        if bnd:
            for part in body.split(b'--' + bnd):
                if b'Content-Disposition' not in part:
                    continue
                head, _, data = part.partition(b'\r\n\r\n')
                disp = head.decode(errors='replace')
                name = disp.split('name="')[-1].split('"')[0]
                data = data.rstrip(b'\r\n-')
                if len(data) < 200:
                    print(f"  field {name!r}: {data!r}")
                else:
                    print(f"  field {name!r}: {len(data)} bytes, "
                          f"head={data[:16]!r} tail={data[-16:]!r}")
        else:
            print(f"  raw: {body[:500]!r}")
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(b'{"text":"echo ok"}')

    do_GET = do_POST = do_PUT = _dump

    def log_message(self, *a):
        pass


print(f"dump server on :{PORT} — Ctrl-C to stop", flush=True)
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
