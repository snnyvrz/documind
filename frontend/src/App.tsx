import { ThemeProvider } from "@/components/theme-provider";
import { Header } from "@/components/header";
import { DocumentUploadForm } from "@/components/document-upload-form";

function App() {
  return (
    <ThemeProvider defaultTheme="dark" storageKey="vite-ui-theme">
      <Header />
      <main className="mx-auto flex w-full max-w-3xl flex-col px-6 pt-16 pb-24">
        <div className="mb-8 space-y-2">
          <h2 className="text-3xl font-semibold tracking-tight">
            Add a document
          </h2>
          <p className="text-muted-foreground">
            Upload a PDF to make it ready for analysis.
          </p>
        </div>

        <DocumentUploadForm />
      </main>
    </ThemeProvider>
  );
}

export default App;
