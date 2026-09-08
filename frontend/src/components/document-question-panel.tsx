import { Button } from "@/components/ui/button";
import { useAnswerStream } from "@/hooks/use-answer-stream";
import { useState } from "react";

type Props = {
  documentId: string;
  filename: string;
  pageCount?: number;
};

export function DocumentQuestionPanel({ documentId, filename, pageCount }: Props) {
  const [question, setQuestion] = useState("");
  const answerStream = useAnswerStream(documentId);

  const ask = () => void answerStream.ask(question);

  return (
    <section className="space-y-5 rounded-3xl border bg-card/40 p-5 sm:p-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-wider text-primary">Document ready</p>
        <h3 className="mt-2 text-xl font-semibold">Ask about this document</h3>
        <p className="mt-1 truncate text-sm text-muted-foreground">{filename} · {pageCount ?? 0} pages</p>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
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
      {answerStream.answer && (
        <div className="rounded-2xl border bg-background/70 p-5">
          <p className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Answer</p>
          <p className="whitespace-pre-wrap text-sm leading-7">{answerStream.answer}</p>
        </div>
      )}
    </section>
  );
}
