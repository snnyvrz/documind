import type { AnswerSource } from "@/hooks/use-answer-stream";
import { FileText, ExternalLink } from "lucide-react";

type Props = {
  source: AnswerSource;
  onOpen: (page: number) => void;
};

export function RetrievedSourceCard({ source, onOpen }: Props) {
  const pageLabel = source.pageStart === source.pageEnd ? `Page ${source.pageStart}` : `Pages ${source.pageStart}-${source.pageEnd}`;

  return (
    <details className="group rounded-2xl border bg-background/60">
      <summary className="flex cursor-pointer list-none items-center gap-3 p-4 [&::-webkit-details-marker]:hidden">
        <FileText className="size-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-medium">Retrieved passage {source.chunkIndex + 1}</span>
          <span className="mt-0.5 block text-xs text-muted-foreground">{pageLabel}</span>
        </span>
        <span className="text-xs text-muted-foreground group-open:hidden">Expand</span>
      </summary>
      <div className="space-y-3 border-t px-4 pb-4 pt-3">
        <p className="whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{source.text}</p>
        <button type="button" onClick={() => onOpen(source.pageStart)} className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline">
          Open in PDF <ExternalLink className="size-3.5" aria-hidden="true" />
        </button>
      </div>
    </details>
  );
}
