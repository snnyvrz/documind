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
       <main className="mx-auto flex w-full max-w-7xl flex-col px-4 pt-8 pb-10 sm:px-6">
        <div className="mb-8 space-y-2">
          <h2 className="text-3xl font-semibold tracking-tight">
            Your document workspace
          </h2>
          <p className="text-muted-foreground">
             Search, prepare, and ask questions across your documents.
          </p>
        </div>

        <DocumentUploadForm />
      </main>
    </ThemeProvider>
  );
}

export default App;
