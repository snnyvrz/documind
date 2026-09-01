from concurrent import futures
from io import BytesIO
from typing import Any

import grpc
from fastapi import FastAPI
from pypdf import PdfReader

from generated import extractor_pb2, extractor_pb2_grpc

app = FastAPI(title="DocuMind PDF extractor")

GRPC_ADDRESS = "[::]:50051"
GRPC_MAX_WORKERS = 4


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


class TextExtractor(extractor_pb2_grpc.TextExtractorServicer):
    def Extract(self, request: Any, context: grpc.ServicerContext) -> Any:
        try:
            reader = PdfReader(BytesIO(request.pdf))
            text = "\n\n".join(
                page.extract_text() or "" for page in reader.pages
            ).strip()
            return extractor_pb2.ExtractResponse(
                document_id=request.document_id,
                text=text,
                page_count=len(reader.pages),
            )
        except Exception as error:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"could not extract PDF text: {error}",
            )


def serve_grpc() -> None:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=GRPC_MAX_WORKERS))
    extractor_pb2_grpc.add_TextExtractorServicer_to_server(TextExtractor(), server)
    server.add_insecure_port(GRPC_ADDRESS)
    server.start()
    try:
        server.wait_for_termination()
    finally:
        server.stop(grace=5)


if __name__ == "__main__":
    serve_grpc()
