from types import SimpleNamespace
from unittest.mock import Mock, patch

import grpc
import httpx
import pytest

from service import DocumentProcessor


class AbortCalled(Exception):
    pass


def aborting_context() -> Mock:
    context = Mock()
    context.abort.side_effect = AbortCalled
    return context


def test_process_classifies_malformed_pdf_as_invalid_argument() -> None:
    request = SimpleNamespace(pdf=b"not a PDF", document_id="document-1")
    context = aborting_context()

    with (
        patch("service.PdfReader", side_effect=ValueError("malformed PDF")),
        patch("service.embed") as embed,
        pytest.raises(AbortCalled),
    ):
        list(DocumentProcessor().Process(request, context))

    context.abort.assert_called_once_with(
        grpc.StatusCode.INVALID_ARGUMENT,
        "could not process PDF: malformed PDF",
    )
    embed.assert_not_called()


def test_process_classifies_ollama_failure_as_unavailable() -> None:
    page = Mock()
    page.extract_text.return_value = "document text"
    reader = SimpleNamespace(pages=[page])
    request = SimpleNamespace(pdf=b"%PDF-", document_id="document-1")
    context = aborting_context()

    with (
        patch("service.PdfReader", return_value=reader),
        patch(
            "service.embed",
            side_effect=httpx.ConnectError("Ollama unavailable"),
        ),
    ):
        events = DocumentProcessor().Process(request, context)
        metadata = next(events)
        with pytest.raises(AbortCalled):
            next(events)

    assert metadata.metadata.document_id == "document-1"
    context.abort.assert_called_once_with(
        grpc.StatusCode.UNAVAILABLE,
        "could not embed PDF: Ollama unavailable",
    )
