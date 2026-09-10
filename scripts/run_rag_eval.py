#!/usr/bin/env python3
"""Run the versioned RAG set through the public HTTP API."""

import argparse
import json
import subprocess
import time
import urllib.request
import uuid
import os
import urllib.error
from pathlib import Path


class EvaluationClient:
    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())

    def request_json(self, url: str, method: str = "GET", body: bytes | None = None) -> dict:
        request = urllib.request.Request(url, data=body, method=method, headers={"Content-Type": "application/json"})
        try:
            response = self.opener.open(request)
        except urllib.error.HTTPError as error:
            detail = error.read().decode(errors="replace")
            raise RuntimeError(f"{method} {url} failed with HTTP {error.code}: {detail}") from error
        with response:
            return json.loads(response.read())

    def authenticate(self, email: str, password: str, register: bool) -> None:
        endpoint = "register" if register else "login"
        self.request_json(f"{self.base_url}/auth/{endpoint}", "POST", json.dumps({"email": email, "password": password}).encode())

    def upload(self, path: Path) -> str:
        boundary = uuid.uuid4().hex
        content = path.read_bytes()
        body = b"--" + boundary.encode() + b'\r\nContent-Disposition: form-data; name="file"; filename="' + path.name.encode() + b'"\r\nContent-Type: application/pdf\r\n\r\n' + content + b"\r\n--" + boundary.encode() + b"--\r\n"
        request = urllib.request.Request(f"{self.base_url}/documents", data=body, method="POST", headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
        try:
            response = self.opener.open(request)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"upload failed with HTTP {error.code}: {error.read().decode(errors='replace')}") from error
        with response:
            return json.loads(response.read())["documentId"]

    def ask(self, document_id: str, question: str) -> dict:
        started = time.monotonic()
        request = urllib.request.Request(f"{self.base_url}/documents/{document_id}/questions", data=json.dumps({"question": question}).encode(), method="POST", headers={"Content-Type": "application/json"})
        answer, sources = [], []
        completed = False
        with self.opener.open(request) as response:
            event, data = None, []
            for line in response:
                value = line.decode().rstrip("\n")
                if value.startswith("event: "):
                    event = value[7:]
                elif value.startswith("data: "):
                    data.append(value[6:])
                elif not value and event and data:
                    payload = json.loads("".join(data))
                    if event == "token":
                        if not isinstance(payload.get("text"), str):
                            raise RuntimeError("SSE token event has invalid text")
                        answer.append(payload["text"])
                    elif event == "sources":
                        sources = payload["sources"]
                    elif event == "error":
                        raise RuntimeError(f"SSE error {payload.get('code', 'unknown')}: {payload.get('message', 'unknown error')}")
                    elif event == "done":
                        if payload.get("ok") is not True:
                            raise RuntimeError("SSE stream completed unsuccessfully")
                        completed = True
                    event, data = None, []
        if not completed:
            raise RuntimeError("SSE stream ended without a successful done event")
        return {"answer": "".join(answer), "sources": sources, "latencySeconds": time.monotonic() - started}

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:1323")
    parser.add_argument("--dataset", type=Path, default=Path("evals/rag_questions.jsonl"))
    parser.add_argument("--documents", type=Path, default=Path("evals/documents"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--email", default=os.environ.get("DOCUMIND_EVAL_EMAIL"))
    parser.add_argument("--password", default=os.environ.get("DOCUMIND_EVAL_PASSWORD"))
    parser.add_argument("--register", action="store_true")
    args = parser.parse_args()
    records = [json.loads(line) for line in args.dataset.read_text().splitlines() if line.strip()]
    client = EvaluationClient(args.base_url)
    if not args.email or not args.password:
        parser.error("--email and --password or DOCUMIND_EVAL_EMAIL and DOCUMIND_EVAL_PASSWORD are required")
    client.authenticate(args.email, args.password, args.register)
    document_ids = {}
    results = []
    for record in records:
        fixture = args.documents / record["document"]
        if record["document"] not in document_ids:
            document_ids[record["document"]] = client.upload(fixture)
        document_id = document_ids[record["document"]]
        status = client.request_json(f"{args.base_url}/documents/{document_id}")
        while status.get("status") in {"queued", "processing"}:
            time.sleep(1)
            status = client.request_json(f"{args.base_url}/documents/{document_id}")
        if status.get("status") != "completed":
            raise RuntimeError(f"document {document_id} ended with status {status.get('status')}: {status.get('error', '')}")
        result = client.ask(document_id, record["question"])
        result.update({"id": record["id"], "answerable": record["answerable"], "referenceAnswer": record["reference_answer"], "goldPages": [page for passage in record["supporting_passages"] for page in range(passage["page_start"], passage["page_end"] + 1)]})
        results.append(result)
    output = {"gitRevision": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "questionCount": len(records), "results": results}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(output, indent=2) + "\n")


if __name__ == "__main__":
    main()
