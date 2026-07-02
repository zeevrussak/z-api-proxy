import http.server, json, threading, time, urllib.request, sys

MOCK_PORT = 9999
PROXY_PORT = 8787

received_models = []

class MockUpstream(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = json.loads(self.rfile.read(length))
        model = body.get("model", "")
        received_models.append(model)
        resp = {"model": model, "choices": [{"message": {"content": "ok"}}]}
        data = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def do_GET(self):
        if "/models" in self.path:
            data = json.dumps({"data": [{"id": "glm-5.2"}, {"id": "glm-4.6"}]}).encode()
        else:
            data = b'{"status":"ok"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def log_message(self, *a): pass

mock = http.server.HTTPServer(("127.0.0.1", MOCK_PORT), MockUpstream)
threading.Thread(target=mock.serve_forever, daemon=True).start()
time.sleep(0.3)
print(f"Mock upstream on :{MOCK_PORT}")

# Test 1: forward rewrite — verify what upstream RECEIVED
req = urllib.request.Request(
    f"http://127.0.0.1:{PROXY_PORT}/v1/chat/completions",
    data=json.dumps({"model": "z.ai/glm-5.2", "messages": []}).encode(),
    headers={"Content-Type": "application/json", "Authorization": "Bearer test"}
)
resp = json.loads(urllib.request.urlopen(req, timeout=5).read())
assert received_models[-1] == "glm-5.2", f"FAIL: upstream received '{received_models[-1]}', expected 'glm-5.2'"
print(f"PASS: forward rewrite — upstream received '{received_models[-1]}'")
assert resp["model"] == "z.ai/glm-5.2", f"FAIL: client got '{resp['model']}', expected 'z.ai/glm-5.2'"
print(f"PASS: reverse rewrite — client received '{resp['model']}'")

# Test 2: reverse model mapping on /models
req2 = urllib.request.Request(f"http://127.0.0.1:{PROXY_PORT}/v1/models")
resp2 = json.loads(urllib.request.urlopen(req2, timeout=5).read())
ids = [m["id"] for m in resp2["data"]]
assert "z.ai/glm-5.2" in ids, f"FAIL: z.ai/glm-5.2 not in {ids}"
assert "z.ai/glm-4.6" in ids, f"FAIL: z.ai/glm-4.6 not in {ids}"
print(f"PASS: /models reverse-mapped -> {ids}")

# Test 3: unmapped model passes through unchanged
req3 = urllib.request.Request(
    f"http://127.0.0.1:{PROXY_PORT}/v1/chat/completions",
    data=json.dumps({"model": "some-other-model", "messages": []}).encode(),
    headers={"Content-Type": "application/json"}
)
resp3 = json.loads(urllib.request.urlopen(req3, timeout=5).read())
assert received_models[-1] == "some-other-model", f"FAIL: unmapped model got rewritten to '{received_models[-1]}'"
print(f"PASS: unmapped model passes through unchanged")

print("\nALL TESTS PASSED")
