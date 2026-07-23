import { ModeToggle } from "./components/mode-toggle";
import { ThemeProvider } from "./components/theme-provider";

function App() {
  return (
    <ThemeProvider defaultTheme="dark" storageKey="vite-ui-theme">
      <p>Welcome to Documind!</p>
      <ModeToggle />
    </ThemeProvider>
  );
}

export default App;
