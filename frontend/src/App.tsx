import { ThemeProvider } from "@/components/theme-provider";
import { Header } from "@/components/header";
import { DocumentUploadForm } from "@/components/document-upload-form";
import { useAuth } from "@/auth/auth-context";
import { LoginPage, RegisterPage } from "@/pages/login-page";

function App() {
  const { status } = useAuth();
  const path = window.location.pathname;
  if (status === "loading") return <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">Checking your session...</div>;
  if (status === "unauthenticated") return path === "/register" ? <RegisterPage /> : <LoginPage />;
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
