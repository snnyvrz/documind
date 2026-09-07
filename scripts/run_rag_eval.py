#!/usr/bin/env python3
"""Run the versioned RAG set through the public HTTP API."""

import argparse
import json
import subprocess
import time
import urllib.request
import uuid
from pathlib import Path


def request_json(url: str, method: str = "GET", body: bytes | None = None) -> dict:
    request = urllib.request.Request(url, data=body, method=method, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request) as response:
        return json.loads(response.read())


def upload(base_url: str, path: Path) -> str:
    boundary = uuid.uuid4().hex
    content = path.read_bytes()
    body = b"--" + boundary.encode() + b'\r\nContent-Disposition: form-data; name="file"; filename="' + path.name.encode() + b'"\r\nContent-Type: application/pdf\r\n\r\n' + content + b"\r\n--" + boundary.encode() + b"--\r\n"
    request = urllib.request.Request(f"{base_url}/documents", data=body, method="POST", headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    with urllib.request.urlopen(request) as response:
        return json.loads(response.read())["documentId"]


def ask(base_url: str, document_id: str, question: str) -> dict:
    started = time.monotonic()
    request = urllib.request.Request(f"{base_url}/documents/{document_id}/questions", data=json.dumps({"question": question}).encode(), method="POST", headers={"Content-Type": "application/json"})
    answer, sources = [], []
    with urllib.request.urlopen(request) as response:
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
                    answer.append(payload["text"])
                elif event == "sources":
                    sources = payload["sources"]
                event, data = None, []
    return {"answer": "".join(answer), "sources": sources, "latencySeconds": time.monotonic() - started}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:1323")
    parser.add_argument("--dataset", type=Path, default=Path("evals/rag_questions.jsonl"))
    parser.add_argument("--documents", type=Path, default=Path("evals/documents"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    records = [json.loads(line) for line in args.dataset.read_text().splitlines() if line.strip()]
    document_ids = {}
    results = []
    for record in records:
        fixture = args.documents / record["document"]
        if record["document"] not in document_ids:
            document_ids[record["document"]] = upload(args.base_url, fixture)
        document_id = document_ids[record["document"]]
        status = request_json(f"{args.base_url}/documents/{document_id}")
        while status.get("status") in {"queued", "processing"}:
            time.sleep(1)
            status = request_json(f"{args.base_url}/documents/{document_id}")
        result = ask(args.base_url, document_id, record["question"])
        result.update({"id": record["id"], "answerable": record["answerable"], "referenceAnswer": record["reference_answer"], "goldPages": [page for passage in record["supporting_passages"] for page in range(passage["page_start"], passage["page_end"] + 1)]})
        results.append(result)
    output = {"gitRevision": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(), "questionCount": len(records), "results": results}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(output, indent=2) + "\n")


if __name__ == "__main__":
    main()
