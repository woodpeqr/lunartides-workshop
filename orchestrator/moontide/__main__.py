import logging
import signal
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

from moontide import telemetry, pipeline

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(name)s %(levelname)s %(message)s",
)


class HealthHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"status":"ok"}')

    def log_message(self, format, *args):
        pass  # suppress default httpserver logs


def main():
    stop_event = threading.Event()

    def handle_shutdown(sig, frame):
        stop_event.set()

    signal.signal(signal.SIGINT, handle_shutdown)
    signal.signal(signal.SIGTERM, handle_shutdown)

    # 1. Start health server (telemetry will call /health for startup trace)
    health_server = HTTPServer(("", 8080), HealthHandler)
    health_thread = threading.Thread(target=health_server.serve_forever, daemon=True)
    health_thread.start()
    time.sleep(0.05)  # brief wait for server to bind

    # 2. Set up OTel providers
    telemetry.setup()

    # 3. Emit startup signals
    telemetry.emit_startup_signals()

    # 4. Run the pipeline loop
    pipeline.run(stop_event)

    health_server.shutdown()


if __name__ == "__main__":
    main()
