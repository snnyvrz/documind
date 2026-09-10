from types import SimpleNamespace
from unittest.mock import Mock, patch

import grpc
import httpx
import pytest

from service import DocumentProcessor, chat, chunks


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


def test_chunks_preserve_page_ranges() -> None:
    values = chunks("alpha beta gamma delta", [(0, 11, 1), (13, 24, 2)])

    assert values[0][0] == "alpha beta gamma delta"
    assert values[0][3:] == (1, 2)


def test_chunks_preserve_spans_for_repeated_words() -> None:
    text = "word " * 7 + "word"

    with patch("service.TOKEN_CHUNK_SIZE", 4), patch("service.TOKEN_CHUNK_OVERLAP", 1):
        values = chunks(text)

    assert [(value, start, end) for value, start, end, _, _ in values] == [
        ("word word word word", 0, 19),
        ("word word word word", 15, 34),
        ("word word", 30, 39),
    ]


def test_chunks_use_original_spans_with_irregular_whitespace() -> None:
    text = "one  two\n\tthree    four"

    values = chunks(text)

    assert values == [("one  two\n\tthree    four", 0, len(text), 1, 1)]


def test_chunks_page_ranges_use_original_chunk_span() -> None:
    text = "one two three four five six"

    with patch("service.TOKEN_CHUNK_SIZE", 4), patch("service.TOKEN_CHUNK_OVERLAP", 0):
        values = chunks(text, [(0, 13, 1), (13, len(text), 2)])

    assert values[0][1:5] == (0, 18, 1, 2)
    assert values[1][1:5] == (19, len(text), 2, 2)


def test_process_rejects_documents_without_extractable_text() -> None:
    page = Mock()
    page.extract_text.return_value = ""
    context = aborting_context()

    with patch("service.PdfReader", return_value=SimpleNamespace(pages=[page])), pytest.raises(AbortCalled):
        list(DocumentProcessor().Process(SimpleNamespace(pdf=b"%PDF-", document_id="document-1"), context))

    context.abort.assert_called_once_with(grpc.StatusCode.INVALID_ARGUMENT, "could not process PDF: PDF contains no extractable text; OCR is required for scanned PDFs")


@pytest.mark.parametrize("close_after", [1, 2, 3, 4])
def test_process_releases_ingestion_slot_when_stream_is_closed_at_each_stage(close_after: int) -> None:
    request = SimpleNamespace(pdf=b"%PDF-", document_id="document-1")
    context = Mock()
    context.is_active.return_value = True
    document_chunks = [
        ("first", 0, 5, 1, 1),
        ("second", 6, 12, 1, 1),
        ("third", 13, 18, 1, 1),
    ]

    with (
        patch("service.extract_pages", return_value=["document text"]),
        patch("service.chunks", return_value=document_chunks),
        patch("service.EMBEDDING_BATCH_SIZE", 1),
        patch("service.embed", return_value=[[0.0] * 768]),
        patch("service.release_slot") as release,
    ):
        events = DocumentProcessor().Process(request, context)
        for _ in range(close_after):
            next(events)
        events.close()

    release.assert_called_once()


def test_process_releases_ingestion_slot_when_context_becomes_inactive() -> None:
    request = SimpleNamespace(pdf=b"%PDF-", document_id="document-1")
    context = Mock()
    context.is_active.return_value = False

    with (
        patch("service.extract_pages", return_value=["document text"]),
        patch("service.chunks", return_value=[("chunk", 0, 5, 1, 1)]),
        patch("service.embed") as embed,
        patch("service.release_slot") as release,
    ):
        events = DocumentProcessor().Process(request, context)
        next(events)
        with pytest.raises(StopIteration):
            next(events)

    embed.assert_not_called()
    release.assert_called_once()


def test_chat_places_temperature_in_ollama_options() -> None:
    response = Mock()
    response.__enter__ = Mock(return_value=response)
    response.__exit__ = Mock(return_value=False)
    response.iter_lines.return_value = [b'{"message":{"content":"answer"}}']

    with patch("service.httpx.stream", return_value=response) as stream:
        assert list(chat("question", [SimpleNamespace(chunk_index=2, page_start=3, page_end=4, text="evidence")])) == ["answer"]

    payload = stream.call_args.kwargs["json"]
    assert payload["options"] == {"temperature": 0}
    assert "temperature" not in payload


def test_ready_checks_models_with_post() -> None:
    import service

    responses = [Mock(status_code=200), Mock(status_code=200)]
    with patch("service.httpx.post", side_effect=responses) as post:
        response = service.ready()

    assert response.status_code == 200
    assert post.call_count == 2
    assert all(call.args[0].endswith("/api/show") for call in post.call_args_list)
    assert all(call.kwargs["json"]["name"] for call in post.call_args_list)
