import { Button } from "@/components/ui/button";
import { PdfPreview } from "@/components/pdf-preview";
import { RetrievedSourceCard } from "@/components/retrieved-source-card";
import { useAnswerStream } from "@/hooks/use-answer-stream";
import { useEffect, useState } from "react";
import type { QuestionHistoryItem } from "@/documents/document-types";

type Props = {
  documentId: string;
  filename: string;
  pageCount?: number;
  history: QuestionHistoryItem[];
  onHistory: (item: QuestionHistoryItem) => void;
  onNewHistory: (item: QuestionHistoryItem) => void;
};

export function DocumentQuestionPanel({ documentId, filename, pageCount, history, onHistory, onNewHistory }: Props) {
  const [question, setQuestion] = useState("");
  const [previewPage, setPreviewPage] = useState<number | null>(null);
  const [selectedHistory, setSelectedHistory] = useState<QuestionHistoryItem | null>(null);
  const answerStream = useAnswerStream(documentId);

  useEffect(() => {
    if (answerStream.historyItem && !history.some((item) => item.id === answerStream.historyItem?.id)) onNewHistory(answerStream.historyItem);
  }, [answerStream.historyItem, history, onNewHistory]);

  const ask = () => {
    setSelectedHistory(null);
    void answerStream.ask(question);
  };
  const displayedAnswer = selectedHistory?.answer ?? answerStream.answer;
  const displayedSources = selectedHistory?.sources ?? answerStream.sources;

  return (
    <section className="space-y-5 rounded-3xl border bg-card/40 p-5 sm:p-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-wider text-emerald-600 dark:text-emerald-400">Document ready</p>
        <h3 className="mt-2 text-2xl font-semibold">Ask about this document</h3>
        <p className="mt-1 truncate text-sm text-muted-foreground">{filename} · {pageCount ?? 0} pages</p>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <label htmlFor="document-question" className="sr-only">Question</label>
        <input
          id="document-question"
          value={question}
          onChange={(event) => setQuestion(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              if (!answerStream.isAsking) ask();
            }
          }}
          placeholder="What is this document about?"
          className="min-w-0 flex-1 rounded-2xl border bg-background px-4 py-2.5 text-sm outline-none transition focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
        />
        <Button
          type="button"
          onClick={() => (answerStream.isAsking ? answerStream.stop() : ask())}
          disabled={!answerStream.isAsking && !question.trim()}
          className="sm:min-w-20"
        >
          {answerStream.isAsking ? "Stop" : "Ask"}
        </Button>
      </div>
      {answerStream.error && <p className="text-sm text-destructive">{answerStream.error}</p>}
      {displayedAnswer && (
        <div className="rounded-2xl border bg-background/70 p-5">
          <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Answer</p>
          <p className="whitespace-pre-wrap text-sm leading-7">{displayedAnswer}</p>
        </div>
      )}
      {displayedSources.length > 0 && (
        <div className="space-y-3 rounded-2xl border bg-background/40 p-4 sm:p-5">
          <div>
            <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Retrieved passages</p>
            <p className="mt-1 text-xs text-muted-foreground">These passages were retrieved as context. They are not claim-level citations.</p>
          </div>
          <div className="space-y-2">
            {displayedSources.map((source) => (
              <RetrievedSourceCard key={source.chunkIndex} source={source} onOpen={setPreviewPage} />
            ))}
          </div>
        </div>
      )}
      {previewPage !== null && (
        <PdfPreview
          documentId={documentId}
          filename={filename}
          pageCount={pageCount}
          initialPage={previewPage}
          onClose={() => setPreviewPage(null)}
        />
      )}
      {history.length > 0 && <div className="space-y-3 border-t pt-5">
        <div><p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Question history</p><p className="mt-1 text-sm text-muted-foreground">Saved answers from this document.</p></div>
        <div className="space-y-2">{history.map((item) => <button key={item.id} type="button" onClick={() => { setSelectedHistory(item); onHistory(item); }} className={`block w-full rounded-2xl border bg-background/50 p-4 text-left hover:bg-accent ${selectedHistory?.id === item.id ? "border-primary bg-accent" : ""}`}><p className="text-sm font-medium">{item.question}</p><p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{item.answer}</p><p className="mt-2 text-xs text-muted-foreground">{new Date(item.createdAt).toLocaleString()}</p></button>)}</div>
      </div>}
    </section>
  );
}
